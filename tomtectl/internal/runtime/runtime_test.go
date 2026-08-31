package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gambtho/tomte/tomtectl/internal/agentfile"
)

func agentDoc(t *testing.T, llm string) *agentfile.Agent {
	t.Helper()
	doc := `
apiVersion: tomte.dev/v1alpha1
kind: Agent
metadata:
  name: hello
spec:
  steps:
    - id: greet
      text: Say hello to the cluster.
  schedule:
    every: 1s
` + llm
	a, err := agentfile.Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func llmBlock(kind, baseURL string) string {
	return `  llm:
    kind: ` + kind + `
    base_url: ` + baseURL + `
    model: test-model
    secretRef: hello-key
`
}

func TestWakeOpenAI(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&gotBody)
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "howdy from the model"}}},
		})
	}))
	defer srv.Close()

	a := agentDoc(t, llmBlock("openai_compatible", srv.URL+"/v1"))
	out, err := Wake(context.Background(), a, "sk-test", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if out != "howdy from the model" {
		t.Errorf("out = %q", out)
	}
	if gotPath != "/v1/chat/completions" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotBody["model"] != "test-model" {
		t.Errorf("model = %v", gotBody["model"])
	}
	if !strings.Contains(mustJSON(t, gotBody), "Say hello to the cluster.") {
		t.Errorf("steps not in request: %v", gotBody)
	}
}

func TestWakeAnthropic(t *testing.T) {
	var gotPath, gotKey, gotVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		json.NewEncoder(w).Encode(map[string]any{
			"content": []any{map[string]any{"type": "text", "text": "hello from claude"}},
		})
	}))
	defer srv.Close()

	a := agentDoc(t, llmBlock("anthropic", srv.URL))
	out, err := Wake(context.Background(), a, "sk-ant-test", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello from claude" {
		t.Errorf("out = %q", out)
	}
	if gotPath != "/v1/messages" {
		t.Errorf("path = %q", gotPath)
	}
	if gotKey != "sk-ant-test" || gotVersion == "" {
		t.Errorf("headers key=%q version=%q", gotKey, gotVersion)
	}
}

// Fail closed: an error status, an unreadable body, malformed JSON, or
// an empty completion must all be errors — never a printable result.
func TestWakeFailsClosed(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		wantErr string
	}{
		{"non-2xx", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"error":"nope"}`, http.StatusUnauthorized)
		}, "401"},
		{"html interstitial", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("<html>blocked</html>"))
		}, "parsing"},
		{"empty content", func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"choices": []any{map[string]any{"message": map[string]any{"content": ""}}},
			})
		}, "empty"},
		{"no choices", func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"choices": []any{}})
		}, "empty"},
		{"truncated body", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "500")
			w.Write([]byte(`{"choices":`))
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			a := agentDoc(t, llmBlock("openai_compatible", srv.URL+"/v1"))
			out, err := Wake(context.Background(), a, "sk-test", srv.Client())
			if err == nil {
				t.Fatalf("want error, got output %q", out)
			}
			if tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not mention %q", err, tc.wantErr)
			}
			if strings.Contains(err.Error(), "sk-test") {
				t.Errorf("error must never carry the key: %q", err)
			}
		})
	}
}

func TestStubSpeaksOpenAIAndChecksTheKey(t *testing.T) {
	srv := httptest.NewServer(StubHandler("sk-stub"))
	defer srv.Close()

	a := agentDoc(t, llmBlock("openai_compatible", srv.URL+"/v1"))
	out, err := Wake(context.Background(), a, "sk-stub", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "TOMTE_STUB_OK") {
		t.Errorf("stub reply missing marker: %q", out)
	}

	if _, err := Wake(context.Background(), a, "sk-wrong", srv.Client()); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("wrong key must fail closed with a 401, got %v", err)
	}
}

// TestLoopKeepsK1Behavior: with no llm the runtime prints each step's
// text on the schedule, re-reading the mounted file each wake.
func TestLoopKeepsK1Behavior(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	doc := strings.Replace(string(agentfile.Template), "every: 30s", "every: 1s", 1)
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	var buf strings.Builder
	err := Loop(ctx, path, "", &buf, http.DefaultClient)
	if err != nil && ctx.Err() == nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.Contains(got, "waking every 1s") {
		t.Errorf("no startup line:\n%s", got)
	}
	if strings.Count(got, "Hello, world — from the hello agent.") < 2 {
		t.Errorf("steps did not loop:\n%s", got)
	}
}

// TestLoopFailsClosedOnMissingKey: an llm agent with no key must not
// start at all.
func TestLoopFailsClosedOnMissingKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	doc := `
apiVersion: tomte.dev/v1alpha1
kind: Agent
metadata:
  name: hello
spec:
  steps:
    - id: greet
      text: hi
  schedule:
    every: 1s
` + llmBlock("openai_compatible", "https://api.openai.com/v1")
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	err := Loop(context.Background(), path, "", &buf, http.DefaultClient)
	if err == nil || !strings.Contains(err.Error(), "TOMTE_API_KEY") {
		t.Fatalf("want missing-key error, got %v", err)
	}
}

// TestLoopLogsWakeFailuresAndContinues: a failing endpoint is a logged
// failure, never a printed result, and the schedule survives it.
func TestLoopLogsWakeFailuresAndContinues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	doc := `
apiVersion: tomte.dev/v1alpha1
kind: Agent
metadata:
  name: hello
spec:
  steps:
    - id: greet
      text: hi
  schedule:
    every: 1s
` + llmBlock("openai_compatible", srv.URL+"/v1")
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	var buf strings.Builder
	Loop(ctx, path, "sk-test", &buf, srv.Client())
	got := buf.String()
	if strings.Count(got, "wake failed") < 2 {
		t.Errorf("failures should be logged each wake, and the loop should survive them:\n%s", got)
	}
	if strings.Contains(got, "sk-test") {
		t.Errorf("output must never carry the key:\n%s", got)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
