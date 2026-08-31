package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// originRequest fires a mutating authenticated request with the given
// Origin header ("" = absent) and returns the status code.
func originRequest(t *testing.T, e *env, origin string) int {
	t.Helper()
	req, err := http.NewRequest("POST", e.ts.URL+"/v1/workflows", nil)
	require.NoError(t, err)
	req.AddCookie(e.cookie)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	return resp.StatusCode
}

func TestOriginPolicyOnMutatingRoutes(t *testing.T) {
	e := newEnv(t)

	// Absent: allowed (non-browser clients, same-origin navigation); the
	// request then fails on its own merits (empty body → 400), not 403.
	require.NotEqual(t, http.StatusForbidden, originRequest(t, e, ""))

	// Exact match with the configured public origin: allowed.
	require.NotEqual(t, http.StatusForbidden, originRequest(t, e, e.baseURL.String()))

	// Anything else: 403 before the handler runs.
	require.Equal(t, http.StatusForbidden, originRequest(t, e, "https://evil.test"))
	require.Equal(t, http.StatusForbidden, originRequest(t, e, "http://app.tomte.test"))
	require.Equal(t, http.StatusForbidden, originRequest(t, e, "null"))
}

func TestOriginPolicySkipsReadOnlyRoutes(t *testing.T) {
	e := newEnv(t)
	req, err := http.NewRequest("GET", e.ts.URL+"/v1/workflows", nil)
	require.NoError(t, err)
	req.AddCookie(e.cookie)
	req.Header.Set("Origin", "https://evil.test")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "GETs are not gated on Origin")
}

func TestOriginPolicyOnAuthRoutes(t *testing.T) {
	e := newEnv(t)
	req, err := http.NewRequest("POST", e.ts.URL+"/v1/auth/magic-link", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", "https://evil.test")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}
