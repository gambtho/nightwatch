// Package harness is the agent loop that executes one run. It is the code
// that will later live inside a Substrate actor, which is why it reports
// results only through its Sink (run records are pushed, never pulled) and
// receives everything else through Input and Deps.
//
// This is the tool-less reshape of cronfoundry's internal/runner: the
// filesystem manifest/skill loading, git writeback, memory extraction, and
// MCP subprocess management did not survive the move to a hosted platform
// (see the platform spec's harvest table). The ChatTurn tool loop returns
// with connector work.
package harness

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/gambtho/nightwatch/server/internal/llm"
)

type Status string

const (
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
)

const defaultMaxTokens = 4096

type Steps struct {
	SystemPrompt string `json:"system_prompt"`
	Kickoff      string `json:"kickoff"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	MaxTokens    int    `json:"max_tokens"`
}

type Input struct {
	Steps  Steps
	APIKey string
}

type RunEvent struct {
	Type    string
	Payload map[string]any
}

type Result struct {
	Status     Status
	ErrorKind  string
	ErrorMsg   string
	Output     string
	Usage      llm.Usage
	CostCents  int
	StartedAt  time.Time
	FinishedAt time.Time
}

type Sink interface {
	Event(ctx context.Context, ev RunEvent) error
	Finalize(ctx context.Context, res Result) error
}

type Deps struct {
	ProviderFactory func(name string) (llm.Provider, error)
	Sink            Sink
	Now             func() time.Time
}

func Run(ctx context.Context, in Input, d Deps) (Result, error) {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	res := Result{StartedAt: now()}

	// Events are best-effort telemetry; the finalization is the run record
	// itself, so a Finalize failure surfaces to the caller.
	emit := func(typ string, payload map[string]any) {
		if d.Sink != nil {
			_ = d.Sink.Event(ctx, RunEvent{Type: typ, Payload: payload})
		}
	}
	finish := func() error {
		res.FinishedAt = now()
		if d.Sink == nil {
			return nil
		}
		// Finalize must deliver the run record even if ctx was canceled
		// (e.g. provider.Chat aborted) — otherwise a failure record could
		// never be written. Event emission stays best-effort on ctx as-is.
		return d.Sink.Finalize(context.WithoutCancel(ctx), res)
	}
	fail := func(kind string, err error) (Result, error) {
		res.Status = StatusFailed
		res.ErrorKind = kind
		res.ErrorMsg = err.Error()
		emit("run.fail", map[string]any{"kind": kind})
		if ferr := finish(); ferr != nil {
			err = errors.Join(err, ferr)
		}
		return res, err
	}

	emit("run.start", nil)

	provider, err := d.ProviderFactory(in.Steps.Provider)
	if err != nil {
		return fail("provider_unknown", err)
	}

	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: in.Steps.SystemPrompt},
		{Role: llm.RoleUser, Content: in.Steps.Kickoff},
	}
	maxTokens := in.Steps.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	var out strings.Builder
	usage, err := provider.Chat(ctx, msgs,
		llm.CallOptions{Model: in.Steps.Model, MaxTokens: maxTokens, APIKey: in.APIKey},
		func(c llm.StreamChunk) { out.WriteString(c.Delta) })
	if err != nil {
		return fail("llm_error", err)
	}

	res.Usage = usage
	res.CostCents = llm.CostCents(in.Steps.Provider, in.Steps.Model, usage)
	res.Output = out.String()
	res.Status = StatusSucceeded
	emit("run.finish", map[string]any{"status": string(res.Status)})
	if err := finish(); err != nil {
		return res, err
	}
	return res, nil
}
