package catalog

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// def builds a minimal valid connector definition with substitutions.
func def(t *testing.T, replace ...string) []byte {
	t.Helper()
	base := `{
	  "id": "fake",
	  "name": "Fake",
	  "description": "A fake connector.",
	  "auth": {"provider": "fake"},
	  "hosts": ["fake.example.com"],
	  "ops": [
	    {
	      "name": "read_things",
	      "description": "Read things.",
	      "effect": "read",
	      "scopes": ["things:read"],
	      "args_schema": {"type":"object","properties":{"limit":{"type":"integer"}},"additionalProperties":false},
	      "binding": {"method":"GET","host":"fake.example.com","path":"/api/things","query":{"limit":"limit"}}
	    },
	    {
	      "name": "write_thing",
	      "description": "Write a thing.",
	      "effect": "write",
	      "scopes": ["things:write"],
	      "args_schema": {"type":"object","properties":{"box":{"type":"string"},"text":{"type":"string"}},"required":["box","text"],"additionalProperties":false},
	      "binding": {"method":"POST","host":"fake.example.com","path":"/api/things","body":{"box":"box","text":"text"}},
	      "constraints": [{"field":"box"}]
	    }
	  ]
	}`
	r := strings.NewReplacer(replace...)
	return []byte(r.Replace(base))
}

func TestLoadEmbeddedCatalog(t *testing.T) {
	cat, err := Load()
	require.NoError(t, err)
	ids := []string{}
	for _, c := range cat.Connectors() {
		ids = append(ids, c.ID)
	}
	require.Equal(t, []string{"slack"}, ids)

	_, op, ok := cat.Op("slack", "post_message")
	require.True(t, ok)
	require.Equal(t, EffectWrite, op.Effect)
	require.Equal(t, []Constraint{{Field: "channel"}}, op.Constraints)
}

func TestParseDefsValidation(t *testing.T) {
	cases := []struct {
		name    string
		mangle  []string
		wantErr string
	}{
		{"unknown effect", []string{`"effect": "read"`, `"effect": "admin"`}, "effect"},
		{"host not in connector hosts", []string{`"host":"fake.example.com","path":"/api/things","query"`, `"host":"other.example.com","path":"/api/things","query"`}, "host"},
		{"templated host unsupported", []string{`"hosts": ["fake.example.com"]`, `"hosts": ["{ws}.example.com"]`}, "templated host"},
		{"constraint on read op", []string{`"query":{"limit":"limit"}}`, `"query":{"limit":"limit"}},"constraints":[{"field":"limit"}]`}, "write ops"},
		{"constraint field missing from schema", []string{`"constraints": [{"field":"box"}]`, `"constraints": [{"field":"nope"}]`}, "constraint field"},
		{"dangling query arg", []string{`"query":{"limit":"limit"}`, `"query":{"limit":"limit","q":"query_text"}`}, "not in args_schema"},
		{"unplaced arg", []string{`"query":{"limit":"limit"}`, `"query":{}`}, "never placed"},
		{"bad method", []string{`"method":"GET"`, `"method":"CONNECT"`}, "method"},
		{"empty scopes", []string{`"scopes": ["things:read"]`, `"scopes": []`}, "scope"},
		{"unknown top-level field", []string{`"id": "fake"`, `"id": "fake", "surprise": 1`}, "surprise"},
		{"overlapping body paths", []string{
			`"body":{"box":"box","text":"text"}`, `"body":{"box":"box","box.inner":"text"}`,
		}, "overlap"},
		{"non-string path placeholder", []string{
			`"path":"/api/things","query":{"limit":"limit"}`,
			`"path":"/api/things/{limit}","query":{}`,
			`"args_schema": {"type":"object","properties":{"limit":{"type":"integer"}},"additionalProperties":false}`,
			`"args_schema": {"type":"object","properties":{"limit":{"type":"integer"}},"required":["limit"],"additionalProperties":false}`,
		}, "must be a string arg"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseDefs(def(t, tc.mangle...))
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestParseDefsDuplicates(t *testing.T) {
	_, err := ParseDefs(def(t), def(t))
	require.ErrorContains(t, err, "duplicate connector id")

	_, err = ParseDefs(def(t, `"name": "write_thing"`, `"name": "read_things"`))
	require.ErrorContains(t, err, "duplicate op name")
}

func TestOpLookup(t *testing.T) {
	cat, err := ParseDefs(def(t))
	require.NoError(t, err)
	_, _, ok := cat.Op("fake", "read_things")
	require.True(t, ok)
	_, _, ok = cat.Op("fake", "nope")
	require.False(t, ok)
	_, _, ok = cat.Op("nope", "read_things")
	require.False(t, ok)
}
