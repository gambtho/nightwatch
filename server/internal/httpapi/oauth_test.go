package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/tomte/server/internal/httpapi"
	"github.com/gambtho/tomte/server/internal/oauth"
	"github.com/gambtho/tomte/server/internal/store"
)

// fakeIdP fakes the provider's token endpoint for callback tests.
type fakeIdP struct {
	ts      *httptest.Server
	respond func(w http.ResponseWriter)
	hits    int
}

func withOAuth(t *testing.T) (func(*httpapi.Deps), *fakeIdP, *oauth.Signer) {
	t.Helper()
	idp := &fakeIdP{}
	idp.respond = func(w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"ok":true,"access_token":"xoxb-new","scope":"channels:read,chat:write"}`))
	}
	idp.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idp.hits++
		idp.respond(w)
	}))
	t.Cleanup(idp.ts.Close)

	signer := oauth.NewSigner([]byte("0123456789abcdef0123456789abcdef"), 15*time.Minute)
	svc := &oauth.Service{
		Providers: map[string]oauth.Endpoints{
			"slack":  {AuthURL: "https://slack.test/authorize", TokenURL: idp.ts.URL, RevokeURL: idp.ts.URL},
			"google": {AuthURL: "https://google.test/auth", TokenURL: idp.ts.URL},
		},
		Clients: func(ctx context.Context, provider string) (oauth.ClientCreds, error) {
			return oauth.ClientCreds{ID: "cid", Secret: "csec"}, nil
		},
	}
	return func(d *httpapi.Deps) {
		withCatalog(t)(d)
		d.OAuth = svc
		d.StateSigner = signer
	}, idp, signer
}

func TestOAuthStart(t *testing.T) {
	mod, _, signer := withOAuth(t)
	e := newEnv(t, mod)

	resp, out := e.do(t, "POST", "/v1/connections/oauth/slack/start",
		map[string]any{"ops": []string{"post_message"}, "return_to": "/builds/7"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	authURL, err := url.Parse(out["auth_url"].(string))
	require.NoError(t, err)
	require.Equal(t, "slack.test", authURL.Host)
	q := authURL.Query()
	require.Equal(t, "chat:write", q.Get("scope"), "only the granted op's scopes")
	require.Contains(t, q.Get("redirect_uri"), "/auth/oauth/callback")

	st, err := signer.Verify(q.Get("state"))
	require.NoError(t, err)
	require.Equal(t, e.tenant.ID, st.TenantID)
	require.Equal(t, "slack", st.Connector)
	require.Equal(t, "/builds/7", st.ReturnTo)
}

func TestOAuthStartScopeUnionWithExistingGrant(t *testing.T) {
	mod, _, signer := withOAuth(t)
	e := newEnv(t, mod)

	// An existing grant holds channels:history; asking for post_message
	// must request the union, never shrink the grant.
	seedOAuthConnection(t, e, "slack", oauth.Bundle{AccessToken: "at"}, []string{"channels:history"})

	_, out := e.do(t, "POST", "/v1/connections/oauth/slack/start",
		map[string]any{"ops": []string{"post_message"}})
	authURL, _ := url.Parse(out["auth_url"].(string))
	st, err := signer.Verify(authURL.Query().Get("state"))
	require.NoError(t, err)
	require.Equal(t, []string{"channels:history", "chat:write"}, st.Scopes)
}

func TestOAuthStartRejects(t *testing.T) {
	mod, _, _ := withOAuth(t)
	e := newEnv(t, mod)

	resp, _ := e.do(t, "POST", "/v1/connections/oauth/nope/start", nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)

	resp, _ = e.do(t, "POST", "/v1/connections/oauth/slack/start",
		map[string]any{"ops": []string{"no_such_op"}})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	resp, _ = e.do(t, "POST", "/v1/connections/oauth/slack/start",
		map[string]any{"return_to": "https://evil.example.com/"})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestOAuthCallbackWritesBundleAndRedirects(t *testing.T) {
	mod, idp, signer := withOAuth(t)
	e := newEnv(t, mod)

	state, err := signer.Sign(oauth.State{
		TenantID: e.tenant.ID, UserID: e.user.ID,
		Connector: "slack", Provider: "slack",
		Scopes: []string{"channels:read", "chat:write"}, ReturnTo: "/settings",
	})
	require.NoError(t, err)

	resp := getNoRedirect(t, e, "/auth/oauth/callback?code=the-code&state="+url.QueryEscape(state))
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	loc, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "/settings", loc.Path)
	require.Equal(t, "ok", loc.Query().Get("connect"))
	require.Equal(t, 1, idp.hits)

	// The bundle landed encrypted, with metadata and kind oauth.
	conn, err := e.store.GetConnection(context.Background(), e.tenant.ID, "slack", "default")
	require.NoError(t, err)
	require.Equal(t, "oauth", conn.Kind)
	require.Equal(t, "ok", conn.Status)
	var meta oauth.Metadata
	require.NoError(t, json.Unmarshal(conn.Metadata, &meta))
	require.Equal(t, []string{"channels:read", "chat:write"}, meta.Scopes)

	// And the catalog now shows slack connected.
	_, out := e.do(t, "GET", "/v1/catalog", nil)
	for _, c := range out["connectors"].([]any) {
		cm := c.(map[string]any)
		if cm["id"] == "slack" {
			require.Equal(t, true, cm["connected"])
			require.Equal(t, "ok", cm["status"])
		}
	}
}

func TestOAuthCallbackFailures(t *testing.T) {
	mod, idp, signer := withOAuth(t)
	e := newEnv(t, mod)

	// Invalid state: 400, no redirect anywhere.
	resp := getNoRedirect(t, e, "/auth/oauth/callback?code=x&state=garbage")
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	state, err := signer.Sign(oauth.State{
		TenantID: e.tenant.ID, UserID: e.user.ID,
		Connector: "slack", Provider: "slack", ReturnTo: "/settings",
	})
	require.NoError(t, err)

	// Provider handed back an error: bounce to the app with
	// connect=error, no token-endpoint call.
	resp = getNoRedirect(t, e, "/auth/oauth/callback?error=access_denied&state="+url.QueryEscape(state))
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	loc, _ := url.Parse(resp.Header.Get("Location"))
	require.Equal(t, "error", loc.Query().Get("connect"))
	require.Equal(t, 0, idp.hits)

	// Exchange failure: same bounce.
	idp.respond = func(w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_code"}`))
	}
	resp = getNoRedirect(t, e, "/auth/oauth/callback?code=bad&state="+url.QueryEscape(state))
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	loc, _ = url.Parse(resp.Header.Get("Location"))
	require.Equal(t, "error", loc.Query().Get("connect"))
	_, err = e.store.GetConnection(context.Background(), e.tenant.ID, "slack", "default")
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestDeleteOAuthConnectionRevokesBestEffort(t *testing.T) {
	mod, idp, _ := withOAuth(t)
	e := newEnv(t, mod)
	seedOAuthConnection(t, e, "slack", oauth.Bundle{AccessToken: "at", RefreshToken: "rt"}, nil)

	resp, _ := e.do(t, "DELETE", "/v1/connections/default?provider=slack", nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.Equal(t, 1, idp.hits, "provider revoke called")
	_, err := e.store.GetConnection(context.Background(), e.tenant.ID, "slack", "default")
	require.ErrorIs(t, err, store.ErrNotFound)
}

// Approval-time connection re-validation (build item 9).
func TestApproveChecksConnections(t *testing.T) {
	mod, _, _ := withOAuth(t)
	e := newEnv(t, mod)

	create := func(t *testing.T) string {
		b := connectionsBody(map[string]any{
			"slack": map[string]any{
				"kind": "http", "ops": []string{"post_message"},
				"resources": map[string]any{"post_message": map[string]any{"channel": []string{"C1"}}},
			},
		})
		resp, out := e.do(t, "POST", "/v1/workflows", b)
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		return out["workflow"].(map[string]any)["id"].(string)
	}

	// Not connected: 400.
	id := create(t)
	resp, out := e.do(t, "POST", "/v1/workflows/"+id+"/versions/1/approve", nil)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Contains(t, out["error"], "not connected")

	// Connected but needs_reauth: 400.
	conn := seedOAuthConnection(t, e, "slack", oauth.Bundle{AccessToken: "at"}, nil)
	_, err := e.store.MarkConnectionNeedsReauth(context.Background(), e.tenant.ID, conn.ID, conn.Epoch)
	require.NoError(t, err)
	resp, out = e.do(t, "POST", "/v1/workflows/"+id+"/versions/1/approve", nil)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Contains(t, out["error"], "re-authorization")

	// Healthy: approves.
	seedOAuthConnection(t, e, "slack", oauth.Bundle{AccessToken: "at2"}, nil) // bundle write resets status
	resp, _ = e.do(t, "POST", "/v1/workflows/"+id+"/versions/1/approve", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestApproveChecksNamedLLMConnection(t *testing.T) {
	e := newEnv(t, withCatalog(t))
	b := workflowBody()
	b["permit"] = map[string]any{"v": 1, "llm": map[string]any{
		"providers": []string{"anthropic"}, "connection": "work-key",
	}}
	resp, out := e.do(t, "POST", "/v1/workflows", b)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	id := out["workflow"].(map[string]any)["id"].(string)

	resp, out = e.do(t, "POST", "/v1/workflows/"+id+"/versions/1/approve", nil)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Contains(t, out["error"], "work-key")

	// Store the named key; approval goes through.
	resp, _ = e.do(t, "PUT", "/v1/connections/work-key",
		map[string]any{"provider": "anthropic", "value": "sk-ant-real"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp, _ = e.do(t, "POST", "/v1/workflows/"+id+"/versions/1/approve", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func seedOAuthConnection(t *testing.T, e *env, provider string, b oauth.Bundle, scopes []string) store.Connection {
	t.Helper()
	ctx := context.Background()
	raw, err := json.Marshal(b)
	require.NoError(t, err)
	wrapped, kekVersion, err := e.store.TenantKEK(ctx, e.tenant.ID)
	require.NoError(t, err)
	dek, ct, nonce, err := e.vault.EncryptSecret(wrapped, string(raw))
	require.NoError(t, err)
	if scopes == nil {
		scopes = b.Scopes
	}
	meta, _ := json.Marshal(oauth.Metadata{Scopes: scopes})
	conn, err := e.store.UpsertConnectionBundle(ctx, e.tenant.ID, "default", provider,
		store.BundleUpdate{Kind: "oauth", DEKWrapped: dek, Ciphertext: ct, Nonce: nonce,
			KEKVersion: kekVersion, Metadata: meta})
	require.NoError(t, err)
	return conn
}

func getNoRedirect(t *testing.T, e *env, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), "GET", e.ts.URL+path, nil)
	require.NoError(t, err)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}
