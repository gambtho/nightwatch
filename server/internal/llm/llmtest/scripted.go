// Package llmtest provides a scripted in-memory Provider for tests.
package llmtest

import (
	"context"

	"github.com/gambtho/nightwatch/server/internal/llm"
)

type Scripted struct {
	Response string
	Usage    llm.Usage
	Err      error
	Calls    [][]llm.Message
}

func (s *Scripted) Chat(ctx context.Context, msgs []llm.Message, opts llm.CallOptions, onChunk func(llm.StreamChunk)) (llm.Usage, error) {
	s.Calls = append(s.Calls, msgs)
	if s.Err != nil {
		return llm.Usage{}, s.Err
	}
	if onChunk != nil {
		onChunk(llm.StreamChunk{Delta: s.Response})
	}
	return s.Usage, nil
}
