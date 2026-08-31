package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/tomte/server/internal/httpapi"
)

func TestLogoutDeletesSessionAndClearsCookie(t *testing.T) {
	e := newEnv(t)

	// e.cookie authenticates before logout.
	resp, _ := e.do(t, "GET", "/v1/me", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp, _ = e.do(t, "POST", "/v1/auth/logout", nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	var cleared *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == httpapi.SessionCookieName {
			cleared = c
		}
	}
	require.NotNil(t, cleared)
	require.Negative(t, cleared.MaxAge)

	resp, _ = e.do(t, "GET", "/v1/me", nil)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}
