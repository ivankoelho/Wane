package handlers_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// runID keeps phone IDs unique across runs: the account cache lives in Redis.
var runID = uuid.NewString()[:8]

func createWebhookAccount(t *testing.T, app *handlers.App, phoneID, appSecret string) {
	t.Helper()
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, app.DB.Create(&models.WhatsAppAccount{
		BaseModel:          models.BaseModel{ID: uuid.New()},
		OrganizationID:     org.ID,
		Name:               "wbk-" + phoneID,
		PhoneID:            phoneID,
		BusinessID:         "biz-" + phoneID,
		AccessToken:        "tok",
		AppSecret:          appSecret,
		WebhookVerifyToken: "vt",
		APIVersion:         "v18.0",
		Status:             "active",
	}).Error)
}

func postWebhook(t *testing.T, app *handlers.App, body []byte, sig string) int {
	t.Helper()
	req := testutil.NewRequest(t)
	req.RequestCtx.Request.Header.SetMethod("POST")
	req.RequestCtx.Request.Header.SetContentType("application/json")
	if sig != "" {
		req.RequestCtx.Request.Header.Set("X-Hub-Signature-256", sig)
	}
	req.RequestCtx.Request.SetBody(body)
	require.NoError(t, app.WebhookHandler(req))
	return testutil.GetResponseStatusCode(req)
}

// audit run-1: webhook:unsigned-payload-accepted — an account without its own
// secret must not make any signature acceptable.
func TestWebhookFailClosed_SecretlessAccountRejectsAnySignature(t *testing.T) {
	app := newAppForWebhook(t, "")
	createWebhookAccount(t, app, "fc-phone-1-"+runID, "")
	body := makeMessagesPayload("fc-phone-1-"+runID)
	assert.Equal(t, fasthttp.StatusForbidden, postWebhook(t, app, body, signWebhook(body, "attacker-chosen")))
}

// The global Meta app secret is the fallback for accounts without their own.
func TestWebhookFailClosed_GlobalSecretFallbackAccepted(t *testing.T) {
	app := newAppForWebhook(t, "")
	app.Config.WhatsApp.AppSecret = "global-meta-app-secret"
	createWebhookAccount(t, app, "fc-phone-2-"+runID, "")
	body := makeMessagesPayload("fc-phone-2-"+runID)
	assert.Equal(t, fasthttp.StatusOK, postWebhook(t, app, body, signWebhook(body, "global-meta-app-secret")))
}

// audit run-1: webhook:signature-verifies-first-change-only — a payload signed
// for account A must not carry a change for account B.
func TestWebhookFailClosed_EveryChangeMustMatchItsAccountSecret(t *testing.T) {
	app := newAppForWebhook(t, "")
	createWebhookAccount(t, app, "fc-phone-a-"+runID, "secret-a")
	createWebhookAccount(t, app, "fc-phone-b-"+runID, "secret-b")
	change := func(phone string) map[string]any {
		return map[string]any{"field": "messages", "value": map[string]any{
			"messaging_product": "whatsapp",
			"metadata":          map[string]any{"phone_number_id": phone},
		}}
	}
	body, _ := json.Marshal(map[string]any{
		"object": "whatsapp_business_account",
		"entry": []map[string]any{{
			"id":      "WABA-X",
			"changes": []map[string]any{change("fc-phone-a-"+runID), change("fc-phone-b-"+runID)},
		}},
	})
	assert.Equal(t, fasthttp.StatusForbidden, postWebhook(t, app, body, signWebhook(body, "secret-a")))
}
