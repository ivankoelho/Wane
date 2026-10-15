package handlers

import (
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
)

// calculateStatsFor computes the performance stats of any number of agents with
// a fixed number of grouped queries (at most 7), so the cost does not grow with
// the number of agents. It is the only place these metrics are computed; the
// single-agent and all-agents paths both go through it.
//
// Metric semantics:
//   - TransfersHandled: attendances closed (status resumed) by the agent, in the
//     period. The closer is resumed_by; rows without it fall back to the owner.
//     Auto-closed (expired) attendances are not "handled".
//   - AvgFirstResponseMins: transferred_at -> first_response_at, over the
//     attendances the agent owns in the period that already have a response.
//   - MessagesSent: outgoing messages sent by the agent (sent_by_user_id) in the
//     period, regardless of whether the contact was ever transferred.
//   - ActiveTransfers and its split: attendances the agent owns right now. The
//     split looks at the last message since the attendance started: incoming =
//     the customer waits for the agent, outgoing by a person = the agent waits
//     for the customer. An attendance with no message yet is in neither.
func (a *App) calculateStatsFor(orgID uuid.UUID, agentIDs []uuid.UUID, start, end time.Time) map[uuid.UUID]*AgentPerformanceStats {
	out := make(map[uuid.UUID]*AgentPerformanceStats, len(agentIDs))
	if len(agentIDs) == 0 {
		return out
	}
	for _, id := range agentIDs {
		out[id] = &AgentPerformanceStats{AgentID: id.String()}
	}

	// 1. Identity and current availability.
	var users []models.User
	if err := a.DB.Select("id", "full_name", "is_available").Where("id IN ?", agentIDs).Find(&users).Error; err != nil {
		a.Log.Error("Failed to load agents for analytics", "error", err, "org_id", orgID)
	}
	var unavailable []uuid.UUID
	for _, u := range users {
		if s, ok := out[u.ID]; ok {
			s.AgentName = u.FullName
			s.IsAvailable = u.IsAvailable
			if !u.IsAvailable {
				unavailable = append(unavailable, u.ID)
			}
		}
	}

	// 2. Attendances closed by each agent, and their resolution time.
	var handled []struct {
		AgentID       uuid.UUID
		Count         int64
		AvgResolution *float64
	}
	if err := a.DB.Model(&models.AgentTransfer{}).
		Select("COALESCE(resumed_by, agent_id) AS agent_id, COUNT(*) AS count, "+
			"AVG(EXTRACT(EPOCH FROM (resumed_at - transferred_at))/60) FILTER (WHERE resumed_at IS NOT NULL) AS avg_resolution").
		Where("organization_id = ? AND status = ? AND transferred_at >= ? AND transferred_at <= ? AND COALESCE(resumed_by, agent_id) IN ?",
			orgID, models.TransferStatusResumed, start, end, agentIDs).
		Group("COALESCE(resumed_by, agent_id)").
		Scan(&handled).Error; err != nil {
		a.Log.Error("Failed to compute handled transfers", "error", err, "org_id", orgID)
	}
	for _, h := range handled {
		if s, ok := out[h.AgentID]; ok {
			s.TransfersHandled = h.Count
			if h.AvgResolution != nil {
				s.AvgResolutionMins = *h.AvgResolution
			}
		}
	}

	// 3. First response time of the attendances each agent owns.
	var first []struct {
		AgentID uuid.UUID
		Avg     *float64
	}
	if err := a.DB.Model(&models.AgentTransfer{}).
		Select("agent_id, AVG(EXTRACT(EPOCH FROM (first_response_at - transferred_at))/60) AS avg").
		Where("organization_id = ? AND agent_id IN ? AND first_response_at IS NOT NULL AND transferred_at >= ? AND transferred_at <= ?",
			orgID, agentIDs, start, end).
		Group("agent_id").
		Scan(&first).Error; err != nil {
		a.Log.Error("Failed to compute first response time", "error", err, "org_id", orgID)
	}
	for _, f := range first {
		if s, ok := out[f.AgentID]; ok && f.Avg != nil {
			s.AvgFirstResponseMins = *f.Avg
		}
	}

	// 4. Current load and what each attendance is waiting for.
	var active []struct {
		AgentID          uuid.UUID
		Active           int64
		AwaitingAgent    int64
		AwaitingCustomer int64
		SLAAtRisk        int64 `gorm:"column:sla_at_risk"`
		SLABreached      int64 `gorm:"column:sla_breached"`
	}
	if err := a.DB.Raw(`
SELECT t.agent_id,
       COUNT(*) AS active,
       COUNT(*) FILTER (WHERE lm.direction = ?) AS awaiting_agent,
       COUNT(*) FILTER (WHERE lm.direction = ? AND lm.sent_by_user_id IS NOT NULL) AS awaiting_customer,
       COUNT(*) FILTER (WHERE NOT t.sla_breached AND t.escalation_level >= 1) AS sla_at_risk,
       COUNT(*) FILTER (WHERE t.sla_breached) AS sla_breached
FROM agent_transfers t
LEFT JOIN LATERAL (
    SELECT m.direction, m.sent_by_user_id
    FROM messages m
    WHERE m.organization_id = t.organization_id AND m.contact_id = t.contact_id
      AND m.created_at >= t.transferred_at AND m.deleted_at IS NULL
    ORDER BY m.created_at DESC
    LIMIT 1
) lm ON true
WHERE t.organization_id = ? AND t.status = ? AND t.agent_id IN ? AND t.deleted_at IS NULL
GROUP BY t.agent_id`,
		models.DirectionIncoming, models.DirectionOutgoing, orgID, models.TransferStatusActive, agentIDs).
		Scan(&active).Error; err != nil {
		a.Log.Error("Failed to compute active transfers", "error", err, "org_id", orgID)
	}
	for _, r := range active {
		if s, ok := out[r.AgentID]; ok {
			s.ActiveTransfers = r.Active
			s.AwaitingAgent = r.AwaitingAgent
			s.AwaitingCustomer = r.AwaitingCustomer
			s.SLAAtRisk = r.SLAAtRisk
			s.SLABreached = r.SLABreached
		}
	}

	// 5. Messages each agent actually sent.
	var sent []struct {
		AgentID uuid.UUID
		Count   int64
	}
	if err := a.DB.Model(&models.Message{}).
		Select("sent_by_user_id AS agent_id, COUNT(*) AS count").
		Where("organization_id = ? AND direction = ? AND sent_by_user_id IN ? AND created_at >= ? AND created_at <= ?",
			orgID, models.DirectionOutgoing, agentIDs, start, end).
		Group("sent_by_user_id").
		Scan(&sent).Error; err != nil {
		a.Log.Error("Failed to compute messages sent", "error", err, "org_id", orgID)
	}
	for _, m := range sent {
		if s, ok := out[m.AgentID]; ok {
			s.MessagesSent = m.Count
		}
	}

	// 6. Away time in the period.
	for id, bt := range a.calculateBreakTimes(agentIDs, start, end) {
		if s, ok := out[id]; ok {
			s.TotalBreakTimeMins = bt.mins
			s.BreakCount = bt.count
		}
	}

	// 7. When the current away period started, for agents away right now.
	if len(unavailable) > 0 {
		var open []models.UserAvailabilityLog
		if err := a.DB.Where("user_id IN ? AND is_available = false AND ended_at IS NULL", unavailable).
			Order("started_at DESC").Find(&open).Error; err != nil {
			a.Log.Error("Failed to load open away periods", "error", err, "org_id", orgID)
		}
		for _, l := range open {
			if s, ok := out[l.UserID]; ok && s.CurrentBreakStart == nil {
				v := l.StartedAt.Format(time.RFC3339)
				s.CurrentBreakStart = &v
			}
		}
	}

	return out
}

func (a *App) calculateAgentStats(orgID, agentID uuid.UUID, start, end time.Time) AgentPerformanceStats {
	return *a.calculateStatsFor(orgID, []uuid.UUID{agentID}, start, end)[agentID]
}

func (a *App) calculateAllAgentStats(orgID uuid.UUID, start, end time.Time) []AgentPerformanceStats {
	// Get all agents in the organization through team membership
	var agents []models.User
	if err := a.DB.
		Joins("JOIN team_members ON team_members.user_id = users.id").
		Joins("JOIN teams ON teams.id = team_members.team_id").
		Where("users.organization_id = ? AND team_members.role = ?", orgID, models.TeamRoleAgent).
		Distinct().
		Find(&agents).Error; err != nil {
		a.Log.Error("Failed to fetch agents for analytics", "error", err, "org_id", orgID)
		return []AgentPerformanceStats{}
	}

	ids := make([]uuid.UUID, 0, len(agents))
	for _, agent := range agents {
		ids = append(ids, agent.ID)
	}
	byAgent := a.calculateStatsFor(orgID, ids, start, end)

	stats := make([]AgentPerformanceStats, 0, len(agents))
	for _, id := range ids {
		stats = append(stats, *byAgent[id])
	}
	return stats
}

type breakTotals struct {
	mins  float64
	count int64
}

// calculateBreakTimes returns, per agent, the away time and number of away
// periods that overlap [start, end], from one query for all agents.
func (a *App) calculateBreakTimes(agentIDs []uuid.UUID, start, end time.Time) map[uuid.UUID]breakTotals {
	totals := make(map[uuid.UUID]breakTotals, len(agentIDs))
	if len(agentIDs) == 0 {
		return totals
	}

	// "Away" periods that overlap with the time range.
	var logs []models.UserAvailabilityLog
	if err := a.DB.Where("user_id IN ? AND is_available = false AND started_at <= ? AND (ended_at >= ? OR ended_at IS NULL)",
		agentIDs, end, start).
		Find(&logs).Error; err != nil {
		a.Log.Error("Failed to fetch availability logs for break time calculation", "error", err)
		return totals
	}

	for _, log := range logs {
		logStart := log.StartedAt
		if logStart.Before(start) {
			logStart = start
		}

		var logEnd time.Time
		if log.EndedAt != nil {
			logEnd = *log.EndedAt
		} else {
			// Still away: count up to now, capped at the end of the period.
			logEnd = time.Now()
		}
		if logEnd.After(end) {
			logEnd = end
		}

		if logEnd.After(logStart) {
			t := totals[log.UserID]
			t.mins += logEnd.Sub(logStart).Minutes()
			t.count++
			totals[log.UserID] = t
		}
	}
	return totals
}

// calculateBreakTime is the single-agent form of calculateBreakTimes.
func (a *App) calculateBreakTime(agentID uuid.UUID, start, end time.Time) (totalMins float64, count int64) {
	t := a.calculateBreakTimes([]uuid.UUID{agentID}, start, end)[agentID]
	return t.mins, t.count
}

// calculateAgentSummaryStats fills the summary for one agent from the same
// computation as the per-agent stats, so the two can never disagree.
func (a *App) calculateAgentSummaryStats(orgID, agentID uuid.UUID, start, end time.Time, summary *AgentAnalyticsSummary) {
	stats := a.calculateAgentStats(orgID, agentID, start, end)
	summary.TotalTransfersHandled = stats.TransfersHandled
	summary.ActiveTransfers = stats.ActiveTransfers
	summary.AvgResolutionMins = stats.AvgResolutionMins
	summary.AvgFirstResponseMins = stats.AvgFirstResponseMins
	summary.TotalBreakTimeMins = stats.TotalBreakTimeMins
	summary.BreakCount = stats.BreakCount

	// Queue time of the attendances this agent first picked up (first pickup
	// only, see calculateSummaryStats).
	var queue struct{ Avg float64 }
	a.DB.Model(&models.AgentTransfer{}).
		Select("COALESCE(AVG(EXTRACT(EPOCH FROM (picked_up_at - transferred_at))/60), 0) as avg").
		Where("organization_id = ? AND agent_id = ? AND picked_up_at IS NOT NULL AND transferred_at >= ? AND transferred_at <= ?",
			orgID, agentID, start, end).
		Scan(&queue)
	summary.AvgQueueTimeMins = queue.Avg

	var sourceCounts []struct {
		Source string
		Count  int64
	}
	a.DB.Model(&models.AgentTransfer{}).
		Select("source, COUNT(*) as count").
		Where("organization_id = ? AND agent_id = ? AND transferred_at >= ? AND transferred_at <= ?", orgID, agentID, start, end).
		Group("source").
		Scan(&sourceCounts)
	for _, sc := range sourceCounts {
		summary.TransfersBySource[sc.Source] = sc.Count
	}
}
