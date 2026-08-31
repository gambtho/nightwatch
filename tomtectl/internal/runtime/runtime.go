// Package runtime is the Tomte agent runtime: the program inside the
// pod. It reads the mounted agent.yaml each wake — behavior lives in
// the file, never in the image — and either prints the steps verbatim
// (no llm: the K1 contract) or hands them to the configured model and
// logs the reply. Every failure is closed: an error status, an
// unreadable body, malformed JSON, or an empty completion is logged as
// a failure and never printed as a result.
package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gambtho/tomte/tomtectl/internal/agentfile"
)

// maxBody bounds how much of a response is read; a model reply fits
// comfortably, and an endpoint cannot flood the pod's memory.
const maxBody = 1 << 20

// Loop runs the agent at path forever: wake, act, sleep, re-read the
// file. Config-level faults (unreadable file, missing key) are fatal —
// a loud CrashLoopBackOff, never a silent default. Per-wake call
// failures are logged and the schedule survives them.
func Loop(ctx context.Context, path, apiKey string, out io.Writer, client *http.Client) error {
	a, err := loadAgent(path)
	if err != nil {
		return err
	}
	if l := a.Spec.LLM; l.Enabled() && !l.Local && apiKey == "" {
		return fmt.Errorf("spec.llm.secretRef is set but TOMTE_API_KEY is empty — was the Secret created? (`tomtectl set-key`)")
	}
	if l := a.Spec.LLM; l.Enabled() {
		fmt.Fprintf(out, "tomte agent starting: waking every %s, thinking with %s (%s)\n",
			a.Spec.Schedule.Every, l.Model, l.Kind)
	} else {
		fmt.Fprintf(out, "tomte agent starting: waking every %s\n", a.Spec.Schedule.Every)
	}
	for {
		wake(ctx, a, apiKey, out, client)
		every, err := time.ParseDuration(a.Spec.Schedule.Every)
		if err != nil {
			return fmt.Errorf("spec.schedule.every: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(every):
		}
		// Re-read the file so a ConfigMap edit takes effect on the
		// next wake once the volume syncs. A file that stops parsing
		// is a loud crash, never a stale-config fallback.
		if a, err = loadAgent(path); err != nil {
			return err
		}
	}
}

func wake(ctx context.Context, a *agentfile.Agent, apiKey string, out io.Writer, client *http.Client) {
	ts := time.Now().UTC().Format(time.RFC3339)
	if !a.Spec.LLM.Enabled() {
		for _, s := range a.Spec.Steps {
			fmt.Fprintf(out, "%s %s\n", ts, s.Text)
		}
		return
	}
	reply, err := Wake(ctx, a, apiKey, client)
	if err != nil {
		fmt.Fprintf(out, "%s wake failed: %v\n", ts, err)
		return
	}
	for _, line := range strings.Split(reply, "\n") {
		fmt.Fprintf(out, "%s %s\n", ts, line)
	}
}

// Wake performs one LLM call for the agent: its steps as one request
// to the configured endpoint, the model's text back. Anything but a
// well-formed positive response is an error.
func Wake(ctx context.Context, a *agentfile.Agent, apiKey string, client *http.Client) (string, error) {
	l := a.Spec.LLM
	system := fmt.Sprintf("You are %q, a Tomte agent running on Kubernetes. Each time you wake, perform your steps and reply with the result — concise, plain text.", a.Metadata.Name)
	var steps strings.Builder
	for i, s := range a.Spec.Steps {
		fmt.Fprintf(&steps, "%d. [%s] %s\n", i+1, s.ID, s.Text)
	}

	var url string
	var body any
	base := strings.TrimSuffix(l.BaseURL, "/")
	switch l.Kind {
	case "anthropic":
		url = base + "/v1/messages"
		body = map[string]any{
			"model":      l.Model,
			"max_tokens": 1024,
			"system":     system,
			"messages": []map[string]string{
				{"role": "user", "content": "Your steps:\n" + steps.String()},
			},
		}
	default: // openai_compatible — the parser admits nothing else
		url = base + "/chat/completions"
		body = map[string]any{
			"model": l.Model,
			"messages": []map[string]string{
				{"role": "system", "content": system},
				{"role": "user", "content": "Your steps:\n" + steps.String()},
			},
		}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		if l.Kind == "anthropic" {
			req.Header.Set("x-api-key", apiKey)
		} else {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
	}
	if l.Kind == "anthropic" {
		req.Header.Set("anthropic-version", "2023-06-01")
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("calling %s: %w", url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return "", fmt.Errorf("reading response from %s: %w", url, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("endpoint returned %d: %s", resp.StatusCode, truncate(raw))
	}

	var text string
	switch l.Kind {
	case "anthropic":
		var r struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return "", fmt.Errorf("parsing response: %w (body: %s)", err, truncate(raw))
		}
		for _, c := range r.Content {
			if c.Type == "text" {
				text += c.Text
			}
		}
	default:
		var r struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return "", fmt.Errorf("parsing response: %w (body: %s)", err, truncate(raw))
		}
		if len(r.Choices) > 0 {
			text = r.Choices[0].Message.Content
		}
	}
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("empty completion (body: %s)", truncate(raw))
	}
	return text, nil
}

func loadAgent(path string) (*agentfile.Agent, error) {
	a, _, err := agentfile.Load(path)
	return a, err
}

func truncate(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// StubHandler serves the minimal OpenAI-compatible endpoint the kind
// e2e talks to: it rejects any request whose Bearer key is not its
// own Secret-mounted key (proving the Secret round trip on both ends)
// and answers with a marked, well-formed completion.
func StubHandler(expectedKey string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+expectedKey {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": "invalid api key"}})
			return
		}
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{
					"role":    "assistant",
					"content": fmt.Sprintf("TOMTE_STUB_OK: %s answered a %d-message request", req.Model, len(req.Messages)),
				},
			}},
		})
	})
	return mux
}
