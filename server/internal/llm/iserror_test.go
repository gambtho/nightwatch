package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The provider contract carries tool errors: Anthropic gets
// tool_result.is_error, OpenAI-shaped APIs get the stated prefix
// convention. Without the mapping, failures would present to the model
// as successful results.

func toolErrMsgs() []Message {
	return []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "go"},
		{Role: RoleAssistant, ToolUses: []ToolUse{{ID: "t1", Name: "slack__post_message", Input: json.RawMessage(`{}`)}}},
		{Role: RoleTool, ToolUseID: "t1", Content: "denied by policy", IsError: true},
	}
}

// Two parallel tool results must land in ONE user message: the
// Messages API requires alternating roles, and both results belong to
// the immediately following user turn.
func TestAnthropicParallelToolResultsCoalesce(t *testing.T) {
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: message_start\n"+
			`data: {"type":"message_start","message":{"id":"m1","usage":{"input_tokens":3,"output_tokens":0}}}`+"\n\n"+
			"event: message_stop\n"+`data: {"type":"message_stop"}`+"\n\n")
	}))
	t.Cleanup(srv.Close)

	msgs := []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "go"},
		{Role: RoleAssistant, ToolUses: []ToolUse{
			{ID: "t1", Name: "a__x", Input: json.RawMessage(`{}`)},
			{ID: "t2", Name: "a__y", Input: json.RawMessage(`{}`)},
		}},
		{Role: RoleTool, ToolUseID: "t1", Content: "one"},
		{Role: RoleTool, ToolUseID: "t2", Content: "two", IsError: true},
	}
	tcp := NewAnthropic(srv.URL).(ToolCapableProvider)
	_, err := tcp.ChatTurn(context.Background(), msgs, nil,
		CallOptions{Model: "claude-test", MaxTokens: 64, APIKey: "k"}, func(StreamChunk) {})
	require.NoError(t, err)

	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type      string `json:"type"`
				ToolUseID string `json:"tool_use_id"`
				IsError   bool   `json:"is_error"`
			} `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(captured, &req))
	// user, assistant, user — never two users in a row.
	var roles []string
	for _, m := range req.Messages {
		roles = append(roles, m.Role)
	}
	require.Equal(t, []string{"user", "assistant", "user"}, roles)
	last := req.Messages[len(req.Messages)-1]
	require.Len(t, last.Content, 2)
	require.Equal(t, "t1", last.Content[0].ToolUseID)
	require.False(t, last.Content[0].IsError)
	require.Equal(t, "t2", last.Content[1].ToolUseID)
	require.True(t, last.Content[1].IsError)
}

func TestAnthropicToolResultCarriesIsError(t *testing.T) {
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: message_start\n"+
			`data: {"type":"message_start","message":{"id":"m1","usage":{"input_tokens":3,"output_tokens":0}}}`+"\n\n"+
			"event: message_stop\n"+`data: {"type":"message_stop"}`+"\n\n")
	}))
	t.Cleanup(srv.Close)

	p := NewAnthropic(srv.URL)
	tcp := p.(ToolCapableProvider)
	_, err := tcp.ChatTurn(context.Background(), toolErrMsgs(), nil,
		CallOptions{Model: "claude-test", MaxTokens: 64, APIKey: "k"}, func(StreamChunk) {})
	require.NoError(t, err)

	var req struct {
		Messages []struct {
			Content []struct {
				Type    string `json:"type"`
				IsError bool   `json:"is_error"`
			} `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(captured, &req))
	found := false
	for _, m := range req.Messages {
		for _, c := range m.Content {
			if c.Type == "tool_result" {
				require.True(t, c.IsError, "is_error must reach the wire")
				found = true
			}
		}
	}
	require.True(t, found, "request carries a tool_result block")
}

func TestOpenAIToolResultGetsErrorPrefix(t *testing.T) {
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)

	p := NewOpenAI(srv.URL)
	tcp := p.(ToolCapableProvider)
	_, err := tcp.ChatTurn(context.Background(), toolErrMsgs(), nil,
		CallOptions{Model: "gpt-test", MaxTokens: 64, APIKey: "k"}, func(StreamChunk) {})
	require.NoError(t, err)

	require.Contains(t, string(captured), ToolErrorPrefix+"denied by policy",
		"the prefix convention is how an OpenAI-shaped API sees the failure")
	require.False(t, strings.Contains(string(captured), `"is_error"`))
}
