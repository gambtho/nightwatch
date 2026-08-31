package harness_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/tomte/server/internal/harness"
)

func invokeAgainst(t *testing.T, handler http.HandlerFunc, name string) (harness.ToolResult, error) {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	c := harness.NewClient(ts.URL, uuid.New(), "run-token")
	return c.Invoke(context.Background(), name, json.RawMessage(`{}`))
}

func TestInvokeSuccess(t *testing.T) {
	res, err := invokeAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/proxy/connector/slack/list_channels", r.URL.Path)
		require.Equal(t, "Bearer run-token", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"ok":true}`))
	}, "slack__list_channels")
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.JSONEq(t, `{"ok":true}`, res.Content)
}

// The 401 split: the proxy's own 401 (dead run token) is fatal; a
// relayed upstream 401 (broken connector credential, marker header
// present) is a tool-level result the model sees.
func TestInvoke401Split(t *testing.T) {
	_, err := invokeAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}, "slack__list_channels")
	require.Error(t, err, "proxy-auth 401 is fatal")

	res, err := invokeAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Tomte-Upstream", "1")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
	}, "slack__list_channels")
	require.NoError(t, err, "relayed upstream 401 is tool-level")
	require.True(t, res.IsError)
	require.Contains(t, res.Content, "401")
}

func TestInvokeDenialIsToolLevel(t *testing.T) {
	res, err := invokeAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}, "slack__post_message")
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, res.Content, "403")
}

func TestInvokeUnknownToolNameIsToolLevel(t *testing.T) {
	res, err := invokeAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("proxy must not be reached for a malformed name")
	}, "no-separator")
	require.NoError(t, err)
	require.True(t, res.IsError)
}

func TestInvokeTruncationMarked(t *testing.T) {
	big := strings.Repeat("x", 300<<10)
	res, err := invokeAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(big))
	}, "slack__list_channels")
	require.NoError(t, err)
	require.Contains(t, res.Content, "[tool result truncated at 256KiB]",
		"the model must know it is working from partial data")
	require.Less(t, len(res.Content), len(big))
}
