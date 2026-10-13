package handlers_test

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

const claimPhone = "5511955554444"

type claimEnv struct {
	app *handlers.App
	org *models.Organization
}

func newClaimEnv(t *testing.T) *claimEnv {
	t.Helper()
	app := newTestApp(t)
	return &claimEnv{app: app, org: testutil.CreateTestOrganization(t, app.DB)}
}

// user creates a member of the env's organization holding exactly the keys.
func (e *claimEnv) user(t *testing.T, keys ...string) *models.User {
	t.Helper()
	role := testutil.CreateTestRoleWithKeys(t, e.app.DB, e.org.ID, "claim-"+uuid.NewString()[:8], keys)
	return testutil.CreateTestUser(t, e.app.DB, e.org.ID, testutil.WithRoleID(&role.ID))
}

// agent holds what a custom attendant role needs to add contacts and pick up.
func (e *claimEnv) agent(t *testing.T) *models.User {
	return e.user(t, "contacts:read", "contacts:write", "chat:read", "chat:write", "transfers:read", "transfers:pickup")
}

func (e *claimEnv) contact(t *testing.T) *models.Contact {
	t.Helper()
	return testutil.CreateTestContactWith(t, e.app.DB, e.org.ID, testutil.WithPhoneNumber(claimPhone))
}

type claimReply struct {
	status int
	data   map[string]any
}

// request builds the call now and returns a func that runs it, so tests can
// fire several at once from goroutines without touching *testing.T there.
func (e *claimEnv) request(t *testing.T, userID uuid.UUID, claim bool) func() claimReply {
	t.Helper()
	body := map[string]any{"phone_number": claimPhone}
	if claim {
		body["claim"] = true
	}
	req := testutil.NewJSONRequest(t, body)
	testutil.SetAuthContext(req, e.org.ID, userID)
	return func() claimReply {
		_ = e.app.CreateContact(req)
		return claimReply{status: testutil.GetResponseStatusCode(req), data: errorData(req)}
	}
}

func (e *claimEnv) create(t *testing.T, userID uuid.UUID, claim bool) claimReply {
	return e.request(t, userID, claim)()
}

func (e *claimEnv) team(t *testing.T, members ...uuid.UUID) *models.Team {
	t.Helper()
	team := &models.Team{BaseModel: models.BaseModel{ID: uuid.New()}, OrganizationID: e.org.ID, Name: "Claim " + uuid.NewString()[:6], IsActive: true}
	require.NoError(t, e.app.DB.Create(team).Error)
	for _, m := range members {
		require.NoError(t, e.app.DB.Create(&models.TeamMember{BaseModel: models.BaseModel{ID: uuid.New()}, TeamID: team.ID, UserID: m, Role: models.TeamRoleAgent}).Error)
	}
	return team
}

func (e *claimEnv) transferOf(t *testing.T, contactID uuid.UUID) models.AgentTransfer {
	t.Helper()
	var tr models.AgentTransfer
	require.NoError(t, e.app.DB.Where("contact_id = ?", contactID).First(&tr).Error)
	return tr
}

func (e *claimEnv) carteira(t *testing.T, contactID uuid.UUID) *uuid.UUID {
	t.Helper()
	var c models.Contact
	require.NoError(t, e.app.DB.First(&c, "id = ?", contactID).Error)
	return c.AssignedUserID
}

// claim=true is a request, not an authorization: without contacts:write the
// call is refused and the contact is untouched.
func TestCreateContact_ClaimNeedsContactsWrite(t *testing.T) {
	e := newClaimEnv(t)
	c := e.contact(t)
	reader := e.user(t, "contacts:read", "chat:read")

	reply := e.create(t, reader.ID, true)
	assert.Equal(t, fasthttp.StatusForbidden, reply.status)
	assert.Nil(t, e.carteira(t, c.ID))
}

// A contact of another organization with the same number is invisible: the
// claim never reaches it and a fresh contact is created in the caller's org.
func TestCreateContact_ClaimDoesNotCrossOrganizations(t *testing.T) {
	e := newClaimEnv(t)
	otherOrg := testutil.CreateTestOrganization(t, e.app.DB)
	foreign := testutil.CreateTestContactWith(t, e.app.DB, otherOrg.ID, testutil.WithPhoneNumber(claimPhone))
	agent := e.agent(t)

	reply := e.create(t, agent.ID, true)
	assert.Equal(t, fasthttp.StatusOK, reply.status)
	assert.Nil(t, e.carteira(t, foreign.ID), "the other organization's contact must not change")

	var n int64
	e.app.DB.Model(&models.Contact{}).Where("organization_id = ? AND phone_number = ?", e.org.ID, claimPhone).Count(&n)
	assert.Equal(t, int64(1), n)
}

func TestCreateContact_AlreadyMineReportsAssignedToMe(t *testing.T) {
	e := newClaimEnv(t)
	c := e.contact(t)
	agent := e.agent(t)
	require.NoError(t, e.app.DB.Model(c).Update("assigned_user_id", agent.ID).Error)

	reply := e.create(t, agent.ID, true)
	assert.Equal(t, fasthttp.StatusConflict, reply.status)
	assert.Equal(t, "contact_exists_assigned", reply.data["code"])
	assert.Equal(t, true, reply.data["assigned_to_me"])
}

// The queue is an attendance, so the claim goes through the same atomic
// pickup as every other path and the agent can then see the conversation.
func TestCreateContact_ClaimTakesTheQueueAttendanceAndMakesItVisible(t *testing.T) {
	e := newClaimEnv(t)
	enableStrictVisibility(t, e.app, e.org.ID)
	c := e.contact(t)
	agent := e.agent(t)
	team := e.team(t, agent.ID)
	activeTransfer(t, e.app, e.org.ID, c.ID, nil, &team.ID)

	reply := e.create(t, agent.ID, true)
	require.Equal(t, fasthttp.StatusOK, reply.status)

	tr := e.transferOf(t, c.ID)
	require.NotNil(t, tr.AgentID, "the attendance itself must be taken, not only the carteira")
	assert.Equal(t, agent.ID, *tr.AgentID)

	var reloaded models.Contact
	require.NoError(t, e.app.DB.First(&reloaded, "id = ?", c.ID).Error)
	assert.True(t, e.app.CanViewConversationForTest(agent.ID, e.org.ID, &reloaded))
	assert.True(t, e.app.CanInteractWithConversationForTest(agent.ID, e.org.ID, &reloaded))
}

func TestCreateContact_ClaimRespectsTeamQueueMembership(t *testing.T) {
	e := newClaimEnv(t)
	c := e.contact(t)
	outsider := e.agent(t)
	insider := e.agent(t)
	team := e.team(t, insider.ID)
	activeTransfer(t, e.app, e.org.ID, c.ID, nil, &team.ID)

	reply := e.create(t, outsider.ID, true)
	assert.Equal(t, fasthttp.StatusForbidden, reply.status)
	assert.Nil(t, e.transferOf(t, c.ID).AgentID, "refused claim must leave the queue untouched")

	reply = e.create(t, insider.ID, true)
	assert.Equal(t, fasthttp.StatusOK, reply.status)
}

func TestCreateContact_ClaimRespectsQueuePickupRules(t *testing.T) {
	e := newClaimEnv(t)
	c := e.contact(t)
	activeTransfer(t, e.app, e.org.ID, c.ID, nil, nil)

	noPickup := e.user(t, "contacts:read", "contacts:write", "chat:read")
	assert.Equal(t, fasthttp.StatusForbidden, e.create(t, noPickup.ID, true).status, "needs transfers:pickup")

	settings := &models.ChatbotSettings{OrganizationID: e.org.ID}
	require.NoError(t, e.app.DB.Where("organization_id = ? AND whats_app_account = ?", e.org.ID, "").FirstOrCreate(settings).Error)
	require.NoError(t, e.app.DB.Model(&models.ChatbotSettings{}).Where("id = ?", settings.ID).Update("allow_agent_queue_pickup", false).Error)
	e.app.InvalidateChatbotSettingsCache(e.org.ID)

	agent := e.agent(t)
	assert.Equal(t, fasthttp.StatusForbidden, e.create(t, agent.ID, true).status, "queue pickup disabled for the org")
	assert.Nil(t, e.transferOf(t, c.ID).AgentID)
}

// Held through an attendance (carteira empty) still counts as owned, and the
// agent is told by whom.
func TestCreateContact_AttendanceOwnedByAnotherAgentNamesThem(t *testing.T) {
	e := newClaimEnv(t)
	c := e.contact(t)
	owner := e.agent(t)
	activeTransfer(t, e.app, e.org.ID, c.ID, &owner.ID, nil)

	reply := e.create(t, e.agent(t).ID, true)
	assert.Equal(t, fasthttp.StatusConflict, reply.status)
	assert.Equal(t, "contact_exists_assigned", reply.data["code"])
	assert.Equal(t, owner.FullName, reply.data["assigned_user_name"])
	assert.Equal(t, owner.ID, *e.transferOf(t, c.ID).AgentID, "claim must not take another agent's attendance")
}

func TestCreateContact_ClaimOfTeamContactNeedsMembership(t *testing.T) {
	e := newClaimEnv(t)
	c := e.contact(t)
	member := e.agent(t)
	team := e.team(t, member.ID)
	require.NoError(t, e.app.DB.Model(c).Update("team_id", team.ID).Error)

	assert.Equal(t, fasthttp.StatusForbidden, e.create(t, e.agent(t).ID, true).status)
	assert.Equal(t, fasthttp.StatusOK, e.create(t, member.ID, true).status)
	assert.Equal(t, member.ID, *e.carteira(t, c.ID))
}

// Two agents confirm at once: exactly one wins, the other is told it is taken,
// and the stored owner is the winner's.
func TestCreateContact_ConcurrentClaimHasOneWinner(t *testing.T) {
	for name, withQueue := range map[string]bool{"carteira": false, "queue attendance": true} {
		t.Run(name, func(t *testing.T) {
			e := newClaimEnv(t)
			c := e.contact(t)
			if withQueue {
				activeTransfer(t, e.app, e.org.ID, c.ID, nil, nil)
			}
			a, b := e.agent(t), e.agent(t)
			runA, runB := e.request(t, a.ID, true), e.request(t, b.ID, true)

			var wg sync.WaitGroup
			var ra, rb claimReply
			wg.Add(2)
			go func() { defer wg.Done(); ra = runA() }()
			go func() { defer wg.Done(); rb = runB() }()
			wg.Wait()

			assert.ElementsMatch(t, []int{fasthttp.StatusOK, fasthttp.StatusConflict}, []int{ra.status, rb.status})

			winner := a.ID
			if rb.status == fasthttp.StatusOK {
				winner = b.ID
			}
			if withQueue {
				require.NotNil(t, e.transferOf(t, c.ID).AgentID)
				assert.Equal(t, winner, *e.transferOf(t, c.ID).AgentID)
			} else {
				require.NotNil(t, e.carteira(t, c.ID))
				assert.Equal(t, winner, *e.carteira(t, c.ID))
			}
		})
	}
}

// errorData returns the data object of an error envelope, nil when absent.
func errorData(req *fastglue.Request) map[string]any {
	var env struct {
		Data map[string]any `json:"data"`
	}
	_ = json.Unmarshal(testutil.GetResponseBody(req), &env)
	return env.Data
}

// createWith posts an arbitrary body as userID.
func (e *claimEnv) createWith(t *testing.T, userID uuid.UUID, body map[string]any) claimReply {
	t.Helper()
	req := testutil.NewJSONRequest(t, body)
	testutil.SetAuthContext(req, e.org.ID, userID)
	_ = e.app.CreateContact(req)
	return claimReply{status: testutil.GetResponseStatusCode(req), data: errorData(req)}
}
