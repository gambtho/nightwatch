package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testClients(ctx context.Context, provider string) (ClientCreds, error) {
	return ClientCreds{ID: "client-id", Secret: "client-secret"}, nil
}

// fakeProvider is an httptest token endpoint capturing the last form.
type fakeProvider struct {
	ts       *httptest.Server
	lastForm url.Values
	respond  func(w http.ResponseWriter)
	hits     int
}

func newFakeProvider(t *testing.T) *fakeProvider {
	t.Helper()
	f := &fakeProvider{}
	f.respond = func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at-1","refresh_token":"rt-1","expires_in":3600,"scope":"a b"}`))
	}
	f.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		f.lastForm = r.PostForm
		f.hits++
		f.respond(w)
	}))
	t.Cleanup(f.ts.Close)
	return f
}

func service(f *fakeProvider) *Service {
	return &Service{
		Providers: map[string]Endpoints{
			"fakeoauth": {AuthURL: "https://auth.example.com/authorize", TokenURL: f.ts.URL, RevokeURL: f.ts.URL},
		},
		Clients: testClients,
	}
}

func TestAuthURL(t *testing.T) {
	svc := service(newFakeProvider(t))
	svc.Providers["fakeoauth"] = Endpoints{
		AuthURL:    "https://auth.example.com/authorize",
		TokenURL:   "https://unused.example.com",
		AuthParams: url.Values{"access_type": {"offline"}},
	}
	got, err := svc.AuthURL(context.Background(), "fakeoauth", "https://app.test/auth/oauth/callback", "signed-state", []string{"a", "b"})
	require.NoError(t, err)
	u, err := url.Parse(got)
	require.NoError(t, err)
	q := u.Query()
	require.Equal(t, "client-id", q.Get("client_id"))
	require.Equal(t, "https://app.test/auth/oauth/callback", q.Get("redirect_uri"))
	require.Equal(t, "code", q.Get("response_type"))
	require.Equal(t, "a b", q.Get("scope"))
	require.Equal(t, "signed-state", q.Get("state"))
	require.Equal(t, "offline", q.Get("access_type"))

	_, err = svc.AuthURL(context.Background(), "unknown", "https://app.test/cb", "s", nil)
	require.Error(t, err)
}

func TestExchange(t *testing.T) {
	f := newFakeProvider(t)
	svc := service(f)
	b, err := svc.Exchange(context.Background(), "fakeoauth", "the-code", "https://app.test/cb", []string{"a"})
	require.NoError(t, err)
	require.Equal(t, "authorization_code", f.lastForm.Get("grant_type"))
	require.Equal(t, "the-code", f.lastForm.Get("code"))
	require.Equal(t, "client-secret", f.lastForm.Get("client_secret"))
	require.Equal(t, "at-1", b.AccessToken)
	require.Equal(t, "rt-1", b.RefreshToken)
	require.Equal(t, []string{"a", "b"}, b.Scopes)
	require.InDelta(t, time.Hour.Seconds(), time.Until(b.Expiry).Seconds(), 60)
}

func TestRefreshKeepsRefreshTokenWhenOmitted(t *testing.T) {
	f := newFakeProvider(t)
	f.respond = func(w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"access_token":"at-2","expires_in":3600}`))
	}
	svc := service(f)
	prev := Bundle{AccessToken: "at-1", RefreshToken: "rt-keep", Scopes: []string{"a"}}
	b, err := svc.Refresh(context.Background(), "fakeoauth", prev)
	require.NoError(t, err)
	require.Equal(t, "refresh_token", f.lastForm.Get("grant_type"))
	require.Equal(t, "rt-keep", f.lastForm.Get("refresh_token"))
	require.Equal(t, "at-2", b.AccessToken)
	require.Equal(t, "rt-keep", b.RefreshToken, "stored refresh token survives an omitting response")
	require.Equal(t, []string{"a"}, b.Scopes)
}

func TestRefreshWithoutRefreshTokenFails(t *testing.T) {
	svc := service(newFakeProvider(t))
	_, err := svc.Refresh(context.Background(), "fakeoauth", Bundle{AccessToken: "at"})
	require.ErrorContains(t, err, "no refresh token")
}

func TestTokenEndpointFailures(t *testing.T) {
	f := newFakeProvider(t)
	svc := service(f)

	f.respond = func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}
	_, err := svc.Exchange(context.Background(), "fakeoauth", "bad", "https://app.test/cb", nil)
	require.Error(t, err)

	// Slack-style failure: 200 with ok:false.
	f.respond = func(w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_code"}`))
	}
	_, err = svc.Exchange(context.Background(), "fakeoauth", "bad", "https://app.test/cb", nil)
	require.ErrorContains(t, err, "invalid_code")

	f.respond = func(w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"scope":"a"}`))
	}
	_, err = svc.Exchange(context.Background(), "fakeoauth", "x", "https://app.test/cb", nil)
	require.ErrorContains(t, err, "no access token")
}

func TestSlackCommaScopes(t *testing.T) {
	f := newFakeProvider(t)
	f.respond = func(w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"ok":true,"access_token":"xoxb-1","scope":"channels:read,chat:write"}`))
	}
	svc := service(f)
	b, err := svc.Exchange(context.Background(), "fakeoauth", "c", "https://app.test/cb", nil)
	require.NoError(t, err)
	require.Equal(t, []string{"channels:read", "chat:write"}, b.Scopes)
	require.True(t, b.Expiry.IsZero(), "non-expiring token stays refresh-free")
}

func TestRevoke(t *testing.T) {
	f := newFakeProvider(t)
	f.respond = func(w http.ResponseWriter) { _, _ = w.Write([]byte(`{}`)) }
	svc := service(f)
	require.NoError(t, svc.Revoke(context.Background(), "fakeoauth",
		Bundle{AccessToken: "at", RefreshToken: "rt"}))
	require.Equal(t, "rt", f.lastForm.Get("token"), "refresh token preferred for revocation")

	// No RevokeURL: a no-op, not an error.
	svc.Providers["norevoke"] = Endpoints{AuthURL: "https://a", TokenURL: "https://t"}
	require.NoError(t, svc.Revoke(context.Background(), "norevoke", Bundle{AccessToken: "at"}))
}

func TestBundleJSONRoundTrip(t *testing.T) {
	b := Bundle{AccessToken: "at", RefreshToken: "rt", Scopes: []string{"a"}}
	raw, err := json.Marshal(b)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "expiry", "zero expiry stays out of the bundle")
	var back Bundle
	require.NoError(t, json.Unmarshal(raw, &back))
	require.Equal(t, b, back)
}

func TestEnvClients(t *testing.T) {
	env := map[string]string{
		"TOMTE_OAUTH_GOOGLE_CLIENT_ID":     "gid",
		"TOMTE_OAUTH_GOOGLE_CLIENT_SECRET": "gsec",
	}
	src := EnvClients(func(k string) string { return env[k] })
	c, err := src(context.Background(), "google")
	require.NoError(t, err)
	require.Equal(t, ClientCreds{ID: "gid", Secret: "gsec"}, c)
	_, err = src(context.Background(), "slack")
	require.ErrorContains(t, err, "TOMTE_OAUTH_SLACK_CLIENT_ID")
}
