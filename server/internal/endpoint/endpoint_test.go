package endpoint_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/tomte/server/internal/endpoint"
)

func TestValidatePresetsPinFixedBases(t *testing.T) {
	for preset, want := range map[string]string{
		"anthropic":  "https://api.anthropic.com",
		"openai":     "https://api.openai.com/v1",
		"openrouter": "https://openrouter.ai/api/v1",
		"github":     "https://models.github.ai/inference",
	} {
		e, err := endpoint.Validate(endpoint.Endpoint{
			Preset: preset, BaseURL: "https://attacker.example", Connection: "default", RunModel: "m",
		})
		require.NoError(t, err, preset)
		require.Equal(t, want, e.BaseURL, "a submitted base URL never overrides a preset's")
	}
}

func TestValidateKindAndRoute(t *testing.T) {
	a, err := endpoint.Validate(endpoint.Endpoint{Preset: "anthropic", Connection: "default", RunModel: "m"})
	require.NoError(t, err)
	require.Equal(t, "anthropic", a.Kind)
	base, method, path := a.Route()
	require.Equal(t, []string{"https://api.anthropic.com", "POST", "v1/messages"}, []string{base, method, path})
	require.Equal(t, "x-api-key", a.CredentialHeader())

	g, err := endpoint.Validate(endpoint.Endpoint{Preset: "github", Connection: "default", RunModel: "m"})
	require.NoError(t, err)
	require.Equal(t, "openai_compatible", g.Kind)
	_, _, path = g.Route()
	require.Equal(t, "chat/completions", path)
	require.Equal(t, "authorization", g.CredentialHeader())
}

func TestValidateAzure(t *testing.T) {
	e, err := endpoint.Validate(endpoint.Endpoint{
		Preset: "azure", BaseURL: "https://MyCo.services.ai.azure.com/openai/v1/",
		Connection: "default", RunModel: "gpt-4o",
	})
	require.NoError(t, err)
	require.Equal(t, "https://myco.services.ai.azure.com/openai/v1", e.BaseURL)
	require.Equal(t, "api-key", e.CredentialHeader())

	for _, bad := range []string{
		"http://myco.openai.azure.com/openai/v1",          // not https
		"https://myco.example.com/openai/v1",              // wrong host suffix
		"https://myco.openai.azure.com",                   // missing /openai/v1
		"https://myco.openai.azure.com/openai/v1?x=1",     // query
		"https://user:pw@myco.openai.azure.com/openai/v1", // userinfo
	} {
		_, err := endpoint.Validate(endpoint.Endpoint{Preset: "azure", BaseURL: bad, Connection: "default", RunModel: "m"})
		require.Error(t, err, bad)
	}
}

func TestValidateCustomAndLocal(t *testing.T) {
	// Custom: https required except loopback.
	_, err := endpoint.Validate(endpoint.Endpoint{Preset: "custom", BaseURL: "http://internal.corp/v1", Connection: "default", RunModel: "m"})
	require.Error(t, err)
	c, err := endpoint.Validate(endpoint.Endpoint{Preset: "custom", BaseURL: "http://127.0.0.1:8081/v1", Connection: "default", RunModel: "m"})
	require.NoError(t, err)
	require.False(t, c.ZeroCost, "custom is never zero-cost, even on loopback")

	// A custom endpoint asking for zero-cost is forced back to priced.
	c, err = endpoint.Validate(endpoint.Endpoint{Preset: "custom", BaseURL: "https://api.other.example/v1", Connection: "default", RunModel: "m", ZeroCost: true})
	require.NoError(t, err)
	require.False(t, c.ZeroCost)

	// Local: loopback required, no connection, zero-cost forced true.
	_, err = endpoint.Validate(endpoint.Endpoint{Preset: "local", BaseURL: "https://api.other.example/v1", RunModel: "m"})
	require.Error(t, err)
	_, err = endpoint.Validate(endpoint.Endpoint{Preset: "local", BaseURL: "http://localhost:11434/v1", Connection: "default", RunModel: "m"})
	require.Error(t, err, "local carries no connection")
	l, err := endpoint.Validate(endpoint.Endpoint{Preset: "local", BaseURL: "http://localhost:11434/v1", RunModel: "m"})
	require.NoError(t, err)
	require.True(t, l.ZeroCost)
	require.Equal(t, "", l.CredentialHeader())
	require.Equal(t, "local", l.Provider())
}

func TestValidateGitHubZeroCostChoice(t *testing.T) {
	free, err := endpoint.Validate(endpoint.Endpoint{Preset: "github", Connection: "default", RunModel: "m", ZeroCost: true})
	require.NoError(t, err)
	require.True(t, free.ZeroCost)
	paid, err := endpoint.Validate(endpoint.Endpoint{Preset: "github", Connection: "default", RunModel: "m", ZeroCost: false})
	require.NoError(t, err)
	require.False(t, paid.ZeroCost)
}
