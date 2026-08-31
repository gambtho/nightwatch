package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gambtho/tomte/server/internal/captureverify"
	"github.com/gambtho/tomte/server/internal/catalog"
	"github.com/gambtho/tomte/server/internal/httpapi"
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

// fakeSlack stands in for slack.com behind the capture-verify client.
type fakeSlack struct {
	ok     bool
	scopes string
	errMsg string
}

func newSlackEnv(t *testing.T, f *fakeSlack) *env {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.scopes != "" {
			w.Header().Set("x-oauth-scopes", f.scopes)
		}
		w.Header().Set("Content-Type", "application/json")
		body := map[string]any{"ok": f.ok}
		if f.errMsg != "" {
			body["error"] = f.errMsg
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(upstream.Close)
	cat, err := catalog.Load()
	require.NoError(t, err)
	return newEnv(t, func(d *httpapi.Deps) {
		d.Catalog = cat
		d.CaptureVerify = &captureverify.Client{Upstreams: map[string]string{"slack": upstream.URL}}
	})
}

func TestPutSlackConnectionVerifiesThenStores(t *testing.T) {
	e := newSlackEnv(t, &fakeSlack{ok: true, scopes: "channels:read,channels:history"})

	resp, out := e.do(t, "PUT", "/v1/connections/default",
		map[string]any{"provider": "slack", "value": "xoxb-good", "kind": "api_key"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	conn := out["connection"].(map[string]any)
	require.Equal(t, "api_key", conn["kind"])
	require.Equal(t, "ok", conn["status"])
	// The installed app is missing chat:write — a warning, stored anyway.
	require.Equal(t, []any{"chat:write"}, out["missing_scopes"])

	resp, out = e.do(t, "GET", "/v1/catalog", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	slack := out["connectors"].([]any)[0].(map[string]any)
	require.Equal(t, true, slack["connected"])
}

func TestPutSlackConnectionBadTokenStoresNothing(t *testing.T) {
	e := newSlackEnv(t, &fakeSlack{ok: false, errMsg: "invalid_auth"})

	resp, out := e.do(t, "PUT", "/v1/connections/default",
		map[string]any{"provider": "slack", "value": "xoxb-bad", "kind": "api_key"})
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	require.Contains(t, out["message"], "token")

	resp, out = e.do(t, "GET", "/v1/connections", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, out["connections"], 0)
}

func TestPutSlackRepasteReverifies(t *testing.T) {
	f := &fakeSlack{ok: true, scopes: "channels:read,channels:history,chat:write"}
	e := newSlackEnv(t, f)

	resp, _ := e.do(t, "PUT", "/v1/connections/default",
		map[string]any{"provider": "slack", "value": "xoxb-good", "kind": "api_key"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// A later re-paste of a revoked token re-verifies and changes nothing.
	f.ok, f.errMsg = false, "invalid_auth"
	resp, _ = e.do(t, "PUT", "/v1/connections/default",
		map[string]any{"provider": "slack", "value": "xoxb-revoked", "kind": "api_key"})
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	resp, out := e.do(t, "GET", "/v1/connections", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, out["connections"], 1)
	conn := out["connections"].([]any)[0].(map[string]any)
	require.Equal(t, "ok", conn["status"])
}

func TestPutConnectionKindMismatchIs409(t *testing.T) {
	e := newSlackEnv(t, &fakeSlack{ok: true})
	resp, _ := e.do(t, "PUT", "/v1/connections/default",
		map[string]any{"provider": "slack", "value": "xoxb-good", "kind": "api_key"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Same (provider, name), different kind: repurposing a secret slot is
	// a delete + re-paste, never a silent flip.
	resp, _ = e.do(t, "PUT", "/v1/connections/default",
		map[string]any{"provider": "slack", "value": "sk-ant-x"})
	require.Equal(t, http.StatusConflict, resp.StatusCode)
}

func TestPutConnectionRejectsUnknownKind(t *testing.T) {
	e := newEnv(t)
	resp, _ := e.do(t, "PUT", "/v1/connections/default",
		map[string]any{"provider": "slack", "value": "x", "kind": "oauth"})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestPutApiKeyUnknownProviderIs400(t *testing.T) {
	e := newSlackEnv(t, &fakeSlack{ok: true})
	resp, _ := e.do(t, "PUT", "/v1/connections/default",
		map[string]any{"provider": "not-in-catalog", "value": "x", "kind": "api_key"})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}
