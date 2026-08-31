package proxy_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

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
	require.Contains(t, e.events.events, "proxy.denied")
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

func TestForwardInjectsCredentialAndStrips(t *testing.T) {
	var gotAPIKey, gotAuthz string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("x-api-key")
		gotAuthz = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(upstream.Close)

	e := newEnv(t, upstream.URL, mustPermit(t, `{"v":1,"llm":{"providers":["anthropic"]}}`))
	resp := doAnthropic(t, e, "run-token")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "real-key", gotAPIKey) // injected
	require.Empty(t, gotAuthz)              // nothing else leaks upstream
	require.Contains(t, e.events.events, "proxy.request")
}

func TestForwardOpenAIUsesBearerSlot(t *testing.T) {
	var gotAuthz, gotAPIKey string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthz = r.Header.Get("Authorization")
		gotAPIKey = r.Header.Get("x-api-key")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	e := newEnv(t, upstream.URL, mustPermit(t, `{"v":1,"llm":{"providers":["openai"]}}`))
	// SDK-faithful path: openai-go emits /chat/completions relative to its
	// /v1-suffixed base.
	req, err := http.NewRequestWithContext(context.Background(), "POST",
		e.ts.URL+"/proxy/llm/openai/chat/completions", strings.NewReader(`{}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer run-token")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "Bearer real-key", gotAuthz)
	require.Empty(t, gotAPIKey)
}

func TestForwardStreamsIncrementally(t *testing.T) {
	first := make(chan struct{})
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: one\n\n"))
		w.(http.Flusher).Flush()
		close(first)
		<-release
		_, _ = w.Write([]byte("data: two\n\n"))
	}))
	t.Cleanup(upstream.Close)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	e := newEnv(t, upstream.URL, mustPermit(t, `{"v":1,"llm":{"providers":["anthropic"]}}`))
	req, err := http.NewRequestWithContext(context.Background(), "POST",
		e.ts.URL+"/proxy/llm/anthropic/v1/messages", nil)
	require.NoError(t, err)
	req.Header.Set("x-api-key", "tok")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })

	// The first chunk must arrive while the upstream is still holding the
	// stream open — proof the proxy flushes instead of buffering.
	<-first
	buf := make([]byte, 64)
	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)
	go func() { n, err := resp.Body.Read(buf); done <- result{n, err} }()
	select {
	case res := <-done:
		require.NoError(t, res.err)
		require.Contains(t, string(buf[:res.n]), "data: one")
	case <-time.After(2 * time.Second):
		t.Fatal("first chunk not delivered before upstream finished — proxy is buffering")
	}
	close(release)
}

type fakeHook struct{ err error }

func (f fakeHook) Before(ctx context.Context, req proxy.HookRequest) error { return f.err }

func TestHookErrorChoosesStatus(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("hook rejection must not reach upstream")
	}))
	t.Cleanup(upstream.Close)

	e := &env{
		auth:    &fakeAuth{identity: proxy.RunIdentity{TenantID: uuid.New(), RunID: uuid.New()}},
		permits: &fakePermits{permit: mustPermit(t, `{"v":1,"llm":{"providers":["anthropic"]}}`)},
		creds:   &fakeCreds{secret: proxy.Secret{Value: "real-key"}},
		events:  &fakeEvents{},
	}
	cfg := proxy.DefaultConfig()
	route := cfg.Providers["anthropic"]
	route.Base = upstream.URL
	cfg.Providers["anthropic"] = route
	mux := http.NewServeMux()
	proxy.RegisterRoutes(mux, proxy.Deps{Auth: e.auth, Permits: e.permits, Credentials: e.creds,
		Events: e.events, Hook: fakeHook{err: proxy.HookError{Status: http.StatusTooManyRequests, Msg: "budget"}},
		Config: cfg})
	e.ts = httptest.NewServer(mux)
	t.Cleanup(e.ts.Close)

	resp := doAnthropic(t, e, "tok")
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
}

func TestCredentialFailureIs500WithEvent(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("must not reach upstream without a credential")
	}))
	t.Cleanup(upstream.Close)

	e := newEnv(t, upstream.URL, mustPermit(t, `{"v":1,"llm":{"providers":["anthropic"]}}`))
	e.creds.err = errors.New("kek unwrap failed")
	resp := doAnthropic(t, e, "tok")
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	require.Contains(t, e.events.events, "proxy.error")
}

func TestInternalPassthrough(t *testing.T) {
	var gotPath, gotAuthz string
	internalAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuthz = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(internalAPI.Close)

	e := &env{
		auth:    &fakeAuth{},
		permits: &fakePermits{},
		creds:   &fakeCreds{},
		events:  &fakeEvents{},
	}
	cfg := proxy.DefaultConfig()
	cfg.InternalBase = internalAPI.URL
	mux := http.NewServeMux()
	proxy.RegisterRoutes(mux, proxy.Deps{Auth: e.auth, Permits: e.permits,
		Credentials: e.creds, Events: e.events, Hook: proxy.NopHook{}, Config: cfg})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	req, err := http.NewRequestWithContext(context.Background(), "POST",
		ts.URL+"/proxy/internal/internal/runs/abc/events", strings.NewReader(`{"type":"x"}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer run-token")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.Equal(t, "/internal/runs/abc/events", gotPath)
	require.Equal(t, "Bearer run-token", gotAuthz) // bearer forwarded; internal API re-auths it
}
