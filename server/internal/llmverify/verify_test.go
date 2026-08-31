package llmverify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gambtho/tomte/server/internal/endpoint"
	"github.com/stretchr/testify/require"
)

type upstream struct {
	status  int
	body    string
	headers http.Header
	path    string
	reqBody map[string]any
}

func newUpstream(t *testing.T, u *upstream) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.headers = r.Header.Clone()
		u.path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&u.reqBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(u.status)
		_, _ = w.Write([]byte(u.body))
	}))
	t.Cleanup(ts.Close)
	return ts
}

// customEndpoint builds a validated custom endpoint pointing at the fake —
// Validate allows http for loopback hosts, so no override hook is needed.
func customEndpoint(t *testing.T, baseURL string) endpoint.Endpoint {
	t.Helper()
	e, err := endpoint.Validate(endpoint.Endpoint{
		Preset: "custom", BaseURL: baseURL, Connection: "default", RunModel: "test-model",
	})
	require.NoError(t, err)
	return e
}

func TestVerifyOpenAICompatibleOK(t *testing.T) {
	u := &upstream{status: 200,
		body: `{"choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":12,"completion_tokens":1}}`}
	ts := newUpstream(t, u)
	c := &Client{}
	res, err := c.Verify(context.Background(), customEndpoint(t, ts.URL), "sk-candidate")
	require.NoError(t, err)
	require.True(t, res.OK)
	require.Equal(t, 12, res.Usage.InputTokens)
	require.Equal(t, 1, res.Usage.OutputTokens)
	// The one allowed route, the endpoint's credential slot, one token out.
	require.Equal(t, "/chat/completions", u.path)
	require.Equal(t, "Bearer sk-candidate", u.headers.Get("Authorization"))
	require.Equal(t, "test-model", u.reqBody["model"])
	require.Equal(t, float64(1), u.reqBody["max_tokens"])
}

func TestVerifyAnthropicShape(t *testing.T) {
	u := &upstream{status: 200,
		body: `{"content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":10,"output_tokens":1}}`}
	ts := newUpstream(t, u)
	// The anthropic preset has a fixed base; tests swap it via the
	// endpoint value after validation, which only this package does.
	e, err := endpoint.Validate(endpoint.Endpoint{
		Preset: "anthropic", Connection: "default", RunModel: "claude-haiku-4-5",
	})
	require.NoError(t, err)
	e.BaseURL = ts.URL
	c := &Client{}
	res, verr := c.Verify(context.Background(), e, "sk-ant-candidate")
	require.NoError(t, verr)
	require.True(t, res.OK)
	require.Equal(t, 10, res.Usage.InputTokens)
	require.Equal(t, "/v1/messages", u.path)
	require.Equal(t, "sk-ant-candidate", u.headers.Get("x-api-key"))
	require.Empty(t, u.headers.Get("Authorization"))
	require.NotEmpty(t, u.headers.Get("anthropic-version"))
}

func TestVerifyLocalSendsNoCredential(t *testing.T) {
	u := &upstream{status: 200, body: `{"usage":{"prompt_tokens":5,"completion_tokens":1}}`}
	ts := newUpstream(t, u)
	e, err := endpoint.Validate(endpoint.Endpoint{
		Preset: "local", BaseURL: ts.URL, RunModel: "llama",
	})
	require.NoError(t, err)
	c := &Client{}
	res, verr := c.Verify(context.Background(), e, "")
	require.NoError(t, verr)
	require.True(t, res.OK)
	require.Empty(t, u.headers.Get("Authorization"))
	require.Empty(t, u.headers.Get("x-api-key"))
	require.Empty(t, u.headers.Get("api-key"))
}

func TestVerify401ReadsAsKeyRejection(t *testing.T) {
	u := &upstream{status: 401,
		body: `{"error":{"message":"Incorrect API key provided","type":"invalid_request_error"}}`}
	ts := newUpstream(t, u)
	c := &Client{}
	res, err := c.Verify(context.Background(), customEndpoint(t, ts.URL), "sk-bad")
	require.NoError(t, err)
	require.False(t, res.OK)
	require.Contains(t, res.Message, "key")
	require.NotContains(t, res.Message, "Try again in a moment")
}

func TestVerifyFailsClosedOnNonJSON200(t *testing.T) {
	u := &upstream{status: 200, body: `<html>proxy interstitial</html>`}
	ts := newUpstream(t, u)
	c := &Client{}
	res, err := c.Verify(context.Background(), customEndpoint(t, ts.URL), "sk-x")
	require.NoError(t, err)
	require.False(t, res.OK)
}

func TestVerifyServerErrorReadsAsTransient(t *testing.T) {
	u := &upstream{status: 503, body: `{}`}
	ts := newUpstream(t, u)
	c := &Client{}
	res, err := c.Verify(context.Background(), customEndpoint(t, ts.URL), "sk-x")
	require.NoError(t, err)
	require.False(t, res.OK)
	require.NotEmpty(t, res.Message)
}

func TestVerifyUnreachable(t *testing.T) {
	ts := newUpstream(t, &upstream{status: 200, body: `{}`})
	url := ts.URL
	ts.Close()
	c := &Client{}
	res, err := c.Verify(context.Background(), customEndpoint(t, url), "sk-x")
	require.NoError(t, err)
	require.False(t, res.OK)
	require.NotEmpty(t, res.Message)
}

func TestVerifyMissingUsageStillOK(t *testing.T) {
	// A minimal local server that omits usage still proves the endpoint
	// answers; the spend records zero tokens.
	u := &upstream{status: 200, body: `{"choices":[{"message":{"content":"hi"}}]}`}
	ts := newUpstream(t, u)
	c := &Client{}
	res, err := c.Verify(context.Background(), customEndpoint(t, ts.URL), "sk-x")
	require.NoError(t, err)
	require.True(t, res.OK)
	require.Zero(t, res.Usage.InputTokens)
	require.Zero(t, res.Usage.OutputTokens)
}

func TestVerifyRejects200WithErrorEnvelope(t *testing.T) {
	// OpenRouter-class gateways answer HTTP 200 with an error body for a
	// bad key. A parsed error field must reject, whatever the status.
	u := &upstream{status: 200,
		body: `{"error":{"message":"No auth credentials found","code":401}}`}
	ts := newUpstream(t, u)
	c := &Client{}
	res, err := c.Verify(context.Background(), customEndpoint(t, ts.URL), "sk-bad")
	require.NoError(t, err)
	require.False(t, res.OK)
	require.Contains(t, res.Message, "No auth credentials found")
}

func TestVerifyBilledFlag(t *testing.T) {
	// Any 2xx billed the provider — including one we fail closed on.
	u := &upstream{status: 200, body: `<html>x</html>`}
	ts := newUpstream(t, u)
	c := &Client{}
	res, err := c.Verify(context.Background(), customEndpoint(t, ts.URL), "sk-x")
	require.NoError(t, err)
	require.False(t, res.OK)
	require.True(t, res.Billed)

	u2 := &upstream{status: 401, body: `{}`}
	ts2 := newUpstream(t, u2)
	res, err = (&Client{}).Verify(context.Background(), customEndpoint(t, ts2.URL), "sk-x")
	require.NoError(t, err)
	require.False(t, res.Billed)
}

func TestVerifyNon2xxCarriesUpstreamDetail(t *testing.T) {
	u := &upstream{status: 400,
		body: `{"error":{"message":"max_tokens is not supported; use max_completion_tokens"}}`}
	ts := newUpstream(t, u)
	res, err := (&Client{}).Verify(context.Background(), customEndpoint(t, ts.URL), "sk-x")
	require.NoError(t, err)
	require.False(t, res.OK)
	require.Contains(t, res.Message, "max_completion_tokens")
}
