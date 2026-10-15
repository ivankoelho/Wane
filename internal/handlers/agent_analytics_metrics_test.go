package handlers_test

import (
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// metricsEnv is an organization with a viewer allowed to see every agent.
type metricsEnv struct {
	app    *handlers.App
	org    *models.Organization
	viewer *models.User
}

func newMetricsEnv(t *testing.T) metricsEnv {
	t.Helper()
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	role := testutil.CreateTestRoleExact(t, app.DB, org.ID, "Analytics Viewer", false, false, getAnalyticsPermissions(t, app))
	viewer := testutil.CreateTestUser(t, app.DB, org.ID,
		testutil.WithEmail(testutil.UniqueEmail("viewer")),
		testutil.WithPassword("password"),
		testutil.WithRoleID(&role.ID),
	)
	return metricsEnv{app: app, org: org, viewer: viewer}
}

// agent creates a user who is an agent of a team (so the all-agents listing sees them).
func (e metricsEnv) agent(t *testing.T) *models.User {
	t.Helper()
	u := testutil.CreateTestUser(t, e.app.DB, e.org.ID, testutil.WithEmail(testutil.UniqueEmail("agent")))
	createTestTeamWithAgent(t, e.app, e.org.ID, u.ID)
	return u
}

// analytics calls the endpoint over an explicit, wide period (yesterday..tomorrow).
func (e metricsEnv) analytics(t *testing.T) handlers.AgentAnalyticsResponse {
	t.Helper()
	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, e.org.ID, e.viewer.ID)
	testutil.SetQueryParam(req, "from", time.Now().AddDate(0, 0, -2).Format("2006-01-02"))
	testutil.SetQueryParam(req, "to", time.Now().AddDate(0, 0, 1).Format("2006-01-02"))
	require.NoError(t, e.app.GetAgentAnalytics(req))
	var resp struct {
		Data handlers.AgentAnalyticsResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &resp))
	return resp.Data
}

func (e metricsEnv) statsOf(t *testing.T, agentID uuid.UUID) handlers.AgentPerformanceStats {
	t.Helper()
	for _, s := range e.analytics(t).AgentStats {
		if s.AgentID == agentID.String() {
			return s
		}
	}
	t.Fatalf("agent %s not in the listing", agentID)
	return handlers.AgentPerformanceStats{}
}

// message creates a message and, for a person's reply, stamps the sender.
func (e metricsEnv) message(t *testing.T, contactID uuid.UUID, dir models.Direction, at time.Time, sentBy *uuid.UUID) {
	t.Helper()
	m := createTestMessage(t, e.app, e.org.ID, contactID, dir, at)
	if sentBy != nil {
		require.NoError(t, e.app.DB.Model(&models.Message{}).Where("id = ?", m.ID).Update("sent_by_user_id", *sentBy).Error)
	}
}

// transfer creates a transfer for a fresh contact.
func (e metricsEnv) transfer(t *testing.T, agentID *uuid.UUID, status models.TransferStatus, transferredAt time.Time, resumedAt *time.Time) (*models.AgentTransfer, *models.Contact) {
	t.Helper()
	contact := testutil.CreateTestContact(t, e.app.DB, e.org.ID)
	return createTestAgentTransfer(t, e.app, e.org.ID, contact.ID, agentID, status, models.TransferSourceManual, transferredAt, resumedAt), contact
}

// The queue time is first pickup minus queue entry. updated_at moves for any
// edit and must not leak into the metric; attendances never picked up are out.
func TestAgentAnalytics_QueueTimeUsesFirstPickup(t *testing.T) {
	e := newMetricsEnv(t)
	agent := e.agent(t)
	now := time.Now()

	tr, _ := e.transfer(t, &agent.ID, models.TransferStatusActive, now.Add(-60*time.Minute), nil)
	pickedUp := now.Add(-50 * time.Minute) // waited 10 min
	require.NoError(t, e.app.DB.Model(&models.AgentTransfer{}).Where("id = ?", tr.ID).
		Updates(map[string]any{"picked_up_at": pickedUp, "updated_at": now}).Error) // updated_at: 60 min later
	e.transfer(t, nil, models.TransferStatusActive, now.Add(-30*time.Minute), nil) // still in the queue

	got := e.analytics(t).Summary.AvgQueueTimeMins
	assert.InDelta(t, 10, got, 0.1, "queue time = picked_up_at - transferred_at, ignoring updated_at and waiting items")
}

// Messages are attributed to who sent them, not to anyone who ever had a
// transfer with that contact.
func TestAgentAnalytics_MessagesSentAreAttributedToTheSender(t *testing.T) {
	e := newMetricsEnv(t)
	ana, bia := e.agent(t), e.agent(t)
	now := time.Now()

	_, contact := e.transfer(t, &ana.ID, models.TransferStatusActive, now.Add(-time.Hour), nil)
	e.message(t, contact.ID, models.DirectionOutgoing, now.Add(-50*time.Minute), &ana.ID)
	e.message(t, contact.ID, models.DirectionOutgoing, now.Add(-40*time.Minute), &ana.ID)
	e.message(t, contact.ID, models.DirectionOutgoing, now.Add(-30*time.Minute), &bia.ID) // someone else, same contact
	e.message(t, contact.ID, models.DirectionOutgoing, now.Add(-20*time.Minute), nil)     // bot
	e.message(t, contact.ID, models.DirectionIncoming, now.Add(-10*time.Minute), nil)     // customer

	assert.EqualValues(t, 2, e.statsOf(t, ana.ID).MessagesSent)
	assert.EqualValues(t, 1, e.statsOf(t, bia.ID).MessagesSent)
}

// The agent who closed an attendance gets the credit, regardless of who owns
// it at the end; an auto-closed (expired) attendance is nobody's resolution.
func TestAgentAnalytics_ResolvedIsCreditedToTheCloser(t *testing.T) {
	e := newMetricsEnv(t)
	ana, bia := e.agent(t), e.agent(t)
	now := time.Now()
	resumed := now.Add(-10 * time.Minute)

	// Ana handled it, then it moved to Bia, who closed it.
	tr, _ := e.transfer(t, &bia.ID, models.TransferStatusResumed, now.Add(-2*time.Hour), &resumed)
	require.NoError(t, e.app.DB.Model(&models.AgentTransfer{}).Where("id = ?", tr.ID).Update("resumed_by", bia.ID).Error)
	// Legacy row without resumed_by: falls back to the owner.
	e.transfer(t, &ana.ID, models.TransferStatusResumed, now.Add(-2*time.Hour), &resumed)
	// Auto-closed by SLA: not handled by anyone.
	e.transfer(t, &ana.ID, models.TransferStatusExpired, now.Add(-2*time.Hour), &resumed)

	assert.EqualValues(t, 1, e.statsOf(t, bia.ID).TransfersHandled)
	assert.EqualValues(t, 1, e.statsOf(t, ana.ID).TransfersHandled)
}

// Active attendances are split by who they wait for; "active" alone says
// nothing about whether the agent or the customer owes a reply.
func TestAgentAnalytics_ActiveAttendancesAreSplitByWhoIsWaiting(t *testing.T) {
	e := newMetricsEnv(t)
	agent := e.agent(t)
	now := time.Now()

	// Customer wrote last: waiting for the agent.
	_, c1 := e.transfer(t, &agent.ID, models.TransferStatusActive, now.Add(-time.Hour), nil)
	e.message(t, c1.ID, models.DirectionOutgoing, now.Add(-50*time.Minute), &agent.ID)
	e.message(t, c1.ID, models.DirectionIncoming, now.Add(-5*time.Minute), nil)
	// Agent wrote last: waiting for the customer.
	_, c2 := e.transfer(t, &agent.ID, models.TransferStatusActive, now.Add(-time.Hour), nil)
	e.message(t, c2.ID, models.DirectionIncoming, now.Add(-50*time.Minute), nil)
	e.message(t, c2.ID, models.DirectionOutgoing, now.Add(-5*time.Minute), &agent.ID)
	// Only a bot message since the attendance started: waiting for nobody yet.
	_, c3 := e.transfer(t, &agent.ID, models.TransferStatusActive, now.Add(-time.Hour), nil)
	e.message(t, c3.ID, models.DirectionOutgoing, now.Add(-5*time.Minute), nil)
	// A message from BEFORE the attendance does not count.
	_, c4 := e.transfer(t, &agent.ID, models.TransferStatusActive, now.Add(-time.Hour), nil)
	e.message(t, c4.ID, models.DirectionIncoming, now.Add(-3*time.Hour), nil)

	s := e.statsOf(t, agent.ID)
	assert.EqualValues(t, 4, s.ActiveTransfers)
	assert.EqualValues(t, 1, s.AwaitingAgent)
	assert.EqualValues(t, 1, s.AwaitingCustomer)
}

// SLA counters follow the SLA the processor already maintains.
func TestAgentAnalytics_SLACounters(t *testing.T) {
	e := newMetricsEnv(t)
	agent := e.agent(t)
	now := time.Now()

	risk, _ := e.transfer(t, &agent.ID, models.TransferStatusActive, now.Add(-time.Hour), nil)
	require.NoError(t, e.app.DB.Model(&models.AgentTransfer{}).Where("id = ?", risk.ID).Update("escalation_level", 1).Error)
	late, _ := e.transfer(t, &agent.ID, models.TransferStatusActive, now.Add(-time.Hour), nil)
	require.NoError(t, e.app.DB.Model(&models.AgentTransfer{}).Where("id = ?", late.ID).
		Updates(map[string]any{"escalation_level": 2, "sla_breached": true}).Error)
	e.transfer(t, &agent.ID, models.TransferStatusActive, now.Add(-time.Hour), nil)

	s := e.statsOf(t, agent.ID)
	assert.EqualValues(t, 1, s.SLAAtRisk, "warning level and not breached")
	assert.EqualValues(t, 1, s.SLABreached)
}

// The number of queries must not grow with the number of agents.
func TestAgentAnalytics_QueryCountDoesNotGrowWithAgents(t *testing.T) {
	e := newMetricsEnv(t)
	now := time.Now()

	var queries atomic.Int64
	count := func(*gorm.DB) { queries.Add(1) }
	require.NoError(t, e.app.DB.Callback().Query().Before("gorm:query").Register("count_queries_q", count))
	require.NoError(t, e.app.DB.Callback().Row().Before("gorm:row").Register("count_queries_r", count))
	t.Cleanup(func() {
		_ = e.app.DB.Callback().Query().Remove("count_queries_q")
		_ = e.app.DB.Callback().Row().Remove("count_queries_r")
	})

	addAgent := func() {
		a := e.agent(t)
		_, c := e.transfer(t, &a.ID, models.TransferStatusActive, now.Add(-time.Hour), nil)
		e.message(t, c.ID, models.DirectionIncoming, now.Add(-time.Minute), nil)
		e.message(t, c.ID, models.DirectionOutgoing, now.Add(-30*time.Minute), &a.ID)
	}
	measure := func() int64 {
		queries.Store(0)
		e.analytics(t)
		return queries.Load()
	}

	addAgent()
	addAgent()
	e.analytics(t) // warm the permission cache so only the metric queries are compared
	few := measure()
	for range 8 {
		addAgent()
	}
	many := measure()

	assert.Equal(t, few, many, "queries must not depend on the number of agents (2 agents: %d, 10 agents: %d)", few, many)
}
