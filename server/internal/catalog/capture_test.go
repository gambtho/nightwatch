package catalog

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// captureDef builds a connector whose auth block carries a capture guide
// and whose ops include a scope-less verify op, with substitutions.
func captureDef(t *testing.T, replace ...string) []byte {
	t.Helper()
	base := `{
	  "id": "fake",
	  "name": "Fake",
	  "description": "A fake connector.",
	  "auth": {
	    "provider": "fake",
	    "capture": {
	      "start_url": "https://fake.example.com/apps",
	      "start_label": "Create the app",
	      "steps": ["Click Create.", "Copy the token and paste it below."],
	      "secret_prefix": "xf-",
	      "placeholder": "xf-…",
	      "verify_op": "auth_test"
	    }
	  },
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
	      "name": "auth_test",
	      "description": "Check that the pasted token works.",
	      "effect": "read",
	      "scopes": [],
	      "args_schema": {"type":"object","additionalProperties":false},
	      "binding": {"method":"POST","host":"fake.example.com","path":"/api/auth.test"}
	    }
	  ]
	}`
	r := strings.NewReplacer(replace...)
	return []byte(r.Replace(base))
}

func TestCaptureGuideParses(t *testing.T) {
	cat, err := ParseDefs(captureDef(t))
	require.NoError(t, err)
	con, ok := cat.Connector("fake")
	require.True(t, ok)
	cap := con.Auth.Capture
	require.NotNil(t, cap)
	require.Equal(t, "https://fake.example.com/apps", cap.StartURL)
	require.Equal(t, "Create the app", cap.StartLabel)
	require.Len(t, cap.Steps, 2)
	require.Equal(t, "xf-", cap.SecretPrefix)
	require.Equal(t, "xf-…", cap.Placeholder)
	require.Equal(t, "auth_test", cap.VerifyOp)
}

func TestCaptureVerifyOpMustExist(t *testing.T) {
	_, err := ParseDefs(captureDef(t, `"verify_op": "auth_test"`, `"verify_op": "no_such_op"`))
	require.ErrorContains(t, err, "verify_op")
}

func TestCaptureVerifyOpMustBeRead(t *testing.T) {
	_, err := ParseDefs(captureDef(t,
		`"effect": "read",
	      "scopes": [],`,
		`"effect": "write",
	      "scopes": [],`))
	require.ErrorContains(t, err, "verify_op")
}

func TestCaptureStepsRequired(t *testing.T) {
	_, err := ParseDefs(captureDef(t,
		`"steps": ["Click Create.", "Copy the token and paste it below."],`,
		`"steps": [],`))
	require.ErrorContains(t, err, "steps")
}

// The scope requirement is exempted for exactly the declared verify op —
// a token check needs no scopes, but every other op still declares its
// reach.
func TestScopelessOpOnlyLegalAsVerifyOp(t *testing.T) {
	// Scope-less non-verify op stays an error.
	_, err := ParseDefs(def(t, `"scopes": ["things:read"],`, `"scopes": [],`))
	require.ErrorContains(t, err, "scope")

	// The same scope-less op without the capture declaration is an error.
	_, err = ParseDefs(captureDef(t, `"verify_op": "auth_test"`, `"verify_op": ""`))
	require.ErrorContains(t, err, "scope")
}

func TestEmbeddedSlackCaptureGuide(t *testing.T) {
	cat, err := Load()
	require.NoError(t, err)
	con, verify, ok := cat.Op("slack", "auth_test")
	require.True(t, ok, "slack must ship an auth_test verify op")
	require.Equal(t, EffectRead, verify.Effect)
	require.Empty(t, verify.Scopes)

	cap := con.Auth.Capture
	require.NotNil(t, cap)
	require.Equal(t, "auth_test", cap.VerifyOp)
	require.Equal(t, "xoxb-", cap.SecretPrefix)
	require.NotEmpty(t, cap.Steps)
	// Plain-create flow only: the manifest_url pre-fill has no hosted
	// home yet (board open question) — the guide must not claim it.
	require.NotContains(t, cap.StartURL, "manifest")
	for _, s := range cap.Steps {
		require.NotContains(t, s, "pre-filled")
	}
}

func TestCaptureVerifyOpMustTakeNoRequiredArgs(t *testing.T) {
	// Verify compiles the op with {}; a verify op requiring args is an
	// authoring mistake that must fail at load, not 500 at paste time.
	_, err := ParseDefs(captureDef(t,
		`"args_schema": {"type":"object","additionalProperties":false},
	      "binding": {"method":"POST","host":"fake.example.com","path":"/api/auth.test"}`,
		`"args_schema": {"type":"object","properties":{"team":{"type":"string"}},"required":["team"],"additionalProperties":false},
	      "binding": {"method":"POST","host":"fake.example.com","path":"/api/auth.test","query":{"team":"team"}}`))
	require.ErrorContains(t, err, "verify_op")
}

func TestCaptureStepsMustBeNonEmpty(t *testing.T) {
	_, err := ParseDefs(captureDef(t,
		`"steps": ["Click Create.", "Copy the token and paste it below."],`,
		`"steps": ["Click Create.", ""],`))
	require.ErrorContains(t, err, "steps")
}

func TestCaptureStartURLMustBeHTTPS(t *testing.T) {
	_, err := ParseDefs(captureDef(t,
		`"start_url": "https://fake.example.com/apps",`,
		`"start_url": "javascript:alert(1)",`))
	require.ErrorContains(t, err, "start_url")
}

// One capture card per credential namespace: connectors share a provider
// so one pasted token covers them all — two cards for one token is an
// authoring ambiguity, caught at load.
func TestOneCaptureGuidePerProvider(t *testing.T) {
	second := captureDef(t, `"id": "fake"`, `"id": "fake-two"`)
	_, err := ParseDefs(captureDef(t), second)
	require.ErrorContains(t, err, "capture")
}
