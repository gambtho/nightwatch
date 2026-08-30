package httpapi_test

import (
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/httpapi"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	return key
}

func TestSessionRoundTrip(t *testing.T) {
	key := testKey(t)
	claims := httpapi.SessionClaims{UserID: uuid.New(), TenantID: uuid.New(), Role: "owner"}
	val, err := httpapi.SignSession(claims, key, time.Hour)
	require.NoError(t, err)

	got, err := httpapi.VerifySession(val, key)
	require.NoError(t, err)
	require.Equal(t, claims.UserID, got.UserID)
	require.Equal(t, claims.TenantID, got.TenantID)
}

func TestSessionRejectsTamperAndExpiry(t *testing.T) {
	key := testKey(t)
	claims := httpapi.SessionClaims{UserID: uuid.New(), TenantID: uuid.New(), Role: "owner"}

	val, err := httpapi.SignSession(claims, key, time.Hour)
	require.NoError(t, err)
	_, err = httpapi.VerifySession(val+"x", key)
	require.Error(t, err)

	expired, err := httpapi.SignSession(claims, key, -time.Minute)
	require.NoError(t, err)
	_, err = httpapi.VerifySession(expired, key)
	require.Error(t, err)
}

func TestRequireSession(t *testing.T) {
	key := testKey(t)
	var seen httpapi.SessionClaims
	h := httpapi.RequireSession(key, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = httpapi.ClaimsFrom(r.Context())
	}))

	// No cookie: 401.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/workflows", nil))
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// Valid cookie: passes and exposes claims.
	claims := httpapi.SessionClaims{UserID: uuid.New(), TenantID: uuid.New(), Role: "owner"}
	cookie, err := httpapi.SessionCookie(key, claims, time.Hour)
	require.NoError(t, err)
	req := httptest.NewRequest("GET", "/v1/workflows", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, claims.TenantID, seen.TenantID)
}
