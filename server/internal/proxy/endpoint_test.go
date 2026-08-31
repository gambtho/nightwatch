package proxy_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/tomte/server/internal/endpoint"
	"github.com/gambtho/tomte/server/internal/permit"
	"github.com/gambtho/tomte/server/internal/proxy"
)

type fakeEndpoints struct {
	ep  *endpoint.Endpoint
	err error
}

func (f *fakeEndpoints) EndpointFor(ctx context.Context, tenantID uuid.UUID) (*endpoint.Endpoint, error) {
	return f.ep, f.err
}

// recordingCreds records the connection name it was asked for.
type recordingCreds struct {
	secret  proxy.Secret
	err     error
	sawName string
	calls   int
}

func (f *recordingCreds) Credential(ctx context.Context, tenantID uuid.UUID, name, provider string) (proxy.Secret, error) {
	f.sawName = name
	f.calls++
	return f.secret, f.err
}

type headerCapture struct {
	auth, xAPIKey, apiKey string
	hits                  int
}

func endpointEnv(t *testing.T, p permit.Permit, ep *endpoint.Endpoint, creds *recordingCreds) (*httptest.Server, *headerCapture, *fakeEvents) {
	t.Helper()
	cap := &headerCapture{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.hits++
		cap.auth = r.Header.Get("Authorization")
		cap.xAPIKey = r.Header.Get("x-api-key")
		cap.apiKey = r.Header.Get("api-key")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(upstream.Close)
	if ep != nil {
		ep.BaseURL = upstream.URL // point the endpoint at the capture server
	}
	events := &fakeEvents{}
	mux := http.NewServeMux()
	proxy.RegisterRoutes(mux, proxy.Deps{
		Auth:      &fakeAuth{identity: proxy.RunIdentity{TenantID: uuid.New(), RunID: uuid.New()}},
		Permits:   &fakePermits{permit: p},
		Endpoints: &fakeEndpoints{ep: ep},
		Events:    events, Hook: proxy.NopHook{}, Config: proxy.DefaultConfig(),
		Credentials: creds,
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, cap, events
}

func postLLM(t *testing.T, ts *httptest.Server, provider, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), "POST",
		ts.URL+"/proxy/llm/"+provider+"/"+path, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer run-token")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// The contract the spec names as worth its own test: a local endpoint
// carries no connection, and the proxy skips credential resolution and
// injection rather than failing a lookup that cannot succeed.
func TestProxyLocalEndpointSkipsCredentials(t *testing.T) {
	p := mustPermit(t, `{"v":1,"llm":{"providers":["local"]},"connections":{}}`)
	creds := &recordingCreds{err: errors.New("must never be called")}
	ts, cap, events := endpointEnv(t, p,
		&endpoint.Endpoint{Preset: "local", Kind: "openai_compatible", RunModel: "llama3", ZeroCost: true}, creds)

	resp := postLLM(t, ts, "local", "chat/completions")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, 1, cap.hits)
	require.Zero(t, creds.calls, "credential resolution skipped by contract")
	require.Empty(t, cap.auth, "no auth header reaches a local upstream")
	require.Empty(t, cap.xAPIKey)
	require.Empty(t, cap.apiKey)
	require.Contains(t, events.events, "proxy.request")
	require.NotContains(t, events.events, "proxy.error")
}

func TestProxyCustomEndpointInjectsBearer(t *testing.T) {
	p := mustPermit(t, `{"v":1,"llm":{"providers":["custom"]},"connections":{}}`)
	creds := &recordingCreds{secret: proxy.Secret{Value: "sk-custom"}}
	ts, cap, _ := endpointEnv(t, p,
		&endpoint.Endpoint{Preset: "custom", Kind: "openai_compatible", Connection: "work-key", RunModel: "m"}, creds)

	resp := postLLM(t, ts, "custom", "chat/completions")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "Bearer sk-custom", cap.auth)

	// The endpoint's connection wins over the permit's llm.connection.
	require.Equal(t, "work-key", creds.sawName)

	// The path allowlist holds on endpoint routes too.
	resp = postLLM(t, ts, "custom", "embeddings")
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestProxyAzureEndpointInjectsAPIKeyHeader(t *testing.T) {
	p := mustPermit(t, `{"v":1,"llm":{"providers":["azure"]},"connections":{}}`)
	creds := &recordingCreds{secret: proxy.Secret{Value: "az-key"}}
	ts, cap, _ := endpointEnv(t, p,
		&endpoint.Endpoint{Preset: "azure", Kind: "openai_compatible", Connection: "default", RunModel: "gpt-4o"}, creds)

	resp := postLLM(t, ts, "azure", "chat/completions")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "az-key", cap.apiKey, "Azure api keys travel in the api-key header")
	require.Empty(t, cap.auth)
}

// An endpoint lookup failure fails closed, and an endpoint for a
// different provider plays no part (legacy route table still applies).
func TestProxyEndpointLookupFailureAndMismatch(t *testing.T) {
	p := mustPermit(t, `{"v":1,"llm":{"providers":["custom"]},"connections":{}}`)
	events := &fakeEvents{}
	mux := http.NewServeMux()
	proxy.RegisterRoutes(mux, proxy.Deps{
		Auth:      &fakeAuth{identity: proxy.RunIdentity{TenantID: uuid.New(), RunID: uuid.New()}},
		Permits:   &fakePermits{permit: p},
		Endpoints: &fakeEndpoints{err: errors.New("db down")},
		Events:    events, Hook: proxy.NopHook{}, Config: proxy.DefaultConfig(),
		Credentials: &recordingCreds{},
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	resp := postLLM(t, ts, "custom", "chat/completions")
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	// custom has no static route; with no matching endpoint it is 403.
	creds := &recordingCreds{}
	ts2, _, _ := endpointEnv(t, p,
		&endpoint.Endpoint{Preset: "openai", Kind: "openai_compatible", Connection: "default", RunModel: "m"}, creds)
	resp = postLLM(t, ts2, "custom", "chat/completions")
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	require.Zero(t, creds.calls)
}
