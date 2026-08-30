package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

func (c *Client) Context(ctx context.Context) (Steps, error) {
	var body struct {
		Steps Steps `json:"steps"`
	}
	err := c.do(ctx, "GET", "/internal/runs/"+c.runID.String()+"/context", nil, &body)
	return body.Steps, err
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
