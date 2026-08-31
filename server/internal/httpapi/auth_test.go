package httpapi_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/httpapi"
)

// requestMagicLink POSTs the magic-link form and returns the raw response
// body bytes for byte-identity assertions.
func requestMagicLink(t *testing.T, e *env, body string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Post(e.ts.URL+"/v1/auth/magic-link", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, string(b)
}

// lastMagicToken pulls the token out of the most recent recorded email.
func lastMagicToken(t *testing.T, e *env) string {
	t.Helper()
	msgs := e.mailer.Messages()
	require.NotEmpty(t, msgs)
	body := msgs[len(msgs)-1].Body
	i := strings.Index(body, "/auth/verify?token=")
	require.GreaterOrEqual(t, i, 0, "mail body carries the verify link: %q", body)
	link := body[i:]
	if end := strings.IndexAny(link, " \n"); end >= 0 {
		link = link[:end]
	}
	u, err := url.Parse(link)
	require.NoError(t, err)
	return u.Query().Get("token")
}

func TestMagicLinkResponsesAreByteIdentical(t *testing.T) {
	e := newEnv(t)

	// e.user exists as pat@acme.test; nobody@ does not.
	respKnown, bodyKnown := requestMagicLink(t, e, `{"email":"pat@acme.test"}`)
	respUnknown, bodyUnknown := requestMagicLink(t, e, `{"email":"nobody@else.test"}`)
	require.Equal(t, http.StatusAccepted, respKnown.StatusCode)
	require.Equal(t, http.StatusAccepted, respUnknown.StatusCode)
	require.Equal(t, bodyKnown, bodyUnknown)

	// Both got mail; the link points at the public base URL.
	msgs := e.mailer.Messages()
	require.Len(t, msgs, 2)
	require.Contains(t, msgs[0].Body, e.baseURL.String()+"/auth/verify?token=")
}

func TestMagicLinkPerEmailBudget(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 5; i++ {
		resp, _ := requestMagicLink(t, e, `{"email":"pat@acme.test"}`)
		require.Equal(t, http.StatusAccepted, resp.StatusCode)
	}
	require.Len(t, e.mailer.Messages(), 3, "over-budget requests still 202 but send nothing")
}

func TestMagicLinkStoresOnlyRelativeNext(t *testing.T) {
	e := newEnv(t)

	requestMagicLink(t, e, `{"email":"pat@acme.test","next":"/runs/42"}`)
	tok := lastMagicToken(t, e)
	target := postVerify(t, e, tok)
	require.Equal(t, e.baseURL.String()+"/runs/42", target)

	// An absolute or scheme-relative next is dropped, not stored.
	requestMagicLink(t, e, `{"email":"pat@acme.test","next":"https://evil.test/x"}`)
	target = postVerify(t, e, lastMagicToken(t, e))
	require.Equal(t, e.baseURL.String()+"/", target)

	requestMagicLink(t, e, `{"email":"pat@acme.test","next":"//evil.test/x"}`)
	target = postVerify(t, e, lastMagicToken(t, e))
	require.Equal(t, e.baseURL.String()+"/", target)
}

// postVerify submits the consuming POST and returns the redirect target.
func postVerify(t *testing.T, e *env, token string) string {
	t.Helper()
	resp := postVerifyResp(t, e, token, nil)
	defer resp.Body.Close()
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	return resp.Header.Get("Location")
}

func postVerifyResp(t *testing.T, e *env, token string, extra url.Values) *http.Response {
	t.Helper()
	form := url.Values{"token": {token}}
	for k, v := range extra {
		form[k] = v
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Post(e.ts.URL+"/v1/auth/verify", "application/x-www-form-urlencoded",
		strings.NewReader(form.Encode()))
	require.NoError(t, err)
	return resp
}

func TestInterstitialGETConsumesNothing(t *testing.T) {
	e := newEnv(t)
	requestMagicLink(t, e, `{"email":"new@person.test"}`)
	tok := lastMagicToken(t, e)

	for i := 0; i < 2; i++ {
		resp, err := http.Get(e.ts.URL + "/auth/verify?token=" + url.QueryEscape(tok))
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Contains(t, string(body), "Continue to Nightshift")
		require.NotContains(t, resp.Header.Get("Set-Cookie"), "ns_session")
	}

	// The token still works: the GETs consumed nothing.
	target := postVerify(t, e, tok)
	require.NotEmpty(t, target)
}

func TestInterstitialGETUnknownTokenRendersExpiredPage(t *testing.T) {
	e := newEnv(t)
	resp, err := http.Get(e.ts.URL + "/auth/verify?token=bogus")
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, string(body), "expired")
	require.NotContains(t, string(body), "Continue to Nightshift")
}

func TestVerifyFirstLoginMintsTenantAndRedirectsToBuild(t *testing.T) {
	e := newEnv(t)
	requestMagicLink(t, e, `{"email":"new@person.test","next":"/runs/42"}`)
	tok := lastMagicToken(t, e)

	resp := postVerifyResp(t, e, tok, nil)
	defer resp.Body.Close()
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	// First login goes to the build conversation, not next_path.
	require.Equal(t, e.baseURL.String()+"/build", resp.Header.Get("Location"))

	var sessionCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == httpapi.SessionCookieName {
			sessionCookie = c
		}
	}
	require.NotNil(t, sessionCookie)
	require.True(t, sessionCookie.Secure)
	require.True(t, sessionCookie.HttpOnly)

	// The minted tenant is named from the email local part and the
	// session authenticates against /v1/me.
	user, err := e.store.UserByEmail(t.Context(), "new@person.test")
	require.NoError(t, err)
	tn, err := e.store.GetTenant(t.Context(), user.TenantID)
	require.NoError(t, err)
	require.Equal(t, "new", tn.Name)
	_, _, err = e.store.TenantKEK(t.Context(), tn.ID)
	require.NoError(t, err)

	req, err := http.NewRequest("GET", e.ts.URL+"/v1/me", nil)
	require.NoError(t, err)
	req.AddCookie(&http.Cookie{Name: httpapi.SessionCookieName, Value: sessionCookie.Value})
	me, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer me.Body.Close()
	require.Equal(t, http.StatusOK, me.StatusCode)
	b, err := io.ReadAll(me.Body)
	require.NoError(t, err)
	require.Contains(t, string(b), "new@person.test")
	require.Contains(t, string(b), tn.ID.String())
}

func TestVerifyIgnoresRequestTimeNext(t *testing.T) {
	e := newEnv(t)
	requestMagicLink(t, e, `{"email":"pat@acme.test"}`)
	tok := lastMagicToken(t, e)

	resp := postVerifyResp(t, e, tok, url.Values{"next": {"https://evil.test/x"}})
	defer resp.Body.Close()
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	require.Equal(t, e.baseURL.String()+"/", resp.Header.Get("Location"))
}

func TestVerifyConsumedTokenRendersExpiredPage(t *testing.T) {
	e := newEnv(t)
	requestMagicLink(t, e, `{"email":"pat@acme.test"}`)
	tok := lastMagicToken(t, e)
	postVerify(t, e, tok)

	resp := postVerifyResp(t, e, tok, nil)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, string(body), "expired")
	require.Empty(t, resp.Cookies())
}

func TestLogoutDeletesSessionAndClearsCookie(t *testing.T) {
	e := newEnv(t)

	// e.cookie authenticates before logout.
	resp, _ := e.do(t, "GET", "/v1/me", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp, _ = e.do(t, "POST", "/v1/auth/logout", nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	var cleared *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == httpapi.SessionCookieName {
			cleared = c
		}
	}
	require.NotNil(t, cleared)
	require.Negative(t, cleared.MaxAge)

	resp, _ = e.do(t, "GET", "/v1/me", nil)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}
