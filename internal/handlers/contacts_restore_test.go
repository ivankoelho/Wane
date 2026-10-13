package handlers_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// deletedContact is a soft-deleted contact carrying data worth protecting.
func (e *claimEnv) deletedContact(t *testing.T, owner *uuid.UUID) *models.Contact {
	t.Helper()
	c := e.contact(t)
	require.NoError(t, e.app.DB.Model(c).Updates(map[string]any{
		"profile_name":     "Cliente Original",
		"cpfcnpj":          "03713829551",
		"tags":             models.JSONBArray{"vip"},
		"metadata":         models.JSONB{"loja": "serrinha"},
		"assigned_user_id": owner,
	}).Error)
	require.NoError(t, e.app.DB.Delete(c).Error)
	return c
}

// untouched asserts the soft-deleted contact is exactly as it was left.
func (e *claimEnv) untouched(t *testing.T, id uuid.UUID, owner *uuid.UUID) {
	t.Helper()
	var c models.Contact
	require.NoError(t, e.app.DB.Unscoped().First(&c, "id = ?", id).Error)
	assert.True(t, c.DeletedAt.Valid, "must stay deleted")
	assert.Equal(t, "Cliente Original", c.ProfileName)
	assert.Equal(t, "03713829551", c.CPFCNPJ)
	assert.Equal(t, models.JSONBArray{"vip"}, c.Tags)
	assert.Equal(t, "serrinha", c.Metadata["loja"])
	if owner == nil {
		assert.Nil(t, c.AssignedUserID)
	} else {
		require.NotNil(t, c.AssignedUserID)
		assert.Equal(t, *owner, *c.AssignedUserID)
	}
}

// Someone else's deleted contact: refused, and not one field changes, even
// when the caller sends a full set of replacement data and claim=true.
func TestCreateContact_RestoreOfAnotherUsersContactChangesNothing(t *testing.T) {
	e := newClaimEnv(t)
	owner := e.agent(t)
	c := e.deletedContact(t, &owner.ID)
	other := e.agent(t)

	body := map[string]any{
		"phone_number": claimPhone, "claim": true, "profile_name": "Sobrescrito",
		"cpf_cnpj": "529.982.247-25", "tags": []string{"x"}, "metadata": map[string]any{"loja": "outra"},
	}
	reply := e.createWith(t, other.ID, body)

	assert.Equal(t, fasthttp.StatusConflict, reply.status)
	assert.Equal(t, "contact_exists_assigned", reply.data["code"])
	assert.Equal(t, owner.FullName, reply.data["assigned_user_name"])
	assert.NotContains(t, reply.data, "cpf_cnpj", "no contact details beyond the name")
	e.untouched(t, c.ID, &owner.ID)
}

func TestCreateContact_RestoreWhileAnotherAgentHoldsTheAttendanceChangesNothing(t *testing.T) {
	e := newClaimEnv(t)
	holder := e.agent(t)
	c := e.deletedContact(t, nil)
	activeTransfer(t, e.app, e.org.ID, c.ID, &holder.ID, nil)

	reply := e.createWith(t, e.agent(t).ID, map[string]any{"phone_number": claimPhone, "claim": true, "profile_name": "Sobrescrito"})
	assert.Equal(t, fasthttp.StatusConflict, reply.status)
	e.untouched(t, c.ID, nil)
}

// An unowned deleted contact is not brought back by an unconfirmed request.
func TestCreateContact_RestoreNeedsConfirmation(t *testing.T) {
	e := newClaimEnv(t)
	c := e.deletedContact(t, nil)

	reply := e.createWith(t, e.agent(t).ID, map[string]any{"phone_number": claimPhone, "profile_name": "Sobrescrito"})
	assert.Equal(t, fasthttp.StatusConflict, reply.status)
	assert.Equal(t, "contact_exists_unassigned", reply.data["code"])
	assert.Equal(t, true, reply.data["deleted"])
	assert.Equal(t, true, reply.data["can_claim"])
	e.untouched(t, c.ID, nil)
}

// Not authorized to take it (team contact, caller outside the team): refused
// with a stable code before anything is un-deleted or overwritten, and the
// unconfirmed answer already says the agent cannot take it.
func TestCreateContact_RestoreRefusedWithoutAuthorizationChangesNothing(t *testing.T) {
	e := newClaimEnv(t)
	c := e.deletedContact(t, nil)
	member := e.agent(t)
	team := e.team(t, member.ID)
	require.NoError(t, e.app.DB.Unscoped().Model(&models.Contact{}).Where("id = ?", c.ID).Update("team_id", team.ID).Error)
	outsider := e.agent(t)

	reply := e.createWith(t, outsider.ID, map[string]any{"phone_number": claimPhone})
	assert.Equal(t, fasthttp.StatusConflict, reply.status)
	assert.Equal(t, false, reply.data["can_claim"])
	assert.Equal(t, "not_team_member", reply.data["claim_blocked_reason"])

	reply = e.createWith(t, outsider.ID, map[string]any{"phone_number": claimPhone, "claim": true, "profile_name": "Sobrescrito", "cpf_cnpj": "529.982.247-25"})
	assert.Equal(t, fasthttp.StatusForbidden, reply.status)
	assert.Equal(t, "not_team_member", reply.data["code"])
	e.untouched(t, c.ID, nil)
}

// The authorized path still works: confirmed, allowed, restored and owned.
func TestCreateContact_RestoreWithConfirmedClaimRestoresAndAssigns(t *testing.T) {
	e := newClaimEnv(t)
	c := e.deletedContact(t, nil)
	agent := e.agent(t)

	reply := e.createWith(t, agent.ID, map[string]any{"phone_number": claimPhone, "claim": true})
	require.Equal(t, fasthttp.StatusOK, reply.status)

	var got models.Contact
	require.NoError(t, e.app.DB.First(&got, "id = ?", c.ID).Error)
	require.NotNil(t, got.AssignedUserID)
	assert.Equal(t, agent.ID, *got.AssignedUserID)
	assert.Equal(t, "Cliente Original", got.ProfileName, "fields not sent are kept")
}

// Bringing back your own deleted contact needs no claim.
func TestCreateContact_RestoreOfYourOwnDeletedContact(t *testing.T) {
	e := newClaimEnv(t)
	agent := e.agent(t)
	c := e.deletedContact(t, &agent.ID)

	reply := e.createWith(t, agent.ID, map[string]any{"phone_number": claimPhone})
	require.Equal(t, fasthttp.StatusOK, reply.status)
	var got models.Contact
	require.NoError(t, e.app.DB.First(&got, "id = ?", c.ID).Error)
	assert.Equal(t, agent.ID, *got.AssignedUserID)
}

// The pre-flight answer for a live contact the agent cannot take carries the
// reason, so the dialog can explain instead of offering a doomed button.
func TestCreateContact_UnassignedAnswerReportsWhyItCannotBeTaken(t *testing.T) {
	e := newClaimEnv(t)
	c := e.contact(t)
	activeTransfer(t, e.app, e.org.ID, c.ID, nil, nil)

	noPickup := e.user(t, "contacts:read", "contacts:write", "chat:read")
	reply := e.createWith(t, noPickup.ID, map[string]any{"phone_number": claimPhone})
	assert.Equal(t, fasthttp.StatusConflict, reply.status)
	assert.Equal(t, false, reply.data["can_claim"])
	assert.Equal(t, "pickup_permission_required", reply.data["claim_blocked_reason"])

	reply = e.createWith(t, e.agent(t).ID, map[string]any{"phone_number": claimPhone})
	assert.Equal(t, true, reply.data["can_claim"])
}
