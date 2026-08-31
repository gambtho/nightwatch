package permit_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/permit"
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
		"missing v":            `{}`,
		"wrong v":              `{"v":2}`,
		"nonempty connections": `{"v":1,"connections":{"zendesk":{}}}`,
		"unknown field":        `{"v":1,"blast_radius":true}`,
		"not json":             `nope`,
		"empty provider name":  `{"v":1,"llm":{"providers":[""]}}`,
		"trailing garbage":     `{"v":1}garbage`,
	} {
		_, err := permit.Parse([]byte(raw))
		require.Error(t, err, name)
	}
}
