package proxy_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/permit"
	"github.com/gambtho/nightwatch/server/internal/proxy"
)

type fakeAuth struct {
	identity proxy.RunIdentity
	err      error
	sawToken string
}

func (f *fakeAuth) VerifyRunToken(ctx context.Context, bearer string) (proxy.RunIdentity, error) {
	f.sawToken = bearer
	return f.identity, f.err
}

type fakePermits struct {
	permit permit.Permit
	err    error
	calls  int
}

func (f *fakePermits) PermitForRun(ctx context.Context, tenantID, runID uuid.UUID) (permit.Permit, error) {
	f.calls++
	return f.permit, f.err
}

type fakeCreds struct {
	secret proxy.Secret
	err    error
}

func (f *fakeCreds) Credential(ctx context.Context, tenantID uuid.UUID, name, provider string) (proxy.Secret, error) {
	return f.secret, f.err
}

type fakeEvents struct {
	mu     sync.Mutex
	events []string
}

func (f *fakeEvents) AppendEvent(ctx context.Context, tenantID, runID uuid.UUID, typ string, payload map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, typ)
	return nil
}

func mustPermit(t *testing.T, raw string) permit.Permit {
	t.Helper()
	p, err := permit.Parse([]byte(raw))
	require.NoError(t, err)
	return p
}

type env struct {
	ts      *httptest.Server
	auth    *fakeAuth
	permits *fakePermits
	creds   *fakeCreds
	events  *fakeEvents
}

func newEnv(t *testing.T, upstream string, p permit.Permit) *env {
	t.Helper()
	e := &env{
		auth:    &fakeAuth{identity: proxy.RunIdentity{TenantID: uuid.New(), RunID: uuid.New()}},
		permits: &fakePermits{permit: p},
		creds:   &fakeCreds{secret: proxy.Secret{Value: "real-key"}},
		events:  &fakeEvents{},
	}
	cfg := proxy.DefaultConfig()
	if upstream != "" {
		for name, route := range cfg.Providers {
			route.Base = upstream
			cfg.Providers[name] = route
		}
	}
	mux := http.NewServeMux()
	proxy.RegisterRoutes(mux, proxy.Deps{
		Auth: e.auth, Permits: e.permits, Credentials: e.creds,
		Events: e.events, Hook: proxy.NopHook{}, Config: cfg,
	})
	e.ts = httptest.NewServer(mux)
	t.Cleanup(e.ts.Close)
	return e
}

func doAnthropic(t *testing.T, e *env, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), "POST",
		e.ts.URL+"/proxy/llm/anthropic/v1/messages", nil)
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("x-api-key", token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestDeniedProviderIs403WithEvent(t *testing.T) {
	e := newEnv(t, "", mustPermit(t, `{"v":1,"llm":{"providers":["openai"]}}`))
	resp := doAnthropic(t, e, "run-token")
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	require.Contains(t, e.events.events, "proxy.denied")
	require.Equal(t, "run-token", e.auth.sawToken)
}

func TestMissingOrBadTokenIs401(t *testing.T) {
	e := newEnv(t, "", mustPermit(t, `{"v":1,"llm":{"providers":["anthropic"]}}`))
	resp := doAnthropic(t, e, "")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	e.auth.err = errors.New("bad token")
	resp = doAnthropic(t, e, "nope")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestOpenAITokenRidesAuthorizationHeader(t *testing.T) {
	e := newEnv(t, "", mustPermit(t, `{"v":1,"llm":{"providers":[]}}`))
	// SDK-faithful path: openai-go's base already contains /v1, so the SDK
	// emits /chat/completions relative to it.
	req, err := http.NewRequestWithContext(context.Background(), "POST",
		e.ts.URL+"/proxy/llm/openai/chat/completions", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer run-token-xyz")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode) // authed, then denied (deny-all permit)
	require.Equal(t, "run-token-xyz", e.auth.sawToken)
}

func TestUnknownProviderIs403(t *testing.T) {
	e := newEnv(t, "", mustPermit(t, `{"v":1,"llm":{"providers":["anthropic"]}}`))
	req, err := http.NewRequestWithContext(context.Background(), "POST",
		e.ts.URL+"/proxy/llm/copilot/v1/x", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestAllowedProviderDisallowedPathIs403(t *testing.T) {
	// The provider is permitted, but v1 allows exactly one (method, path)
	// per provider — anything else on the origin is outside the blast radius.
	e := newEnv(t, "", mustPermit(t, `{"v":1,"llm":{"providers":["anthropic"]}}`))

	req, err := http.NewRequestWithContext(context.Background(), "POST",
		e.ts.URL+"/proxy/llm/anthropic/v1/files", nil)
	require.NoError(t, err)
	req.Header.Set("x-api-key", "tok")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	// Wrong method on the allowed path is denied too.
	req, err = http.NewRequestWithContext(context.Background(), "GET",
		e.ts.URL+"/proxy/llm/anthropic/v1/messages", nil)
	require.NoError(t, err)
	req.Header.Set("x-api-key", "tok")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	require.Contains(t, e.events.events, "proxy.denied")
}

func TestPermitSourceFailureFailsClosed(t *testing.T) {
	e := newEnv(t, "", permit.Permit{})
	e.permits.err = errors.New("db down")
	resp := doAnthropic(t, e, "tok")
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}
