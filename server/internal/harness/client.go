package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Client is the harness's channel back to the control plane. It is the
// same channel a sandboxed actor will use later, which is why the local
// Compute implementation goes over HTTP instead of calling the store.
type Client struct {
	base   string
	runID  uuid.UUID
	bearer string
	hc     *http.Client
}

func NewClient(base string, runID uuid.UUID, bearer string) *Client {
	return &Client{
		base:   base,
		runID:  runID,
		bearer: bearer,
		hc:     &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.bearer)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("harness client: %s %s: %s", method, path, resp.Status)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *Client) Context(ctx context.Context) (Steps, []Tool, error) {
	var body struct {
		Steps Steps  `json:"steps"`
		Tools []Tool `json:"tools"`
	}
	err := c.do(ctx, "GET", "/internal/runs/"+c.runID.String()+"/context", nil, &body)
	return body.Steps, body.Tools, err
}

// maxToolResultBytes bounds what one tool result feeds back into the
// model's context.
const maxToolResultBytes = 256 << 10

// Invoke executes one projected tool call through the proxy's connector
// route. The tool name is {connector}__{op} exactly as the run context
// projected it; the run token is the only credential the harness holds.
// Non-2xx responses are tool-level results the model sees (the proxy
// already audited the denial); only an unreachable proxy or a dead run
// token is a transport failure, fatal to the run.
func (c *Client) Invoke(ctx context.Context, name string, input json.RawMessage) (ToolResult, error) {
	connector, op, ok := strings.Cut(name, "__")
	if !ok || connector == "" || op == "" {
		// A name the control plane never projected: the model made it
		// up. Tool-level — let it see the mistake and recover.
		return ToolResult{Content: "unknown tool " + name, IsError: true}, nil
	}
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base+"/proxy/connector/"+url.PathEscape(connector)+"/"+url.PathEscape(op),
		bytes.NewReader(input))
	if err != nil {
		return ToolResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.bearer)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return ToolResult{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxToolResultBytes+1))
	if err != nil {
		return ToolResult{}, err
	}
	truncated := len(body) > maxToolResultBytes
	if truncated {
		// The model must know it is working from partial data — a
		// silently cut-off result would read as a complete one.
		body = append(body[:maxToolResultBytes], []byte("\n\n[tool result truncated at 256KiB]")...)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized && resp.Header.Get("Tomte-Upstream") == "":
		// The proxy's own 401: the run token is dead (finalized,
		// revoked, expired) and nothing this run does can recover.
		// A relayed upstream 401 (marker present) is a broken connector
		// credential instead — tool-level, handled below.
		return ToolResult{}, fmt.Errorf("harness client: tool %s: %s", name, resp.Status)
	case resp.StatusCode >= 300:
		return ToolResult{
			Content: "tool call failed: " + resp.Status + ": " + strings.TrimSpace(string(body)),
			IsError: true,
		}, nil
	default:
		return ToolResult{Content: string(body)}, nil
	}
}

func (c *Client) Event(ctx context.Context, ev RunEvent) error {
	payload := ev.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	return c.do(ctx, "POST", "/internal/runs/"+c.runID.String()+"/events",
		map[string]any{"type": ev.Type, "payload": payload}, nil)
}

func (c *Client) Finalize(ctx context.Context, res Result) error {
	return c.do(ctx, "POST", "/internal/runs/"+c.runID.String()+"/finalize",
		map[string]any{
			"status":     string(res.Status),
			"error_kind": res.ErrorKind,
			"error_msg":  res.ErrorMsg,
			"output":     res.Output,
			"tokens_in":  res.Usage.InputTokens,
			"tokens_out": res.Usage.OutputTokens,
			"cost_cents": res.CostCents,
		}, nil)
}
