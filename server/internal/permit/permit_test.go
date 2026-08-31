package permit_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/tomte/server/internal/permit"
)

func TestParseValid(t *testing.T) {
	p, err := permit.Parse([]byte(`{"v":1,"llm":{"providers":["anthropic","openai"],"connection":"work"},"connections":{}}`))
	require.NoError(t, err)
	require.True(t, p.AllowsProvider("anthropic"))
	require.False(t, p.AllowsProvider("openrouter"))
	require.Equal(t, "work", p.LLM.Connection)
}

func TestParseDefaultsAndDenyAll(t *testing.T) {
	// The canonical empty permit: valid, denies all egress, connection default.
	p, err := permit.Parse(permit.Empty)
	require.NoError(t, err)
	require.False(t, p.AllowsProvider("anthropic"))
	require.Equal(t, "default", p.LLM.Connection)
}

func TestParseRejects(t *testing.T) {
	for name, raw := range map[string]string{
		"missing v":           `{}`,
		"wrong v":             `{"v":2}`,
		"kindless connection": `{"v":1,"connections":{"zendesk":{}}}`,
		"unknown field":       `{"v":1,"blast_radius":true}`,
		"not json":            `nope`,
		"empty provider name": `{"v":1,"llm":{"providers":[""]}}`,
		"trailing garbage":    `{"v":1}garbage`,
	} {
		_, err := permit.Parse([]byte(raw))
		require.Error(t, err, name)
	}
}

func TestParseConnections(t *testing.T) {
	p, err := permit.Parse([]byte(`{"v":1,"llm":{"providers":["anthropic"]},"connections":{
	  "slack":{"kind":"http","ops":["list_channels","post_message"],
	           "resources":{"post_message":{"channel":["C0123ABC","C0456DEF"]}}}
	}}`))
	require.NoError(t, err)

	res, ok := p.AllowsOp("slack", "post_message")
	require.True(t, ok)
	require.Equal(t, map[string][]string{"channel": {"C0123ABC", "C0456DEF"}}, res)

	res, ok = p.AllowsOp("slack", "list_channels")
	require.True(t, ok)
	require.Nil(t, res)

	_, ok = p.AllowsOp("slack", "read_messages")
	require.False(t, ok)
	_, ok = p.AllowsOp("google-gmail", "list_messages")
	require.False(t, ok)

	// The credential name defaults per entry.
	require.Equal(t, "default", p.Connections["slack"].Connection)
}

// Approved permits from before connections existed (connections absent
// or empty) parse unchanged.
func TestParseConnectionsBackcompat(t *testing.T) {
	for _, raw := range []string{
		`{"v":1,"llm":{"providers":["anthropic"]}}`,
		`{"v":1,"llm":{"providers":["anthropic"]},"connections":{}}`,
	} {
		p, err := permit.Parse([]byte(raw))
		require.NoError(t, err)
		require.Empty(t, p.Connections)
	}
}

func TestParseConnectionsRejects(t *testing.T) {
	for name, raw := range map[string]string{
		"remote mcp not yet supported": `{"v":1,"connections":{"mcp:7f3a2c10-0000-0000-0000-000000000000":{"kind":"remote_mcp","ops":["t"]}}}`,
		"unknown kind":                 `{"v":1,"connections":{"slack":{"kind":"grpc","ops":["a"]}}}`,
		"empty ops":                    `{"v":1,"connections":{"slack":{"kind":"http","ops":[]}}}`,
		"empty op name":                `{"v":1,"connections":{"slack":{"kind":"http","ops":[""]}}}`,
		"resources name unlisted op":   `{"v":1,"connections":{"slack":{"kind":"http","ops":["a"],"resources":{"b":{"channel":["C1"]}}}}}`,
		"empty resource list":          `{"v":1,"connections":{"slack":{"kind":"http","ops":["a"],"resources":{"a":{"channel":[]}}}}}`,
		"empty resource value":         `{"v":1,"connections":{"slack":{"kind":"http","ops":["a"],"resources":{"a":{"channel":[""]}}}}}`,
		"unknown entry field":          `{"v":1,"connections":{"slack":{"kind":"http","ops":["a"],"tools":["x"]}}}`,
	} {
		_, err := permit.Parse([]byte(raw))
		require.Error(t, err, name)
	}
}

func TestParseSpend(t *testing.T) {
	p, err := permit.Parse([]byte(`{"v":1,"llm":{"providers":["anthropic"]},"spend":{"per_run_cents":50}}`))
	require.NoError(t, err)
	require.NotNil(t, p.Spend)
	require.Equal(t, 50, p.Spend.PerRunCents)

	p, err = permit.Parse(permit.Empty)
	require.NoError(t, err)
	require.Nil(t, p.Spend)

	for name, raw := range map[string]string{
		"zero cap":     `{"v":1,"spend":{"per_run_cents":0}}`,
		"negative cap": `{"v":1,"spend":{"per_run_cents":-5}}`,
		"unknown key":  `{"v":1,"spend":{"per_run_dollars":1}}`,
	} {
		_, err := permit.Parse([]byte(raw))
		require.Error(t, err, name)
	}
}
