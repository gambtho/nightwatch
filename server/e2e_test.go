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
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/compute"
	"github.com/gambtho/nightwatch/server/internal/engine"
	"github.com/gambtho/nightwatch/server/internal/harness"
	"github.com/gambtho/nightwatch/server/internal/httpapi"
	"github.com/gambtho/nightwatch/server/internal/internalapi"
	"github.com/gambtho/nightwatch/server/internal/llm"
	"github.com/gambtho/nightwatch/server/internal/llm/llmtest"
	"github.com/gambtho/nightwatch/server/internal/proxy"
	"github.com/gambtho/nightwatch/server/internal/proxyadapter"
	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/testpg"
	"github.com/gambtho/nightwatch/server/internal/token"
	"github.com/gambtho/nightwatch/server/internal/vault"
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
	httpapi.RegisterRoutes(mux, httpapi.Deps{Store: s, SessionKey: sessionKey, Engine: &engine.Engine{Store: s, Signer: signer, Compute: local}})
	internalapi.RegisterRoutes(mux, internalapi.Deps{Store: s, Signer: signer})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	baseURL = ts.URL

	tn, err := s.CreateTenant(ctx, "acme", []byte("test-wrapped-kek")) // opaque to the store; real KEKs arrive with vault tests
	require.NoError(t, err)
	user, err := s.UpsertUser(ctx, tn.ID, "pat@acme.test")
	require.NoError(t, err)
	cookie, err := httpapi.SessionCookie(sessionKey,
		httpapi.SessionClaims{UserID: user.ID, TenantID: tn.ID, Role: "owner"}, time.Hour)
	require.NoError(t, err)

	do := newDoHelper(t, ts.URL, cookie)

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

// newDoHelper returns a helper that performs an authenticated JSON request
// against base and decodes the JSON response body.
func newDoHelper(t *testing.T, base string, cookie *http.Cookie) func(method, path string, body any) map[string]any {
	t.Helper()
	return func(method, path string, body any) map[string]any {
		t.Helper()
		var buf bytes.Buffer
		if body != nil {
			require.NoError(t, json.NewEncoder(&buf).Encode(body))
		}
		req, err := http.NewRequestWithContext(context.Background(), method, base+path, &buf)
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
}

// TestEndToEndRunThroughProxy proves the Plan 2 invariant: a run completes
// with ZERO credentials in the harness. The real ported openai provider is
// pointed at the proxy; the proxy authenticates the run token from the
// Authorization slot, injects the platform key, and forwards to a fake
// OpenAI upstream.
func TestEndToEndRunThroughProxy(t *testing.T) {
	// Pins the SDK-env-autoload isolation property that Task 10's wiring
	// otherwise rests on a comment: the pinned openai-go SDK auto-loads
	// OPENAI_API_KEY from the environment into client options if present.
	// If that ever crept back into a real credential, this proves it is
	// never used — the injected platform key below is what must reach
	// upstream, not this one.
	t.Setenv("OPENAI_API_KEY", "sdk-must-not-see-this")

	s := store.New(testpg.New(t))
	ctx := context.Background()

	sessionKey := bytes.Repeat([]byte{1}, 32)
	signer := token.New(bytes.Repeat([]byte{2}, 32))
	master, err := vault.NewMaster(bytes.Repeat([]byte{3}, 32))
	require.NoError(t, err)

	// Fake OpenAI upstream: asserts the injected platform key arrived (and
	// the run token did not), then streams one SSE chat chunk. The chunk
	// shape matches internal/llm's openai fixture format (see
	// openAIStreamFixture in openai_test.go) — that is what the ported
	// openai-go SDK's streaming client actually parses.
	var upstreamMu sync.Mutex
	var upstreamAuth, upstreamPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamMu.Lock()
		upstreamAuth = r.Header.Get("Authorization")
		upstreamPath = r.URL.Path
		upstreamMu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`data: {"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"proxied digest"}}]}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(upstream.Close)

	var baseURL string
	factory := func(name string) (llm.Provider, error) {
		return llm.NewOpenAI(baseURL + "/proxy/llm/openai"), nil
	}
	local := compute.NewLocal(t.TempDir(), func(ctx context.Context, req compute.InvokeRequest, stateDir string) {
		client := harness.NewClient(baseURL, req.RunID, req.RunToken)
		steps, err := client.Context(ctx)
		if err != nil {
			t.Errorf("harness context: %v", err)
			return
		}
		_, _ = harness.Run(ctx, harness.Input{Steps: steps, RunToken: req.RunToken}, harness.Deps{
			ProviderFactory: factory,
			Sink:            client,
		})
	})

	mux := http.NewServeMux()
	httpapi.RegisterRoutes(mux, httpapi.Deps{Store: s, SessionKey: sessionKey, Engine: &engine.Engine{Store: s, Signer: signer, Compute: local}, Vault: master})
	internalapi.RegisterRoutes(mux, internalapi.Deps{Store: s, Signer: signer})
	adapters := proxyadapter.New(s, signer, master, map[string]string{"openai": "platform-openai-key"})
	cfg := proxy.DefaultConfig()
	route := cfg.Providers["openai"]
	route.Base = upstream.URL // bare base: the SDK's emitted path arrives verbatim
	cfg.Providers["openai"] = route
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	baseURL = ts.URL
	cfg.InternalBase = baseURL
	proxy.RegisterRoutes(mux, proxy.Deps{Auth: adapters.Auth, Permits: adapters.Permits,
		Credentials: adapters.Credentials, Events: adapters.Events, Hook: proxy.NopHook{}, Config: cfg})

	wrapped, err := master.NewTenantKEK()
	require.NoError(t, err)
	tn, err := s.CreateTenant(ctx, "acme", wrapped)
	require.NoError(t, err)
	user, err := s.UpsertUser(ctx, tn.ID, "pat@acme.test")
	require.NoError(t, err)
	cookie, err := httpapi.SessionCookie(sessionKey,
		httpapi.SessionClaims{UserID: user.ID, TenantID: tn.ID, Role: "owner"}, time.Hour)
	require.NoError(t, err)

	do := newDoHelper(t, ts.URL, cookie)

	out := do("POST", "/v1/workflows", map[string]any{
		"name": "proxied digest",
		"steps": map[string]any{
			"system_prompt": "You prepare the weekly support digest.",
			"kickoff":       "Summarize last week's tickets.",
			"provider":      "openai",
			"model":         "gpt-4o-mini",
			"max_tokens":    256,
		},
		"permit": map[string]any{"v": 1, "llm": map[string]any{"providers": []string{"openai"}}, "connections": map[string]any{}},
	})
	wfID := out["workflow"].(map[string]any)["id"].(string)
	do("POST", "/v1/workflows/"+wfID+"/versions/1/approve", nil)
	out = do("POST", "/v1/workflows/"+wfID+"/runs", nil)
	runID := out["run"].(map[string]any)["id"].(string)

	local.Wait()

	out = do("GET", "/v1/runs/"+runID, nil)
	run := out["run"].(map[string]any)
	require.Equal(t, "succeeded", run["status"])
	require.Contains(t, run["output"], "proxied digest")
	upstreamMu.Lock()
	require.Equal(t, "Bearer platform-openai-key", upstreamAuth) // injected, not the run token
	// The SDK emitted /chat/completions relative to its base; with a bare
	// upstream base the exact path proves the /v1 rewrite logic is right.
	require.Equal(t, "/chat/completions", upstreamPath)
	upstreamMu.Unlock()

	out = do("GET", "/v1/runs/"+runID+"/events", nil)
	var types []string
	for _, ev := range out["events"].([]any) {
		types = append(types, ev.(map[string]any)["type"].(string))
	}
	require.Contains(t, types, "proxy.request")
}
