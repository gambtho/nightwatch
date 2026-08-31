package harness_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/harness"
	"github.com/gambtho/nightwatch/server/internal/llm"
	"github.com/gambtho/nightwatch/server/internal/llm/llmtest"
)

type memSink struct {
	events   []harness.RunEvent
	final    *harness.Result
	finalErr error
}

func (m *memSink) Event(ctx context.Context, ev harness.RunEvent) error {
	m.events = append(m.events, ev)
	return nil
}

func (m *memSink) Finalize(ctx context.Context, res harness.Result) error {
	m.final = &res
	return m.finalErr
}

func steps() harness.Steps {
	return harness.Steps{
		SystemPrompt: "You prepare the weekly support digest.",
		Kickoff:      "Summarize last week's tickets.",
		Provider:     "scripted",
		Model:        "test-model",
		MaxTokens:    1024,
	}
}

func TestRunSuccess(t *testing.T) {
	provider := &llmtest.Scripted{Response: "the digest", Usage: llm.Usage{InputTokens: 100, OutputTokens: 50}}
	sink := &memSink{}
	res, err := harness.Run(context.Background(), harness.Input{Steps: steps()}, harness.Deps{
		ProviderFactory: func(string) (llm.Provider, error) { return provider, nil },
		Sink:            sink,
	})
	require.NoError(t, err)
	require.Equal(t, harness.StatusSucceeded, res.Status)
	require.Equal(t, "the digest", res.Output)
	require.Equal(t, 100, res.Usage.InputTokens)

	// System prompt and kickoff reached the model.
	require.Len(t, provider.Calls, 1)
	require.Equal(t, llm.RoleSystem, provider.Calls[0][0].Role)
	require.Equal(t, llm.RoleUser, provider.Calls[0][1].Role)

	// Events and finalization were pushed.
	require.NotNil(t, sink.final)
	require.Equal(t, harness.StatusSucceeded, sink.final.Status)
	var types []string
	for _, ev := range sink.events {
		types = append(types, ev.Type)
	}
	require.Equal(t, []string{"run.start", "run.finish"}, types)
}

func TestRunProviderError(t *testing.T) {
	provider := &llmtest.Scripted{Err: errors.New("model unavailable")}
	sink := &memSink{}
	res, err := harness.Run(context.Background(), harness.Input{Steps: steps()}, harness.Deps{
		ProviderFactory: func(string) (llm.Provider, error) { return provider, nil },
		Sink:            sink,
	})
	require.Error(t, err)
	require.Equal(t, harness.StatusFailed, res.Status)
	require.Equal(t, "llm_error", res.ErrorKind)
	require.NotNil(t, sink.final)
	require.Equal(t, harness.StatusFailed, sink.final.Status)
}

func TestRunUnknownProvider(t *testing.T) {
	res, err := harness.Run(context.Background(), harness.Input{Steps: steps()}, harness.Deps{
		ProviderFactory: func(string) (llm.Provider, error) { return nil, errors.New("nope") },
		Sink:            &memSink{},
	})
	require.Error(t, err)
	require.Equal(t, "provider_unknown", res.ErrorKind)
}

func TestRunFinalizeErrorSurfaces(t *testing.T) {
	// The finalization IS the run record (records are pushed, never
	// pulled), so failing to deliver it must not look like success.
	provider := &llmtest.Scripted{Response: "the digest"}
	sink := &memSink{finalErr: errors.New("control plane unreachable")}
	res, err := harness.Run(context.Background(), harness.Input{Steps: steps()}, harness.Deps{
		ProviderFactory: func(string) (llm.Provider, error) { return provider, nil },
		Sink:            sink,
	})
	require.Error(t, err)
	require.Equal(t, harness.StatusSucceeded, res.Status) // the work succeeded; recording it did not
}

func TestRunTokenRidesTheAPIKeySlot(t *testing.T) {
	var sawKey string
	provider := &keyCapturingProvider{onKey: func(k string) { sawKey = k }}
	_, err := harness.Run(context.Background(),
		harness.Input{Steps: steps(), RunToken: "run-jwt-abc"},
		harness.Deps{
			ProviderFactory: func(string) (llm.Provider, error) { return provider, nil },
			Sink:            &memSink{},
		})
	require.NoError(t, err)
	require.Equal(t, "run-jwt-abc", sawKey)
}

type keyCapturingProvider struct{ onKey func(string) }

func (p *keyCapturingProvider) Chat(ctx context.Context, msgs []llm.Message, opts llm.CallOptions, onChunk func(llm.StreamChunk)) (llm.Usage, error) {
	p.onKey(opts.APIKey)
	return llm.Usage{}, nil
}

// fakeInvoker scripts tool outcomes by name.
type fakeInvoker struct {
	results map[string]harness.ToolResult
	errs    map[string]error
	calls   []string
}

func (f *fakeInvoker) Invoke(ctx context.Context, name string, input json.RawMessage) (harness.ToolResult, error) {
	f.calls = append(f.calls, name)
	if err, ok := f.errs[name]; ok {
		return harness.ToolResult{}, err
	}
	if r, ok := f.results[name]; ok {
		return r, nil
	}
	return harness.ToolResult{Content: "ok:" + name}, nil
}

func toolInput(s string) json.RawMessage { return json.RawMessage(s) }

func toolSet() []harness.Tool {
	return []harness.Tool{
		{Name: "slack__read_messages", Description: "Read messages.", InputSchema: toolInput(`{"type":"object"}`)},
		{Name: "slack__post_message", Description: "Post.", InputSchema: toolInput(`{"type":"object"}`)},
	}
}

func TestToolLoopHappyPath(t *testing.T) {
	provider := &llmtest.ScriptedTurns{Turns: []llmtest.TurnScript{
		{ToolUses: []llm.ToolUse{
			{ID: "t1", Name: "slack__read_messages", Input: toolInput(`{"channel":"C1"}`)},
			{ID: "t2", Name: "slack__post_message", Input: toolInput(`{"channel":"C1","text":"hi"}`)},
		}, Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}},
		{Text: "digest posted", Usage: llm.Usage{InputTokens: 20, OutputTokens: 7}},
	}}
	sink := &memSink{}
	inv := &fakeInvoker{results: map[string]harness.ToolResult{
		"slack__read_messages": {Content: `{"messages":[]}`},
	}}
	res, err := harness.Run(context.Background(),
		harness.Input{Steps: steps(), Tools: toolSet(), RunToken: "tok"},
		harness.Deps{
			ProviderFactory: func(string) (llm.Provider, error) { return provider, nil },
			Sink:            sink, Tools: inv,
		})
	require.NoError(t, err)
	require.Equal(t, harness.StatusSucceeded, res.Status)
	require.Contains(t, res.Output, "digest posted")
	require.Equal(t, llm.Usage{InputTokens: 30, OutputTokens: 12}, res.Usage, "usage summed across turns")
	require.ElementsMatch(t, []string{"slack__read_messages", "slack__post_message"}, inv.calls)

	// The second turn saw the assistant tool uses and both results, in
	// call order, with the tool defs projected verbatim.
	require.Len(t, provider.TurnMsgs, 2)
	second := provider.TurnMsgs[1]
	require.Equal(t, llm.RoleAssistant, second[2].Role)
	require.Equal(t, llm.RoleTool, second[3].Role)
	require.Equal(t, "t1", second[3].ToolUseID)
	require.False(t, second[3].IsError)
	require.Equal(t, "t2", second[4].ToolUseID)
	require.Len(t, provider.TurnTools[0], 2)

	var types []string
	for _, ev := range sink.events {
		types = append(types, ev.Type)
	}
	require.Contains(t, types, "tool.call.ok")
	require.NotContains(t, types, "tool.call.fail")
	// Events carry only tool + duration, never args or results.
	for _, ev := range sink.events {
		if ev.Type == "tool.call.ok" {
			require.NotContains(t, ev.Payload, "args")
			require.Contains(t, ev.Payload, "tool")
			require.Contains(t, ev.Payload, "duration_ms")
		}
	}
}

func TestToolLoopToolLevelErrorReachesModel(t *testing.T) {
	provider := &llmtest.ScriptedTurns{Turns: []llmtest.TurnScript{
		{ToolUses: []llm.ToolUse{{ID: "t1", Name: "slack__post_message", Input: toolInput(`{}`)}}},
		{Text: "could not post"},
	}}
	sink := &memSink{}
	inv := &fakeInvoker{results: map[string]harness.ToolResult{
		"slack__post_message": {Content: "tool call failed: 403 Forbidden: forbidden", IsError: true},
	}}
	res, err := harness.Run(context.Background(),
		harness.Input{Steps: steps(), Tools: toolSet(), RunToken: "tok"},
		harness.Deps{ProviderFactory: func(string) (llm.Provider, error) { return provider, nil }, Sink: sink, Tools: inv})
	require.NoError(t, err)
	require.Equal(t, harness.StatusSucceeded, res.Status, "run finishes degraded")

	second := provider.TurnMsgs[1]
	last := second[len(second)-1]
	require.Equal(t, llm.RoleTool, last.Role)
	require.True(t, last.IsError, "denial visible to the model as an error result")

	var types []string
	for _, ev := range sink.events {
		types = append(types, ev.Type)
	}
	require.Contains(t, types, "tool.call.fail")
}

func TestToolLoopTransportFailureIsFatal(t *testing.T) {
	provider := &llmtest.ScriptedTurns{Turns: []llmtest.TurnScript{
		{ToolUses: []llm.ToolUse{{ID: "t1", Name: "slack__read_messages", Input: toolInput(`{}`)}}},
	}}
	sink := &memSink{}
	inv := &fakeInvoker{errs: map[string]error{"slack__read_messages": errors.New("proxy unreachable")}}
	res, err := harness.Run(context.Background(),
		harness.Input{Steps: steps(), Tools: toolSet(), RunToken: "tok"},
		harness.Deps{ProviderFactory: func(string) (llm.Provider, error) { return provider, nil }, Sink: sink, Tools: inv})
	require.Error(t, err)
	require.Equal(t, harness.StatusFailed, res.Status)
	require.Equal(t, "tool_transport", res.ErrorKind)
}

func TestToolLoopNonToolProviderFails(t *testing.T) {
	// Scripted (not ScriptedTurns) implements only Chat.
	provider := &llmtest.Scripted{Response: "text"}
	sink := &memSink{}
	res, err := harness.Run(context.Background(),
		harness.Input{Steps: steps(), Tools: toolSet(), RunToken: "tok"},
		harness.Deps{ProviderFactory: func(string) (llm.Provider, error) { return provider, nil }, Sink: sink, Tools: &fakeInvoker{}})
	require.Error(t, err)
	require.Equal(t, "provider_tool_unsupported", res.ErrorKind)
}

func TestToolLoopMaxTurns(t *testing.T) {
	// Every turn asks for another tool call, forever.
	turns := make([]llmtest.TurnScript, 30)
	for i := range turns {
		turns[i] = llmtest.TurnScript{ToolUses: []llm.ToolUse{{ID: "t", Name: "slack__read_messages", Input: toolInput(`{}`)}}}
	}
	provider := &llmtest.ScriptedTurns{Turns: turns}
	sink := &memSink{}
	res, err := harness.Run(context.Background(),
		harness.Input{Steps: steps(), Tools: toolSet(), RunToken: "tok"},
		harness.Deps{ProviderFactory: func(string) (llm.Provider, error) { return provider, nil }, Sink: sink, Tools: &fakeInvoker{}, MaxTurns: 3})
	require.Error(t, err)
	require.Equal(t, "max_turns", res.ErrorKind)
	require.Len(t, provider.TurnMsgs, 3)
}

func TestToolLoopUnchangedWithoutTools(t *testing.T) {
	provider := &llmtest.ScriptedTurns{}
	provider.Response = "plain completion"
	sink := &memSink{}
	res, err := harness.Run(context.Background(),
		harness.Input{Steps: steps(), RunToken: "tok"},
		harness.Deps{ProviderFactory: func(string) (llm.Provider, error) { return provider, nil }, Sink: sink})
	require.NoError(t, err)
	require.Equal(t, "plain completion", res.Output)
	require.Empty(t, provider.TurnMsgs, "no ChatTurn without tools")
}
