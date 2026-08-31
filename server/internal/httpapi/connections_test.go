package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConnectionEndpoints(t *testing.T) {
	e := newEnv(t)

	resp, out := e.do(t, "PUT", "/v1/connections/default",
		map[string]any{"provider": "anthropic", "value": "sk-ant-test-123"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	conn := out["connection"].(map[string]any)
	require.Equal(t, "anthropic", conn["provider"])
	// The secret value never appears in any response.
	for k, v := range conn {
		s, ok := v.(string)
		require.False(t, ok && strings.Contains(s, "sk-ant"), "field %s leaked the secret", k)
	}

	resp, out = e.do(t, "GET", "/v1/connections", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, out["connections"], 1)

	resp, _ = e.do(t, "DELETE", "/v1/connections/default?provider=anthropic", nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp, _ = e.do(t, "DELETE", "/v1/connections/default?provider=anthropic", nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestConnectionPutValidation(t *testing.T) {
	e := newEnv(t)
	resp, _ := e.do(t, "PUT", "/v1/connections/default", map[string]any{"provider": "", "value": "x"})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp, _ = e.do(t, "PUT", "/v1/connections/default", map[string]any{"provider": "anthropic", "value": ""})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestConnectionPutOversizedBodyIs413(t *testing.T) {
	e := newEnv(t)
	resp, _ := e.do(t, "PUT", "/v1/connections/default",
		map[string]any{"provider": "anthropic", "value": strings.Repeat("x", 2<<20)})
	require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
}
