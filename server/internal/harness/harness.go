// Package harness is the agent loop that executes one run. It is the code
// that will later live inside a Substrate actor, which is why it reports
// results only through its Sink (run records are pushed, never pulled) and
// receives everything else through Input and Deps.
//
// The tool loop is the harvested cronfoundry contract without its
// transport: the ToolUse/tool-result error split (tool-level errors
// return to the model; transport failures are fatal with named kinds)
// and {namespace}__{tool} flat naming survive; stdio subprocesses,
// newline framing, and env-as-auth do not. "Transport" here is HTTP to
// the proxy's connector routes, through the ToolInvoker.
package harness

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/gambtho/nightwatch/server/internal/llm"
)

type Status string

const (
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
)

const (
	defaultMaxTokens = 4096
	// defaultMaxTurns bounds the tool loop; a per-workflow override is a
	// later feature.
	defaultMaxTurns = 20
	// perToolTimeout bounds one tool invocation end to end.
	perToolTimeout = 60 * time.Second
)

type Steps struct {
	SystemPrompt string `json:"system_prompt"`
	Kickoff      string `json:"kickoff"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	MaxTokens    int    `json:"max_tokens"`
}

// Tool is one projected tool definition, exactly as the run context
// served it. The harness never derives tools itself.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type Input struct {
	Steps Steps
	Tools []Tool
	// RunToken is the run's bearer JWT. It rides in the provider-native
	// auth-header slot (CallOptions.APIKey) so the egress proxy can
	// authenticate the request, strip it, and inject the real credential.
	// No API key ever enters the harness.
	RunToken string
}

// ToolResult is what a tool invocation hands back to the model. IsError
// marks denials, credential failures, and upstream connector errors —
// the model sees them and the run can finish degraded.
type ToolResult struct {
	Content string
	IsError bool
}

// ToolInvoker executes one projected tool call. A returned error is a
// transport failure (the proxy itself unreachable, the token dead) and
// is fatal to the run; everything the model should merely see comes
// back as an IsError result.
type ToolInvoker interface {
	Invoke(ctx context.Context, name string, input json.RawMessage) (ToolResult, error)
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
	Tools           ToolInvoker
	Now             func() time.Time
	MaxTurns        int // 0 means defaultMaxTurns
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
		// A failed tool-loop run has real accumulated usage; pricing it
		// is what keeps max_turns loops inside the spend caps.
		res.CostCents = llm.CostCents(in.Steps.Provider, in.Steps.Model, res.Usage)
		emit("run.fail", map[string]any{"kind": kind})
		if ferr := finish(); ferr != nil {
			err = errors.Join(err, ferr)
		}
		return res, err
	}
	succeed := func(output, stopReason string) (Result, error) {
		res.CostCents = llm.CostCents(in.Steps.Provider, in.Steps.Model, res.Usage)
		res.Output = output
		res.Status = StatusSucceeded
		payload := map[string]any{"status": string(res.Status)}
		// A max_tokens stop means the final answer was cut by budget;
		// the run still succeeds (the budget was approved) but the
		// record says so instead of passing truncation off as a clean
		// finish.
		if stopReason != "" && stopReason != "end_turn" && stopReason != "stop" {
			payload["stop_reason"] = stopReason
		}
		emit("run.finish", payload)
		if err := finish(); err != nil {
			return res, err
		}
		return res, nil
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
	opts := llm.CallOptions{Model: in.Steps.Model, MaxTokens: maxTokens, APIKey: in.RunToken}

	var out strings.Builder
	onChunk := func(c llm.StreamChunk) { out.WriteString(c.Delta) }

	// Tool-less runs keep the single-completion path unchanged.
	if len(in.Tools) == 0 {
		usage, err := provider.Chat(ctx, msgs, opts, onChunk)
		if err != nil {
			return fail("llm_error", err)
		}
		res.Usage = usage
		return succeed(out.String(), "")
	}

	tcp, ok := provider.(llm.ToolCapableProvider)
	if !ok {
		return fail("provider_tool_unsupported",
			errors.New("permit grants connections but provider "+in.Steps.Provider+" does not support tools"))
	}
	if d.Tools == nil {
		return fail("tool_transport", errors.New("no tool invoker configured"))
	}

	toolDefs := make([]llm.ToolDef, len(in.Tools))
	for i, t := range in.Tools {
		toolDefs[i] = llm.ToolDef{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema}
	}

	maxTurns := d.MaxTurns
	if maxTurns <= 0 {
		maxTurns = defaultMaxTurns
	}
	for turn := 0; turn < maxTurns; turn++ {
		tr, err := tcp.ChatTurn(ctx, msgs, toolDefs, opts, onChunk)
		if err != nil {
			return fail("llm_error", err)
		}
		res.Usage.InputTokens += tr.Usage.InputTokens
		res.Usage.OutputTokens += tr.Usage.OutputTokens

		if len(tr.ToolUses) == 0 {
			return succeed(out.String(), tr.StopReason)
		}
		msgs = append(msgs, llm.Message{
			Role: llm.RoleAssistant, Content: tr.Text, ToolUses: tr.ToolUses,
		})
		results, terr := dispatchAll(ctx, d.Tools, tr.ToolUses, emit)
		if terr != nil {
			return fail("tool_transport", terr)
		}
		msgs = append(msgs, results...)
	}
	return fail("max_turns", errors.New("tool loop exceeded max turns"))
}

// dispatchAll runs a turn's tool calls in parallel, each under the
// per-tool timeout, and returns their RoleTool messages in call order.
// The first transport failure aborts the run; tool-level failures come
// back as IsError messages the model sees.
func dispatchAll(ctx context.Context, invoker ToolInvoker, uses []llm.ToolUse, emit func(string, map[string]any)) ([]llm.Message, error) {
	type outcome struct {
		result ToolResult
		err    error
		took   time.Duration
	}
	outcomes := make([]outcome, len(uses))
	var wg sync.WaitGroup
	for i, use := range uses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			callCtx, cancel := context.WithTimeout(ctx, perToolTimeout)
			defer cancel()
			start := time.Now()
			r, err := invoker.Invoke(callCtx, use.Name, use.Input)
			outcomes[i] = outcome{result: r, err: err, took: time.Since(start)}
		}()
	}
	wg.Wait()

	// Emit every call's event first — a sibling that completed (and may
	// have written upstream) keeps its audit record even when another
	// call's transport failure aborts the run. Events carry tool name
	// and duration only, never args or results.
	var transportErr error
	for i, use := range uses {
		o := outcomes[i]
		typ := "tool.call.ok"
		if o.err != nil || o.result.IsError {
			typ = "tool.call.fail"
		}
		emit(typ, map[string]any{"tool": use.Name, "duration_ms": o.took.Milliseconds()})
		if o.err != nil && transportErr == nil {
			transportErr = o.err
		}
	}
	if transportErr != nil {
		return nil, transportErr
	}
	msgs := make([]llm.Message, 0, len(uses))
	for i, use := range uses {
		o := outcomes[i]
		msgs = append(msgs, llm.Message{
			Role: llm.RoleTool, ToolUseID: use.ID,
			Content: o.result.Content, IsError: o.result.IsError,
		})
	}
	return msgs, nil
}
