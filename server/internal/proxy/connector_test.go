package proxy_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/tomte/server/internal/catalog"
	"github.com/gambtho/tomte/server/internal/permit"
	"github.com/gambtho/tomte/server/internal/proxy"
)

const fakeDef = `{
  "id": "fake",
  "name": "Fake",
  "description": "A fake connector.",
  "auth": {"provider": "fakeoauth"},
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
      "name": "get_thing",
      "description": "Read one thing.",
      "effect": "read",
      "scopes": ["things:read"],
      "args_schema": {"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false},
      "binding": {"method":"GET","host":"fake.example.com","path":"/api/things/{id}"}
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

type upstreamCapture struct {
	method, path, rawQuery string
	header                 http.Header
	body                   []byte
	status                 int
	statusOnce             int // when set, the FIRST hit returns it instead of status
	respBody               string
	location               string
	hits                   int
}

type connEnv struct {
	*env
	up      *upstreamCapture
	cat     *catalog.Catalog
	swapper *credsSwapper
}

// credsSwapper lets a test replace the credential source after route
// registration (Deps is copied at RegisterRoutes).
type credsSwapper struct{ inner proxy.CredentialSource }

func (c *credsSwapper) Credential(ctx context.Context, tenantID uuid.UUID, name, provider string) (proxy.Secret, error) {
	return c.inner.Credential(ctx, tenantID, name, provider)
}

func (e *connEnv) creds2(t *testing.T, src proxy.CredentialSource) {
	t.Helper()
	e.swapper.inner = src
}

func newConnectorEnv(t *testing.T, p permit.Permit, hook proxy.Hook) *connEnv {
	t.Helper()
	up := &upstreamCapture{status: http.StatusOK, respBody: `{"ok":true}`}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up.hits++
		up.method, up.path, up.rawQuery = r.Method, r.URL.EscapedPath(), r.URL.RawQuery
		up.header = r.Header.Clone()
		up.body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if up.location != "" {
			w.Header().Set("Location", up.location)
		}
		status := up.status
		if up.statusOnce != 0 && up.hits == 1 {
			status = up.statusOnce
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(up.respBody))
	}))
	t.Cleanup(upstream.Close)

	cat, err := catalog.ParseDefs([]byte(fakeDef))
	require.NoError(t, err)

	e := &env{
		auth:    &fakeAuth{identity: proxy.RunIdentity{}},
		permits: &fakePermits{permit: p},
		creds:   &fakeCreds{secret: proxy.Secret{Value: "oauth-access-token"}},
		events:  &fakeEvents{},
	}
	cfg := proxy.DefaultConfig()
	cfg.ConnectorUpstreams = map[string]string{"fake": upstream.URL}
	if hook == nil {
		hook = proxy.NopHook{}
	}
	swapper := &credsSwapper{inner: e.creds}
	mux := http.NewServeMux()
	proxy.RegisterRoutes(mux, proxy.Deps{
		Auth: e.auth, Permits: e.permits, Credentials: swapper,
		Events: e.events, Hook: hook, Config: cfg, Catalog: cat,
	})
	e.ts = httptest.NewServer(mux)
	t.Cleanup(e.ts.Close)
	return &connEnv{env: e, up: up, cat: cat, swapper: swapper}
}

func invoke(t *testing.T, e *connEnv, connector, op, args, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), "POST",
		e.ts.URL+"/proxy/connector/"+connector+"/"+op, strings.NewReader(args))
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	// Harness-supplied headers that must never reach the upstream.
	req.Header.Set("Cookie", "session=evil")
	req.Header.Set("X-Api-Key", "smuggled")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

const grantAll = `{"v":1,"connections":{"fake":{"kind":"http","ops":["read_things","get_thing","write_thing"],
  "resources":{"write_thing":{"box":["b-approved"]}}}}}`

func TestConnectorCompileAndInject(t *testing.T) {
	e := newConnectorEnv(t, mustPermit(t, grantAll), nil)

	resp := invoke(t, e, "fake", "read_things", `{"limit":5}`, "tok")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "GET", e.up.method)
	require.Equal(t, "/api/things", e.up.path)
	require.Equal(t, "limit=5", e.up.rawQuery)
	// Header allowlist: injected credential wins; inbound cookies and
	// credential-shaped headers are dropped.
	require.Equal(t, "Bearer oauth-access-token", e.up.header.Get("Authorization"))
	require.Empty(t, e.up.header.Get("Cookie"))
	require.Empty(t, e.up.header.Get("X-Api-Key"))
	body, _ := io.ReadAll(resp.Body)
	require.JSONEq(t, `{"ok":true}`, string(body))
	require.Contains(t, e.events.events, "proxy.request")
}

func TestConnectorWriteOpBody(t *testing.T) {
	e := newConnectorEnv(t, mustPermit(t, grantAll), nil)

	resp := invoke(t, e, "fake", "write_thing", `{"box":"b-approved","text":"hi"}`, "tok")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "POST", e.up.method)
	var sent map[string]any
	require.NoError(t, json.Unmarshal(e.up.body, &sent))
	require.Equal(t, map[string]any{"box": "b-approved", "text": "hi"}, sent)
	require.Equal(t, "application/json", e.up.header.Get("Content-Type"))
}

// The enforcement matrix, curated rows: every miss is 403 plus a
// recorded proxy.denied.
func TestConnectorEnforcementMatrix(t *testing.T) {
	cases := []struct {
		name          string
		permit        string
		connector, op string
		args          string
	}{
		{"connector not in permit", `{"v":1}`, "fake", "read_things", `{}`},
		{"op not in permit", `{"v":1,"connections":{"fake":{"kind":"http","ops":["read_things"]}}}`, "fake", "write_thing", `{"box":"b","text":"t"}`},
		{"unknown connector", grantAll, "nope", "read_things", `{}`},
		{"unknown op (removed from catalog)", `{"v":1,"connections":{"fake":{"kind":"http","ops":["gone_op"]}}}`, "fake", "gone_op", `{}`},
		{"args fail schema: unknown field", grantAll, "fake", "read_things", `{"surprise":1}`},
		{"args fail schema: wrong type", grantAll, "fake", "read_things", `{"limit":"five"}`},
		{"args fail schema: missing required", grantAll, "fake", "write_thing", `{"text":"hi"}`},
		{"constraint miss", grantAll, "fake", "write_thing", `{"box":"b-unapproved","text":"hi"}`},
		{"constraint field absent from permit resources",
			`{"v":1,"connections":{"fake":{"kind":"http","ops":["write_thing"]}}}`,
			"fake", "write_thing", `{"box":"b-approved","text":"hi"}`},
		{"traversal path arg", grantAll, "fake", "get_thing", `{"id":".."}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newConnectorEnv(t, mustPermit(t, tc.permit), nil)
			resp := invoke(t, e, tc.connector, tc.op, tc.args, "tok")
			require.Equal(t, http.StatusForbidden, resp.StatusCode)
			require.Contains(t, e.events.events, "proxy.denied")
			require.Empty(t, e.up.method, "upstream must not be reached")
		})
	}
}

func TestConnectorAuth(t *testing.T) {
	e := newConnectorEnv(t, mustPermit(t, grantAll), nil)
	resp := invoke(t, e, "fake", "read_things", `{}`, "")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	e.auth.err = errors.New("bad token")
	resp = invoke(t, e, "fake", "read_things", `{}`, "nope")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.Empty(t, e.up.method)
}

func TestConnectorPathArgEncoding(t *testing.T) {
	e := newConnectorEnv(t, mustPermit(t, grantAll), nil)
	resp := invoke(t, e, "fake", "get_thing", `{"id":"a/b?c"}`, "tok")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "/api/things/a%2Fb%3Fc", e.up.path)
}

type denyHook struct{ status int }

func (h denyHook) Before(ctx context.Context, req proxy.HookRequest) error {
	return proxy.HookError{Status: h.status, Msg: "no"}
}

func TestConnectorHookReceivesConnectorOp(t *testing.T) {
	var got proxy.HookRequest
	hook := hookFunc(func(ctx context.Context, req proxy.HookRequest) error {
		got = req
		return nil
	})
	e := newConnectorEnv(t, mustPermit(t, grantAll), hook)
	resp := invoke(t, e, "fake", "read_things", `{}`, "tok")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "fake", got.Connector)
	require.Equal(t, "read_things", got.Op)
	require.Empty(t, got.Provider)
}

func TestConnectorHookDenies(t *testing.T) {
	e := newConnectorEnv(t, mustPermit(t, grantAll), denyHook{status: http.StatusTooManyRequests})
	resp := invoke(t, e, "fake", "read_things", `{}`, "tok")
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	require.Contains(t, e.events.events, "proxy.denied")
}

func TestConnectorCredentialFailureIs500(t *testing.T) {
	e := newConnectorEnv(t, mustPermit(t, grantAll), nil)
	e.creds.err = errors.New("vault broken")
	resp := invoke(t, e, "fake", "read_things", `{}`, "tok")
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	require.Contains(t, e.events.events, "proxy.error")
}

// A 3xx from the upstream is relayed verbatim — the proxy never chases
// it with the injected credential.
func TestConnectorRedirectRelayedNotFollowed(t *testing.T) {
	e := newConnectorEnv(t, mustPermit(t, grantAll), nil)
	e.up.status = http.StatusFound
	e.up.location = "https://elsewhere.example.com/steal"

	req, err := http.NewRequestWithContext(context.Background(), "POST",
		e.ts.URL+"/proxy/connector/fake/read_things", strings.NewReader(`{}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer tok")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Equal(t, "https://elsewhere.example.com/steal", resp.Header.Get("Location"))
	// The upstream saw exactly one request: the proxy did not follow.
	require.Equal(t, 1, e.up.hits)
}

func TestConnectorOversizedArgsDenied(t *testing.T) {
	e := newConnectorEnv(t, mustPermit(t, grantAll), nil)
	big := `{"limit":` + strings.Repeat("1", 70<<10) + `}`
	resp := invoke(t, e, "fake", "read_things", big, "tok")
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	require.Empty(t, e.up.method)
}

type hookFunc func(ctx context.Context, req proxy.HookRequest) error

func (f hookFunc) Before(ctx context.Context, req proxy.HookRequest) error { return f(ctx, req) }

// brokenCreds hands out secrets whose MarkBroken reports a chosen CAS
// outcome and counts resolutions, for the upstream-401 paths.
type brokenCreds struct {
	applied  bool
	resolves int
	marks    int
}

func (b *brokenCreds) Credential(ctx context.Context, tenantID uuid.UUID, name, provider string) (proxy.Secret, error) {
	b.resolves++
	val := fmt.Sprintf("token-%d", b.resolves)
	return proxy.Secret{
		Value: val,
		MarkBroken: func(ctx context.Context) (bool, error) {
			b.marks++
			return b.applied, nil
		},
	}, nil
}

// A 401 whose CAS applies demotes once: connection.broken is recorded
// and the 401 relayed — no retry.
func TestConnectorUpstream401DemotesAndRelays(t *testing.T) {
	e := newConnectorEnv(t, mustPermit(t, grantAll), nil)
	bc := &brokenCreds{applied: true}
	e.creds2(t, bc)
	e.up.status = http.StatusUnauthorized

	resp := invoke(t, e, "fake", "read_things", `{}`, "tok")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.Equal(t, 1, bc.resolves, "no retry when the demotion applied")
	require.Equal(t, 1, bc.marks)
	require.Contains(t, e.events.events, "connection.broken")
}

// A 401 whose CAS misses means the token was stale: exactly one retry
// with a freshly resolved credential.
func TestConnectorUpstream401StaleRetriesOnce(t *testing.T) {
	e := newConnectorEnv(t, mustPermit(t, grantAll), nil)
	bc := &brokenCreds{applied: false}
	e.creds2(t, bc)

	// First upstream hit 401s, the retry succeeds.
	e.up.statusOnce = http.StatusUnauthorized

	resp := invoke(t, e, "fake", "read_things", `{}`, "tok")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, 2, bc.resolves, "one re-resolution for the retry")
	require.Equal(t, 2, e.up.hits)
	require.Equal(t, "Bearer token-2", e.up.header.Get("Authorization"))
	require.NotContains(t, e.events.events, "connection.broken")
}
