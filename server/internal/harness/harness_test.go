package harness_test

import (
	"context"
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
