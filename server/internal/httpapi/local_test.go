package httpapi_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/tomte/server/internal/httpapi"
)

func getNoRedirect(t *testing.T, e *env, path string, cookies ...*http.Cookie) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), "GET", e.ts.URL+path, nil)
	require.NoError(t, err)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func sessionCookieFrom(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == httpapi.SessionCookieName {
			return c
		}
	}
	return nil
}

func TestEnsureLocalOwnerIdempotent(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	tn1, u1, err := httpapi.EnsureLocalOwner(ctx, e.store, e.vault)
	require.NoError(t, err)
	tn2, u2, err := httpapi.EnsureLocalOwner(ctx, e.store, e.vault)
	require.NoError(t, err)
	require.Equal(t, tn1, tn2)
	require.Equal(t, u1, u2)
}

func TestMintLocalSessionAuthenticates(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	tn, u, err := httpapi.EnsureLocalOwner(ctx, e.store, e.vault)
	require.NoError(t, err)
	cookie, err := httpapi.MintLocalSession(ctx, e.store, e.baseURL, tn, u)
	require.NoError(t, err)

	req, err := http.NewRequest("GET", e.ts.URL+"/v1/me", nil)
	require.NoError(t, err)
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestHandoffExchange(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	tn, u, err := httpapi.EnsureLocalOwner(ctx, e.store, e.vault)
	require.NoError(t, err)
	token, err := httpapi.NewHandoffToken(ctx, e.store, tn, u)
	require.NoError(t, err)

	resp := getNoRedirect(t, e, "/local/handoff?token="+url.QueryEscape(token))
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	loc, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "/build", loc.Path)
	cookie := sessionCookieFrom(resp)
	require.NotNil(t, cookie)

	// The minted cookie authenticates.
	req, err := http.NewRequest("GET", e.ts.URL+"/v1/me", nil)
	require.NoError(t, err)
	req.AddCookie(cookie)
	meResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer meResp.Body.Close()
	require.Equal(t, http.StatusOK, meResp.StatusCode)

	// Second exchange with the same token: 403, no cookie.
	resp = getNoRedirect(t, e, "/local/handoff?token="+url.QueryEscape(token))
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	require.Nil(t, sessionCookieFrom(resp))
}

func TestHandoffNextValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	tn, u, err := httpapi.EnsureLocalOwner(ctx, e.store, e.vault)
	require.NoError(t, err)

	// A safe relative next is honored.
	token, err := httpapi.NewHandoffToken(ctx, e.store, tn, u)
	require.NoError(t, err)
	resp := getNoRedirect(t, e, "/local/handoff?token="+url.QueryEscape(token)+"&next=%2Fsettings")
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	loc, _ := url.Parse(resp.Header.Get("Location"))
	require.Equal(t, "/settings", loc.Path)

	// A hostile next falls back to the default landing.
	token, err = httpapi.NewHandoffToken(ctx, e.store, tn, u)
	require.NoError(t, err)
	resp = getNoRedirect(t, e, "/local/handoff?token="+url.QueryEscape(token)+"&next="+url.QueryEscape("https://evil.example/"))
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	loc, _ = url.Parse(resp.Header.Get("Location"))
	require.Equal(t, "/build", loc.Path)
	require.Equal(t, e.baseURL.Host, loc.Host)
}

func TestHandoffUnknownToken(t *testing.T) {
	e := newEnv(t)
	resp := getNoRedirect(t, e, "/local/handoff?token=never-minted")
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	require.Nil(t, sessionCookieFrom(resp))
}
