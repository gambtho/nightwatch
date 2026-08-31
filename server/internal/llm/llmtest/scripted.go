// Package llmtest provides a scripted in-memory Provider for tests.
package llmtest

import (
	"context"

	"github.com/gambtho/tomte/server/internal/llm"
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

// TurnScript is one scripted ChatTurn reply.
type TurnScript struct {
	Text       string
	ToolUses   []llm.ToolUse
	StopReason string
	Usage      llm.Usage
	Err        error
}

// ScriptedTurns is a tool-capable scripted provider: each ChatTurn call
// consumes the next TurnScript and records what it was asked.
type ScriptedTurns struct {
	Scripted
	Turns     []TurnScript
	TurnMsgs  [][]llm.Message
	TurnTools [][]llm.ToolDef
}

func (s *ScriptedTurns) ChatTurn(
	ctx context.Context,
	msgs []llm.Message,
	tools []llm.ToolDef,
	opts llm.CallOptions,
	onChunk func(llm.StreamChunk),
) (llm.TurnResult, error) {
	s.TurnMsgs = append(s.TurnMsgs, append([]llm.Message(nil), msgs...))
	s.TurnTools = append(s.TurnTools, tools)
	if len(s.Turns) == 0 {
		return llm.TurnResult{StopReason: "end_turn"}, nil
	}
	turn := s.Turns[0]
	s.Turns = s.Turns[1:]
	if turn.Err != nil {
		return llm.TurnResult{}, turn.Err
	}
	if onChunk != nil && turn.Text != "" {
		onChunk(llm.StreamChunk{Delta: turn.Text})
	}
	stop := turn.StopReason
	if stop == "" {
		if len(turn.ToolUses) > 0 {
			stop = "tool_use"
		} else {
			stop = "end_turn"
		}
	}
	return llm.TurnResult{Text: turn.Text, ToolUses: turn.ToolUses, Usage: turn.Usage, StopReason: stop}, nil
}
