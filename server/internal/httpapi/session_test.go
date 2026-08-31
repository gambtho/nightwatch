package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/httpapi"
	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/testpg"
)

func TestSessionCookieContract(t *testing.T) {
	c := httpapi.SessionCookie("tok")
	require.Equal(t, "__Host-ns_session", c.Name)
	require.Equal(t, "tok", c.Value)
	require.True(t, c.Secure)
	require.True(t, c.HttpOnly)
	require.Equal(t, http.SameSiteLaxMode, c.SameSite)
	require.Equal(t, "/", c.Path)
	require.Empty(t, c.Domain, "__Host- forbids a Domain attribute")
	require.Positive(t, c.MaxAge)

	cleared := httpapi.ClearSessionCookie()
	require.Equal(t, "__Host-ns_session", cleared.Name)
	require.Negative(t, cleared.MaxAge)
}

func TestRequireSessionAgainstStore(t *testing.T) {
	pool := testpg.New(t)
	s := store.New(pool)
	ctx := context.Background()

	tn, err := s.CreateTenant(ctx, "acme", []byte("kek"))
	require.NoError(t, err)
	user, err := s.UpsertUser(ctx, tn.ID, "pat@acme.test")
	require.NoError(t, err)

	value, tokenHash, err := httpapi.NewSessionToken()
	require.NoError(t, err)
	_, err = s.CreateSession(ctx, tokenHash, tn.ID, user.ID)
	require.NoError(t, err)

	var seen httpapi.SessionClaims
	h := httpapi.RequireSession(s, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = httpapi.ClaimsFrom(r.Context())
	}))

	// No cookie: 401.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/workflows", nil))
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// Garbage token: 401.
	req := httptest.NewRequest("GET", "/v1/workflows", nil)
	req.AddCookie(&http.Cookie{Name: httpapi.SessionCookieName, Value: "not-a-session"})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// Valid session: passes; claims carry the CURRENT app_user role
	// through the join.
	req = httptest.NewRequest("GET", "/v1/workflows", nil)
	req.AddCookie(&http.Cookie{Name: httpapi.SessionCookieName, Value: value})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, user.ID, seen.UserID)
	require.Equal(t, tn.ID, seen.TenantID)
	require.Equal(t, "owner", seen.Role)

	// User row deleted: the same cookie is now a plain 401.
	_, err = pool.Exec(ctx, `DELETE FROM app_user WHERE id = $1`, user.ID)
	require.NoError(t, err)
	req = httptest.NewRequest("GET", "/v1/workflows", nil)
	req.AddCookie(&http.Cookie{Name: httpapi.SessionCookieName, Value: value})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}
