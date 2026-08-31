package httpapi_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/tomte/server/internal/store"
)

// seedConnectorKey stores an api_key-kind connector credential straight
// into the vault, the way P2's capture surface will (rescued from the
// retired OAuth tests; the approval checks it feeds are kind-agnostic).
func seedConnectorKey(t *testing.T, e *env, provider, value string) store.Connection {
	t.Helper()
	ctx := context.Background()
	wrapped, kekVersion, err := e.store.TenantKEK(ctx, e.tenant.ID)
	require.NoError(t, err)
	dek, ct, nonce, err := e.vault.EncryptSecret(wrapped, value)
	require.NoError(t, err)
	conn, err := e.store.UpsertConnection(ctx, e.tenant.ID, "default", "api_key", provider,
		dek, ct, nonce, kekVersion)
	require.NoError(t, err)
	return conn
}

// Approval-time connection re-validation (build item 9).
func TestApproveChecksConnections(t *testing.T) {
	e := newEnv(t, withCatalog(t))

	create := func(t *testing.T) string {
		b := connectionsBody(map[string]any{
			"slack": map[string]any{
				"kind": "http", "ops": []string{"post_message"},
				"resources": map[string]any{"post_message": map[string]any{"channel": []string{"C1"}}},
			},
		})
		resp, out := e.do(t, "POST", "/v1/workflows", b)
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		return out["workflow"].(map[string]any)["id"].(string)
	}

	// Not connected: 400.
	id := create(t)
	resp, out := e.do(t, "POST", "/v1/workflows/"+id+"/versions/1/approve", nil)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Contains(t, out["error"], "not connected")

	// Connected but needs_reauth (the pasted token was revoked upstream): 400.
	conn := seedConnectorKey(t, e, "slack", "xoxb-at")
	applied, err := e.store.MarkConnectionNeedsReauth(context.Background(), e.tenant.ID, conn.ID)
	require.NoError(t, err)
	require.True(t, applied)
	resp, out = e.do(t, "POST", "/v1/workflows/"+id+"/versions/1/approve", nil)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Contains(t, out["error"], "re-authorization")

	// Healthy: a re-paste resets status and approval goes through.
	seedConnectorKey(t, e, "slack", "xoxb-at2")
	resp, _ = e.do(t, "POST", "/v1/workflows/"+id+"/versions/1/approve", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestApproveChecksNamedLLMConnection(t *testing.T) {
	e := newEnv(t, withCatalog(t))
	b := workflowBody()
	b["permit"] = map[string]any{"v": 1, "llm": map[string]any{
		"providers": []string{"anthropic"}, "connection": "work-key",
	}}
	resp, out := e.do(t, "POST", "/v1/workflows", b)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	id := out["workflow"].(map[string]any)["id"].(string)

	resp, out = e.do(t, "POST", "/v1/workflows/"+id+"/versions/1/approve", nil)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Contains(t, out["error"], "work-key")

	// Store the named key; approval goes through.
	resp, _ = e.do(t, "PUT", "/v1/connections/work-key",
		map[string]any{"provider": "anthropic", "value": "sk-ant-real"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp, _ = e.do(t, "POST", "/v1/workflows/"+id+"/versions/1/approve", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}
