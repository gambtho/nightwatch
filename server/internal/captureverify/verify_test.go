package captureverify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gambtho/tomte/server/internal/catalog"
	"github.com/stretchr/testify/require"
)

// fakeConnector returns a validated one-connector catalog whose capture
// guide declares a verify op, plus ops with scopes so the scope warning
// has a union to check against.
func fakeConnector(t *testing.T) *catalog.Connector {
	t.Helper()
	def := []byte(`{
	  "id": "fake",
	  "name": "Fake",
	  "description": "A fake connector.",
	  "auth": {
	    "provider": "fake",
	    "capture": {
	      "steps": ["Paste the token."],
	      "secret_prefix": "xf-",
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
	      "name": "write_thing",
	      "description": "Write a thing.",
	      "effect": "write",
	      "scopes": ["things:write"],
	      "args_schema": {"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false},
	      "binding": {"method":"POST","host":"fake.example.com","path":"/api/things","body":{"text":"text"}}
	    },
	    {
	      "name": "auth_test",
	      "description": "Check the token.",
	      "effect": "read",
	      "scopes": [],
	      "args_schema": {"type":"object","additionalProperties":false},
	      "binding": {"method":"POST","host":"fake.example.com","path":"/api/auth.test"}
	    }
	  ]
	}`)
	cat, err := catalog.ParseDefs(def)
	require.NoError(t, err)
	con, ok := cat.Connector("fake")
	require.True(t, ok)
	return con
}

type upstream struct {
	status  int
	body    map[string]any
	scopes  string // x-oauth-scopes response header, "" = absent
	headers http.Header
	hits    int
}

func newUpstream(t *testing.T, u *upstream) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.hits++
		u.headers = r.Header.Clone()
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/api/auth.test", r.URL.Path)
		if u.scopes != "" {
			w.Header().Set("x-oauth-scopes", u.scopes)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(u.status)
		_ = json.NewEncoder(w).Encode(u.body)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func verifyAgainst(t *testing.T, u *upstream, secret string) Result {
	t.Helper()
	ts := newUpstream(t, u)
	c := &Client{Upstreams: map[string]string{"fake": ts.URL}}
	res, err := c.Verify(context.Background(), fakeConnector(t), secret)
	require.NoError(t, err)
	return res
}

func TestVerifyOKStoresNothingMissing(t *testing.T) {
	u := &upstream{status: 200, body: map[string]any{"ok": true},
		scopes: "things:read,things:write"}
	res := verifyAgainst(t, u, "xf-good")
	require.True(t, res.OK)
	require.Empty(t, res.MissingScopes)
	// The request carried the candidate secret as a bearer token and the
	// proxy's header conventions — nothing else.
	require.Equal(t, "Bearer xf-good", u.headers.Get("Authorization"))
	require.Equal(t, "application/json", u.headers.Get("Accept"))
}

func TestVerifyRejects200WithOkFalse(t *testing.T) {
	// Slack's Web API convention: HTTP 200 with ok:false for a bad
	// token. A status-only check would store garbage.
	u := &upstream{status: 200, body: map[string]any{"ok": false, "error": "invalid_auth"}}
	res := verifyAgainst(t, u, "xf-bad")
	require.False(t, res.OK)
	require.Contains(t, res.Message, "token")
}

func TestVerifyRejectsNon2xx(t *testing.T) {
	u := &upstream{status: 500, body: map[string]any{}}
	res := verifyAgainst(t, u, "xf-any")
	require.False(t, res.OK)
	require.NotEmpty(t, res.Message)
}

func TestVerifyReportsMissingScopes(t *testing.T) {
	u := &upstream{status: 200, body: map[string]any{"ok": true},
		scopes: "things:read, other:scope"}
	res := verifyAgainst(t, u, "xf-good")
	require.True(t, res.OK, "missing scopes warn, they do not fail")
	require.Equal(t, []string{"things:write"}, res.MissingScopes)
}

func TestVerifyNoScopeHeaderNoWarning(t *testing.T) {
	u := &upstream{status: 200, body: map[string]any{"ok": true}}
	res := verifyAgainst(t, u, "xf-good")
	require.True(t, res.OK)
	require.Nil(t, res.MissingScopes)
}

func TestVerifyPrefixMismatchNeverCallsUpstream(t *testing.T) {
	u := &upstream{status: 200, body: map[string]any{"ok": true}}
	ts := newUpstream(t, u)
	c := &Client{Upstreams: map[string]string{"fake": ts.URL}}
	res, err := c.Verify(context.Background(), fakeConnector(t), "sk-wrong-kind-of-key")
	require.NoError(t, err)
	require.False(t, res.OK)
	require.Contains(t, res.Message, "xf-")
	require.Zero(t, u.hits)
}

func TestVerifyRefusesRedirects(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://elsewhere.example.com/", http.StatusFound)
	}))
	t.Cleanup(ts.Close)
	c := &Client{Upstreams: map[string]string{"fake": ts.URL}}
	res, err := c.Verify(context.Background(), fakeConnector(t), "xf-good")
	require.NoError(t, err)
	require.False(t, res.OK)
}

func TestVerifyUnreachableUpstream(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := ts.URL
	ts.Close()
	c := &Client{Upstreams: map[string]string{"fake": url}}
	res, err := c.Verify(context.Background(), fakeConnector(t), "xf-good")
	require.NoError(t, err)
	require.False(t, res.OK)
	require.NotEmpty(t, res.Message)
}

func TestVerifyWithoutGuideIsAnAuthoringError(t *testing.T) {
	def := []byte(`{
	  "id": "bare", "name": "Bare", "description": "No capture.",
	  "auth": {"provider": "bare"},
	  "hosts": ["bare.example.com"],
	  "ops": [{
	    "name": "read_things", "description": "Read.", "effect": "read",
	    "scopes": ["r"],
	    "args_schema": {"type":"object","additionalProperties":false},
	    "binding": {"method":"GET","host":"bare.example.com","path":"/api/things"}
	  }]
	}`)
	cat, err := catalog.ParseDefs(def)
	require.NoError(t, err)
	con, _ := cat.Connector("bare")
	c := &Client{}
	_, err = c.Verify(context.Background(), con, "anything")
	require.Error(t, err)
}
