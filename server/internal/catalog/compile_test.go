package catalog

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func fixtureOp(t *testing.T) (*Op, *Op, *Op) {
	t.Helper()
	cat, err := ParseDefs([]byte(`{
	  "id": "fake",
	  "name": "Fake",
	  "description": "A fake connector.",
	  "auth": {"provider": "fake"},
	  "hosts": ["fake.example.com"],
	  "ops": [
	    {
	      "name": "get_item",
	      "description": "Read one item.",
	      "effect": "read",
	      "scopes": ["r"],
	      "args_schema": {"type":"object","properties":{"id":{"type":"string"},"limit":{"type":"integer"}},"required":["id"],"additionalProperties":false},
	      "binding": {"method":"GET","host":"fake.example.com","path":"/api/items/{id}","query":{"limit":"limit"}}
	    },
	    {
	      "name": "create_item",
	      "description": "Create an item.",
	      "effect": "write",
	      "scopes": ["w"],
	      "args_schema": {"type":"object","properties":{"box":{"type":"string"},"title":{"type":"string"},"when":{"type":"string"}},"required":["box","title"],"additionalProperties":false},
	      "binding": {"method":"POST","host":"fake.example.com","path":"/api/boxes/{box}/items","body":{"title":"title","start.dateTime":"when"}},
	      "constraints": [{"field":"box"}]
	    },
	    {
	      "name": "list_items",
	      "description": "List items.",
	      "effect": "read",
	      "scopes": ["r"],
	      "args_schema": {"type":"object","properties":{"q":{"type":"string"}},"additionalProperties":false},
	      "binding": {"method":"GET","host":"fake.example.com","path":"/api/items","query":{"q":"q"}}
	    }
	  ]
	}`))
	require.NoError(t, err)
	_, get, _ := cat.Op("fake", "get_item")
	_, create, _ := cat.Op("fake", "create_item")
	_, list, _ := cat.Op("fake", "list_items")
	return get, create, list
}

func validated(t *testing.T, op *Op, raw string) map[string]any {
	t.Helper()
	args, err := op.Schema().Validate(json.RawMessage(raw))
	require.NoError(t, err)
	return args
}

func TestCompilePathQueryBody(t *testing.T) {
	get, create, _ := fixtureOp(t)

	cr, err := Compile(get, validated(t, get, `{"id":"abc","limit":3}`))
	require.NoError(t, err)
	require.Equal(t, "GET", cr.Method)
	require.Equal(t, "https://fake.example.com/api/items/abc?limit=3", cr.URL)
	require.Nil(t, cr.Body)

	cr, err = Compile(create, validated(t, create, `{"box":"b1","title":"hello","when":"2026-09-01T10:00:00Z"}`))
	require.NoError(t, err)
	require.Equal(t, "https://fake.example.com/api/boxes/b1/items", cr.URL)
	var body map[string]any
	require.NoError(t, json.Unmarshal(cr.Body, &body))
	require.Equal(t, "hello", body["title"])
	require.Equal(t, map[string]any{"dateTime": "2026-09-01T10:00:00Z"}, body["start"])
}

func TestCompileOmitsUnsuppliedOptionals(t *testing.T) {
	get, create, _ := fixtureOp(t)

	cr, err := Compile(get, validated(t, get, `{"id":"abc"}`))
	require.NoError(t, err)
	require.Equal(t, "https://fake.example.com/api/items/abc", cr.URL)

	cr, err = Compile(create, validated(t, create, `{"box":"b1","title":"hi"}`))
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, json.Unmarshal(cr.Body, &body))
	_, hasStart := body["start"]
	require.False(t, hasStart)
}

// Compilation hygiene: path args containing separator or traversal
// characters arrive component-encoded or rejected — never as extra path
// segments.
func TestCompilePathArgHygiene(t *testing.T) {
	get, _, _ := fixtureOp(t)

	for arg, wantURL := range map[string]string{
		"a/b":     "https://fake.example.com/api/items/a%2Fb",
		"a?x=1":   "https://fake.example.com/api/items/a%3Fx=1",
		"a#frag":  "https://fake.example.com/api/items/a%23frag",
		"...":     "https://fake.example.com/api/items/...",
		"a..b":    "https://fake.example.com/api/items/a..b",
		"%2e%2e ": "https://fake.example.com/api/items/%252e%252e%20",
	} {
		cr, err := Compile(get, validated(t, get, `{"id":`+mustJSON(arg)+`}`))
		require.NoError(t, err, arg)
		require.Equal(t, wantURL, cr.URL, arg)
	}

	for _, arg := range []string{".", "..", "%2e%2e", "%2E%2E", "%2e", ""} {
		_, err := Compile(get, map[string]any{"id": arg})
		require.Error(t, err, arg)
	}
}

func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
