package catalog

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func mustSchema(t *testing.T, raw string) *Schema {
	t.Helper()
	s, err := ParseSchema(json.RawMessage(raw))
	require.NoError(t, err)
	return s
}

func TestParseSchemaRejectsOutsideSubset(t *testing.T) {
	cases := []struct{ name, raw string }{
		{"non-object root", `{"type":"string"}`},
		{"additionalProperties missing", `{"type":"object","properties":{}}`},
		{"additionalProperties true", `{"type":"object","properties":{},"additionalProperties":true}`},
		{"unsupported keyword", `{"type":"object","properties":{},"additionalProperties":false,"patternProperties":{}}`},
		{"unsupported property type", `{"type":"object","properties":{"x":{"type":"array"}},"additionalProperties":false}`},
		{"nested object property", `{"type":"object","properties":{"x":{"type":"object"}},"additionalProperties":false}`},
		{"enum on integer", `{"type":"object","properties":{"x":{"type":"integer","enum":["1"]}},"additionalProperties":false}`},
		{"required names unknown", `{"type":"object","properties":{},"required":["x"],"additionalProperties":false}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseSchema(json.RawMessage(tc.raw))
			require.Error(t, err)
		})
	}
}

func TestValidateArgs(t *testing.T) {
	s := mustSchema(t, `{
	  "type":"object",
	  "properties":{
	    "channel":{"type":"string"},
	    "limit":{"type":"integer"},
	    "flag":{"type":"boolean"},
	    "mode":{"type":"string","enum":["a","b"]}
	  },
	  "required":["channel"],
	  "additionalProperties":false
	}`)

	ok := func(raw string) map[string]any {
		args, err := s.Validate(json.RawMessage(raw))
		require.NoError(t, err)
		return args
	}
	bad := func(raw, want string) {
		_, err := s.Validate(json.RawMessage(raw))
		require.ErrorContains(t, err, want)
	}

	args := ok(`{"channel":"C1","limit":5,"flag":true,"mode":"a"}`)
	require.Equal(t, "C1", args["channel"])

	ok(`{"channel":"C1"}`) // optionals omitted

	bad(`[]`, "JSON object")
	bad(`{"channel":"C1","extra":1}`, "unknown arg")
	bad(`{"limit":5}`, "missing required")
	bad(`{"channel":7}`, "must be a string")
	bad(`{"channel":"C1","limit":1.5}`, "must be an integer")
	bad(`{"channel":"C1","flag":"yes"}`, "must be a boolean")
	bad(`{"channel":"C1","mode":"z"}`, "not an allowed value")
}
