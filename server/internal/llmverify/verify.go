// Package llmverify is the first-run LLM key check: the spec's disclosed,
// metered "one live, minimal call" made with a candidate endpoint and key
// at paste time, before anything is saved. It reuses the proxy's actual
// endpoint machinery — endpoint.Validate upstream of the call, Route()
// for the one allowed (method, path), CredentialHeader() for the header
// slot — so the request a verify sends is the request a run's proxy
// rewrite would produce. Like captureverify, it fails closed: nothing but
// a well-formed positive verifies a key.
package llmverify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gambtho/tomte/server/internal/endpoint"
	"github.com/gambtho/tomte/server/internal/llm"
)

// Result mirrors captureverify: verified or not, user-facing copy when
// not, and the token usage the metered call consumed.
type Result struct {
	OK      bool
	Message string
	Usage   llm.Usage
	// Billed reports whether the upstream answered 2xx — it executed
	// (and, on a paid endpoint, billed) the call, whatever we made of
	// the response. The metering caller records billed attempts even
	// when verification failed.
	Billed bool
}

const (
	// A local model may need a moment to load; hosted endpoints answer a
	// 1-token call well inside this.
	requestTimeout = 30 * time.Second
	maxBodyBytes   = 1 << 20
	// The Messages API version the direct call pins — the run path's SDK
	// sends its own; this only needs to be one the API accepts.
	anthropicVersion = "2023-06-01"
)

// Client makes verify calls. The zero value is ready to use; the
// transport posture (proxy selection disabled, redirects refused,
// timeout) is deliberately not swappable.
type Client struct{}

func (c *Client) httpClient() *http.Client {
	return &http.Client{
		Timeout: requestTimeout,
		// Proxy selection disabled: HTTP(S)_PROXY must not route a
		// credential-bearing call through an unvetted intermediary.
		Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return fmt.Errorf("redirects are refused")
		},
	}
}

// Verify makes one minimal chat call against the candidate endpoint with
// the candidate secret. The endpoint must already have passed
// endpoint.Validate. A non-nil error is a programming mistake; everything
// user-actionable comes back in Result.
func (c *Client) Verify(ctx context.Context, e endpoint.Endpoint, secret string) (Result, error) {
	base, method, path := e.Route()
	body := map[string]any{
		"model":      e.RunModel,
		"max_tokens": 1,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Result{}, fmt.Errorf("llmverify: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method,
		strings.TrimRight(base, "/")+"/"+path, strings.NewReader(string(raw)))
	if err != nil {
		return Result{}, fmt.Errorf("llmverify: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	switch e.CredentialHeader() {
	case "":
		// local: nothing is injected.
	case "x-api-key":
		req.Header.Set("x-api-key", secret)
	case "api-key":
		req.Header.Set("api-key", secret)
	default:
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	if e.Kind == endpoint.KindAnthropic {
		req.Header.Set("anthropic-version", anthropicVersion)
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		slog.Warn("llmverify: endpoint unreachable", "base_url", e.BaseURL, "err", err)
		return Result{Message: "Couldn't reach the service to check the key. Check your connection and try again."}, nil
	}
	defer resp.Body.Close()
	respRaw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))

	// Both API shapes put an explanation under "error"; both usage shapes
	// are accepted so one struct reads either.
	var envelope struct {
		Error json.RawMessage `json:"error"`
		Usage struct {
			InputTokens      int `json:"input_tokens"`
			OutputTokens     int `json:"output_tokens"`
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	parseErr := json.Unmarshal(respRaw, &envelope)
	detail := errorDetail(envelope.Error)
	billed := resp.StatusCode >= 200 && resp.StatusCode <= 299

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		// An auth-status rejection is a re-paste, never a retry.
		msg := "The service didn't accept this key. Copy it again and re-paste — a character may be missing."
		if detail != "" {
			msg += " (" + detail + ")"
		}
		return Result{Message: msg}, nil
	case !billed:
		msg := fmt.Sprintf("The service returned an error (HTTP %d) while checking the key.", resp.StatusCode)
		if detail != "" {
			msg += " " + detail
		} else {
			msg += " Try again in a moment."
		}
		return Result{Message: msg}, nil
	}

	// A 2xx with a populated error field is a gateway-style rejection
	// (OpenRouter-class: HTTP 200 + {"error": ...} for a bad key) — a
	// status-only check would store garbage.
	if parseErr == nil && detail != "" {
		return Result{Billed: true, Message: "The service didn't accept this key: " + detail}, nil
	}

	// Fail closed on a 2xx we cannot read — an interstitial or truncated
	// body must never verify a key. It still billed.
	if readErr != nil || parseErr != nil {
		slog.Warn("llmverify: unreadable 2xx verify response",
			"base_url", e.BaseURL, "status", resp.StatusCode, "read_err", readErr, "parse_err", parseErr)
		return Result{Billed: true, Message: "The service sent an unexpected response while checking the key. Try again in a moment."}, nil
	}

	u := llm.Usage{
		InputTokens:  envelope.Usage.InputTokens + envelope.Usage.PromptTokens,
		OutputTokens: envelope.Usage.OutputTokens + envelope.Usage.CompletionTokens,
	}
	return Result{OK: true, Billed: true, Usage: u}, nil
}

// errorDetail extracts a human-readable message from an error field that
// may be an object ({"message": ...}), a bare string, or absent/null.
func errorDetail(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var obj struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && obj.Message != "" {
		return obj.Message
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return ""
}
