package handlers_test

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/middleware"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

func signTestJWT(t *testing.T, userID, orgID uuid.UUID, jti, subject string) string {
	t.Helper()
	claims := middleware.JWTClaims{
		UserID:         userID,
		OrganizationID: orgID,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti,
			Subject:   subject,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testutil.TestJWTSecret))
	require.NoError(t, err)
	return s
}

// audit run-1: identity:jwt-token-type-confusion-shared-claims
func TestTokenTypes_AccessTokenCannotMintSession(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	user := testutil.CreateTestUser(t, app.DB, org.ID)

	access := signTestJWT(t, user.ID, org.ID, "", "") // access tokens carry no jti
	req := testutil.NewJSONRequest(t, map[string]any{"refresh_token": access})
	require.NoError(t, app.RefreshToken(req))
	require.Equal(t, fasthttp.StatusUnauthorized, testutil.GetResponseStatusCode(req))
}

func TestTokenTypes_RefreshAndWSTokensAreNotBearerTokens(t *testing.T) {
	auth := middleware.AuthWithDB(testutil.TestJWTSecret, nil)
	uid, oid := uuid.New(), uuid.New()
	for name, tok := range map[string]string{
		"refresh": signTestJWT(t, uid, oid, uuid.NewString(), ""),
		"ws":      signTestJWT(t, uid, oid, "", "ws"),
	} {
		req := testutil.NewGETRequest(t)
		req.RequestCtx.Request.Header.Set("Authorization", "Bearer "+tok)
		require.Nil(t, auth(req), "%s token must not authenticate an API request", name)
	}
	// An access token still works.
	req := testutil.NewGETRequest(t)
	req.RequestCtx.Request.Header.Set("Authorization", "Bearer "+signTestJWT(t, uid, oid, "", ""))
	require.NotNil(t, auth(req))
}

// audit run-1: identity:logout-does-not-revoke-refresh-jti — the refresh
// cookie must be sent to /api/auth/logout, or logout can never revoke it.
func TestTokenTypes_RefreshCookieReachesLogout(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	user := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithPassword("password123"))

	req := testutil.NewJSONRequest(t, map[string]any{"email": user.Email, "password": "password123"})
	require.NoError(t, app.Login(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var path string
	req.RequestCtx.Response.Header.VisitAllCookie(func(key, value []byte) {
		c := fasthttp.AcquireCookie()
		defer fasthttp.ReleaseCookie(c)
		_ = c.ParseBytes(value)
		if string(key) == "whm_refresh" && len(c.Value()) > 0 {
			path = string(c.Path())
		}
	})
	require.NotEmpty(t, path)
	require.True(t, strings.HasPrefix("/api/auth/logout", path), "refresh cookie path %q does not cover /api/auth/logout", path)
}
