package steps_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/steps"
)

func TestParseValid(t *testing.T) {
	doc, err := steps.Parse([]byte(`{"v":1,"steps":[
		{"id":"gather","text":"Look at last week's support tickets."},
		{"id":"post-digest","text":"Post a short digest in #team-digest."}
	]}`))
	require.NoError(t, err)
	require.Equal(t, 1, doc.V)
	require.Len(t, doc.Steps, 2)
	require.Equal(t, "gather", doc.Steps[0].ID)
	require.Equal(t, "post-digest", doc.Steps[1].ID)
}

func TestParseRejects(t *testing.T) {
	long := strings.Repeat("x", 501)
	longID := strings.Repeat("a", 65)
	for name, raw := range map[string]string{
		"missing v":        `{"steps":[{"id":"a","text":"b"}]}`,
		"wrong v":          `{"v":2,"steps":[{"id":"a","text":"b"}]}`,
		"no steps":         `{"v":1}`,
		"empty steps":      `{"v":1,"steps":[]}`,
		"not json":         `nope`,
		"trailing garbage": `{"v":1,"steps":[{"id":"a","text":"b"}]}garbage`,
		"unknown field":    `{"v":1,"steps":[{"id":"a","text":"b"}],"model":"gpt-4o"}`,
		// Execution fields are the old compiled form; a steps document
		// carrying them is rejected, not ignored.
		"system_prompt":      `{"v":1,"system_prompt":"you are","steps":[{"id":"a","text":"b"}]}`,
		"provider":           `{"v":1,"provider":"anthropic","steps":[{"id":"a","text":"b"}]}`,
		"max_tokens":         `{"v":1,"max_tokens":100,"steps":[{"id":"a","text":"b"}]}`,
		"unknown step field": `{"v":1,"steps":[{"id":"a","text":"b","model":"x"}]}`,
		"empty text":         `{"v":1,"steps":[{"id":"a","text":""}]}`,
		"long text":          `{"v":1,"steps":[{"id":"a","text":"` + long + `"}]}`,
		"empty id":           `{"v":1,"steps":[{"id":"","text":"b"}]}`,
		"long id":            `{"v":1,"steps":[{"id":"` + longID + `","text":"b"}]}`,
		"uppercase id":       `{"v":1,"steps":[{"id":"Gather","text":"b"}]}`,
		"underscore id":      `{"v":1,"steps":[{"id":"a_b","text":"b"}]}`,
		"leading hyphen id":  `{"v":1,"steps":[{"id":"-a","text":"b"}]}`,
		"trailing hyphen id": `{"v":1,"steps":[{"id":"a-","text":"b"}]}`,
		"double hyphen id":   `{"v":1,"steps":[{"id":"a--b","text":"b"}]}`,
		"duplicate id":       `{"v":1,"steps":[{"id":"a","text":"b"},{"id":"a","text":"c"}]}`,
		"space id":           `{"v":1,"steps":[{"id":"a b","text":"b"}]}`,
	} {
		_, err := steps.Parse([]byte(raw))
		require.Error(t, err, name)
	}
}

func TestParseStepCountBounds(t *testing.T) {
	mk := func(n int) []byte {
		var b strings.Builder
		b.WriteString(`{"v":1,"steps":[`)
		for i := 0; i < n; i++ {
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `{"id":"step-%d","text":"do it"}`, i)
		}
		b.WriteString(`]}`)
		return []byte(b.String())
	}
	_, err := steps.Parse(mk(20))
	require.NoError(t, err)
	_, err = steps.Parse(mk(21))
	require.Error(t, err)
}

func TestCompileDeterministic(t *testing.T) {
	doc, err := steps.Parse([]byte(`{"v":1,"steps":[
		{"id":"gather","text":"Look at tickets."},
		{"id":"post","text":"Post a digest."}
	]}`))
	require.NoError(t, err)

	c := steps.Compile(doc, json.RawMessage(`{"cites-sources":"every claim links a ticket"}`),
		steps.Platform{Provider: "anthropic", Model: "claude-haiku-4-5", MaxTokens: 4096})
	again := steps.Compile(doc, json.RawMessage(`{"cites-sources":"every claim links a ticket"}`),
		steps.Platform{Provider: "anthropic", Model: "claude-haiku-4-5", MaxTokens: 4096})
	require.Equal(t, c, again, "compiler must be deterministic")

	require.Equal(t, steps.CompilerV, c.CompilerV)
	require.Equal(t, "anthropic", c.Provider)
	require.Equal(t, "claude-haiku-4-5", c.Model)
	require.Equal(t, 4096, c.MaxTokens)
	require.NotEmpty(t, c.Kickoff)

	// The approved text is byte-identical inside what runs.
	require.Contains(t, c.SystemPrompt, "1. Look at tickets.")
	require.Contains(t, c.SystemPrompt, "2. Post a digest.")
	require.Contains(t, c.SystemPrompt, "every claim links a ticket")
}

func TestCompileEmptyRubricOmitted(t *testing.T) {
	doc, err := steps.Parse([]byte(`{"v":1,"steps":[{"id":"a","text":"b"}]}`))
	require.NoError(t, err)
	c := steps.Compile(doc, json.RawMessage(`{}`), steps.Platform{Provider: "p", Model: "m", MaxTokens: 1})
	require.NotContains(t, c.SystemPrompt, "rubric")
	require.NotContains(t, c.SystemPrompt, "Rubric")

	cNil := steps.Compile(doc, nil, steps.Platform{Provider: "p", Model: "m", MaxTokens: 1})
	require.Equal(t, c.SystemPrompt, cNil.SystemPrompt)
}

func TestCompiledRoundTripsAsHarnessSteps(t *testing.T) {
	doc, err := steps.Parse([]byte(`{"v":1,"steps":[{"id":"a","text":"b"}]}`))
	require.NoError(t, err)
	c := steps.Compile(doc, nil, steps.Platform{Provider: "anthropic", Model: "m", MaxTokens: 7})
	raw, err := json.Marshal(c)
	require.NoError(t, err)
	// The harness contract's field names must not change shape.
	var wire map[string]any
	require.NoError(t, json.Unmarshal(raw, &wire))
	for _, k := range []string{"system_prompt", "kickoff", "provider", "model", "max_tokens", "compiler_v"} {
		require.Contains(t, wire, k)
	}
}
