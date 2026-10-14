package handlers_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// audit run-1: identity:sso-callback-global-email-login-cross-org — org B's
// identity provider must not log anyone into an account of org A.
func TestSSOScope_ProviderCannotLogInUserOfAnotherOrg(t *testing.T) {
	app := newSSOApp(t)
	fake := newFakeOAuth(t)
	victimOrg := testutil.CreateTestOrganization(t, app.DB)
	victim := testutil.CreateTestUser(t, app.DB, victimOrg.ID, testutil.WithSuperAdmin())
	fake.UserEmail = victim.Email

	attackerOrg := testutil.CreateTestOrganization(t, app.DB)
	createCustomSSOProvider(t, app, attackerOrg.ID, fake, func(p *models.SSOProvider) {
		p.AllowAutoCreate = false
	})
	// Public provider listing is global: don't leak this provider into other tests.
	t.Cleanup(func() { app.DB.Where("organization_id = ?", attackerOrg.ID).Delete(&models.SSOProvider{}) })

	nonce := "cross-org-" + uuid.NewString()
	stored, _ := json.Marshal(handlers.SSOState{
		OrgID: attackerOrg.ID.String(), Provider: "custom", Nonce: nonce, ExpiresAt: time.Now().Add(5 * time.Minute),
	})
	require.NoError(t, app.Redis.Set(context.Background(), "sso:state:"+nonce, stored, 5*time.Minute).Err())

	req := testutil.NewGETRequest(t)
	testutil.SetPathParam(req, "provider", "custom")
	testutil.SetQueryParam(req, "code", "c")
	testutil.SetQueryParam(req, "state", nonce)
	require.NoError(t, app.CallbackSSO(req))

	assert.Empty(t, testutil.GetResponseCookie(req, "whm_access"), "no session may be issued for another org's user")
}

// audit run-1: identity:apikey-org-override-via-x-organization-id and
// identity:apikey-exchangeable-for-jwt-session.
func TestAPIKeyScope(t *testing.T) {
	app := newTestApp(t)
	orgA := testutil.CreateTestOrganization(t, app.DB)
	orgB := testutil.CreateTestOrganization(t, app.DB)
	user := testutil.CreateTestUser(t, app.DB, orgA.ID, testutil.WithSuperAdmin())
	require.NoError(t, app.DB.Create(&models.UserOrganization{UserID: user.ID, OrganizationID: orgB.ID}).Error)
	require.NoError(t, app.DB.Create(&models.Tag{OrganizationID: orgB.ID, Name: "org-b-only-tag"}).Error)

	apiKeyRequest := func() *fastglue.Request {
		req := testutil.NewJSONRequest(t, map[string]any{"organization_id": orgB.ID.String()})
		testutil.SetAuthContext(req, orgA.ID, user.ID)
		req.RequestCtx.SetUserValue("api_key_id", uuid.New())
		return req
	}

	t.Run("header cannot move a key to another org", func(t *testing.T) {
		req := apiKeyRequest()
		req.RequestCtx.Request.Header.Set("X-Organization-ID", orgB.ID.String())
		require.NoError(t, app.ListTags(req))
		require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))
		assert.NotContains(t, string(testutil.GetResponseBody(req)), "org-b-only-tag")
	})

	t.Run("key cannot be exchanged for a session", func(t *testing.T) {
		req := apiKeyRequest()
		require.NoError(t, app.SwitchOrg(req))
		require.Equal(t, fasthttp.StatusForbidden, testutil.GetResponseStatusCode(req))

		req = apiKeyRequest()
		require.NoError(t, app.GetWSToken(req))
		require.Equal(t, fasthttp.StatusForbidden, testutil.GetResponseStatusCode(req))
	})
}
