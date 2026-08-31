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
