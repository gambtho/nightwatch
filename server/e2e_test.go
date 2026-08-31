// End-to-end: session → create workflow → approve → fire → the harness
// runs against a scripted provider and pushes its record back over the
// internal API → the public API shows the finished run.
package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/compute"
	"github.com/gambtho/nightwatch/server/internal/harness"
	"github.com/gambtho/nightwatch/server/internal/httpapi"
	"github.com/gambtho/nightwatch/server/internal/internalapi"
	"github.com/gambtho/nightwatch/server/internal/llm"
	"github.com/gambtho/nightwatch/server/internal/llm/llmtest"
	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/testpg"
	"github.com/gambtho/nightwatch/server/internal/token"
)

func TestEndToEndRun(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()

	sessionKey := bytes.Repeat([]byte{1}, 32)
	signer := token.New(bytes.Repeat([]byte{2}, 32))
	provider := &llmtest.Scripted{
		Response: "This week: 40% of tickets were about the new billing page.",
		Usage:    llm.Usage{InputTokens: 100, OutputTokens: 50},
	}
	factory := func(string) (llm.Provider, error) { return provider, nil }

	// The runner closure needs the server's URL; the server needs Compute.
	// Resolve the cycle the same way serve() does: a variable captured by
	// the closure, assigned once the listener exists.
	var baseURL string
	local := compute.NewLocal(t.TempDir(), func(ctx context.Context, req compute.InvokeRequest, stateDir string) {
		client := harness.NewClient(baseURL, req.RunID, req.RunToken)
		steps, err := client.Context(ctx)
		if err != nil {
			t.Errorf("harness context: %v", err)
			return
		}
		_, _ = harness.Run(ctx, harness.Input{Steps: steps}, harness.Deps{
			ProviderFactory: factory,
			Sink:            client,
		})
	})

	mux := http.NewServeMux()
	httpapi.RegisterRoutes(mux, httpapi.Deps{Store: s, SessionKey: sessionKey, Signer: signer, Compute: local})
	internalapi.RegisterRoutes(mux, internalapi.Deps{Store: s, Signer: signer})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	baseURL = ts.URL

	tn, err := s.CreateTenant(ctx, "acme")
	require.NoError(t, err)
	user, err := s.UpsertUser(ctx, tn.ID, "pat@acme.test")
	require.NoError(t, err)
	cookie, err := httpapi.SessionCookie(sessionKey,
		httpapi.SessionClaims{UserID: user.ID, TenantID: tn.ID, Role: "owner"}, time.Hour)
	require.NoError(t, err)

	do := func(method, path string, body any) map[string]any {
		t.Helper()
		var buf bytes.Buffer
		if body != nil {
			require.NoError(t, json.NewEncoder(&buf).Encode(body))
		}
		req, err := http.NewRequestWithContext(context.Background(), method, ts.URL+path, &buf)
		require.NoError(t, err)
		req.AddCookie(cookie)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Less(t, resp.StatusCode, 300, "%s %s", method, path)
		var out map[string]any
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
		return out
	}

	out := do("POST", "/v1/workflows", map[string]any{
		"name": "weekly digest",
		"steps": map[string]any{
			"system_prompt": "You prepare the weekly support digest.",
			"kickoff":       "Summarize last week's tickets.",
			"provider":      "anthropic",
			"model":         "claude-sonnet-5",
			"max_tokens":    2048,
		},
		"permit": map[string]any{"v": 1, "llm": map[string]any{"providers": []string{"anthropic"}}, "connections": map[string]any{}},
	})
	wfID := out["workflow"].(map[string]any)["id"].(string)
	do("POST", "/v1/workflows/"+wfID+"/versions/1/approve", nil)

	out = do("POST", "/v1/workflows/"+wfID+"/runs", nil)
	runID := out["run"].(map[string]any)["id"].(string)
	require.NoError(t, uuid.Validate(runID))

	local.Wait()

	out = do("GET", "/v1/runs/"+runID, nil)
	run := out["run"].(map[string]any)
	require.Equal(t, "succeeded", run["status"])
	require.Contains(t, run["output"], "billing page")
	require.Equal(t, float64(100), run["tokens_in"])

	out = do("GET", "/v1/runs/"+runID+"/events", nil)
	events := out["events"].([]any)
	require.GreaterOrEqual(t, len(events), 2) // run.start, run.finish
}
