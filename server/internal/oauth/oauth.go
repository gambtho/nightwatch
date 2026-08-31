// Package oauth implements the platform-app OAuth connect flow for
// curated connectors: provider endpoint registry, the signed state
// nonce, code exchange, token refresh, and best-effort revocation. It is
// pure protocol — persistence and encryption stay with store/vault, and
// composition happens in httpapi (connect flow) and proxyadapter
// (injection-time refresh).
package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Bundle is the decrypted shape of an oauth-kind connection's
// ciphertext: exactly what injection and refresh need, nothing more.
type Bundle struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	// Expiry is zero for tokens that do not expire (Slack); such a
	// bundle is never refreshed.
	Expiry time.Time `json:"expiry,omitzero"`
	Scopes []string  `json:"scopes,omitempty"`
}

// Metadata is the non-secret face stored beside the ciphertext — what
// GET /v1/connections may show.
type Metadata struct {
	Scopes []string `json:"scopes,omitempty"`
}

// Endpoints is one provider's protocol surface. Test servers override
// the URLs; production values live in Providers.
type Endpoints struct {
	AuthURL  string
	TokenURL string
	// RevokeURL empty means the provider gets no best-effort revoke call.
	RevokeURL string
	// AuthParams are provider-specific additions to the authorization
	// redirect (offline access, incremental auth).
	AuthParams url.Values
}

// Providers are the platform-app OAuth providers curated connectors may
// name (catalog auth.provider values).
func Providers() map[string]Endpoints {
	return map[string]Endpoints{
		"google": {
			AuthURL:   "https://accounts.google.com/o/oauth2/v2/auth",
			TokenURL:  "https://oauth2.googleapis.com/token",
			RevokeURL: "https://oauth2.googleapis.com/revoke",
			AuthParams: url.Values{
				// A refresh token arrives only with offline access and
				// an explicit consent prompt; incremental auth keeps
				// re-consent additive over the shared connection.
				"access_type":            {"offline"},
				"prompt":                 {"consent"},
				"include_granted_scopes": {"true"},
			},
		},
		"slack": {
			AuthURL:   "https://slack.com/oauth/v2/authorize",
			TokenURL:  "https://slack.com/api/oauth.v2.access",
			RevokeURL: "https://slack.com/api/auth.revoke",
		},
	}
}

// ClientCreds is one OAuth app. The resolver seam is where a per-tenant
// BYO app record will be consulted before the platform app; v1 builds
// only the platform path.
type ClientCreds struct {
	ID     string
	Secret string
}

// ClientSource resolves the OAuth app for a tenant+provider. The
// tenantID parameter is the designed-for BYO seam.
type ClientSource func(ctx context.Context, provider string) (ClientCreds, error)

// Service executes the protocol against a provider set.
type Service struct {
	Providers map[string]Endpoints
	Clients   ClientSource
	// HTTP is the client used for token-endpoint calls; nil means a
	// 30-second-timeout default. Token endpoints are fixed provider
	// constants, not user-controlled destinations.
	HTTP *http.Client
}

func (s *Service) http() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (s *Service) endpoints(provider string) (Endpoints, error) {
	ep, ok := s.Providers[provider]
	if !ok {
		return Endpoints{}, fmt.Errorf("oauth: unknown provider %q", provider)
	}
	return ep, nil
}

// AuthURL builds the provider authorization redirect for the requested
// scopes.
func (s *Service) AuthURL(ctx context.Context, provider, redirectURI, state string, scopes []string) (string, error) {
	ep, err := s.endpoints(provider)
	if err != nil {
		return "", err
	}
	creds, err := s.Clients(ctx, provider)
	if err != nil {
		return "", err
	}
	q := url.Values{}
	for k, vs := range ep.AuthParams {
		q[k] = append([]string(nil), vs...)
	}
	q.Set("client_id", creds.ID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", strings.Join(scopes, " "))
	q.Set("state", state)
	return ep.AuthURL + "?" + q.Encode(), nil
}

// tokenResponse covers Google-shaped and Slack-shaped token replies.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	// Slack signals failure with ok:false + error in a 200.
	OK    *bool  `json:"ok"`
	Error string `json:"error"`
}

func (s *Service) token(ctx context.Context, provider string, form url.Values) (tokenResponse, error) {
	ep, err := s.endpoints(provider)
	if err != nil {
		return tokenResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.TokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.http().Do(req)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("oauth: %s token endpoint: %w", provider, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return tokenResponse{}, err
	}
	var tr tokenResponse
	if jerr := json.Unmarshal(body, &tr); jerr != nil {
		return tokenResponse{}, fmt.Errorf("oauth: %s token endpoint status %d: unparseable body", provider, resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		// Surface the provider's error code (invalid_grant etc.) — it is
		// exactly the context a needs_reauth diagnosis needs. Never the
		// body wholesale: error codes only.
		if tr.Error != "" {
			return tokenResponse{}, fmt.Errorf("oauth: %s token endpoint status %d: %s", provider, resp.StatusCode, tr.Error)
		}
		return tokenResponse{}, fmt.Errorf("oauth: %s token endpoint status %d", provider, resp.StatusCode)
	}
	if tr.OK != nil && !*tr.OK {
		return tokenResponse{}, fmt.Errorf("oauth: %s token endpoint: %s", provider, tr.Error)
	}
	if tr.AccessToken == "" {
		return tokenResponse{}, fmt.Errorf("oauth: %s token endpoint returned no access token", provider)
	}
	return tr, nil
}

func (s *Service) bundleFrom(tr tokenResponse, prevRefresh string, requestedScopes []string, now time.Time) Bundle {
	b := Bundle{AccessToken: tr.AccessToken, RefreshToken: tr.RefreshToken}
	// A refresh response often omits the refresh token; the stored one
	// stays valid unless the provider rotated it.
	if b.RefreshToken == "" {
		b.RefreshToken = prevRefresh
	}
	if tr.ExpiresIn > 0 {
		b.Expiry = now.Add(time.Duration(tr.ExpiresIn) * time.Second)
	}
	// Granted scopes come from the provider when reported (Google's
	// space-separated, Slack's comma-separated), else what was asked.
	switch {
	case strings.Contains(tr.Scope, ","):
		b.Scopes = strings.Split(tr.Scope, ",")
	case tr.Scope != "":
		b.Scopes = strings.Fields(tr.Scope)
	default:
		b.Scopes = append([]string(nil), requestedScopes...)
	}
	sort.Strings(b.Scopes)
	return b
}

// Exchange trades an authorization code for a bundle.
func (s *Service) Exchange(ctx context.Context, provider, code, redirectURI string, requestedScopes []string) (Bundle, error) {
	creds, err := s.Clients(ctx, provider)
	if err != nil {
		return Bundle{}, err
	}
	tr, err := s.token(ctx, provider, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {creds.ID},
		"client_secret": {creds.Secret},
	})
	if err != nil {
		return Bundle{}, err
	}
	return s.bundleFrom(tr, "", requestedScopes, time.Now()), nil
}

// Refresh trades a refresh token for a fresh bundle.
func (s *Service) Refresh(ctx context.Context, provider string, prev Bundle) (Bundle, error) {
	if prev.RefreshToken == "" {
		return Bundle{}, fmt.Errorf("oauth: %s connection has no refresh token", provider)
	}
	creds, err := s.Clients(ctx, provider)
	if err != nil {
		return Bundle{}, err
	}
	tr, err := s.token(ctx, provider, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {prev.RefreshToken},
		"client_id":     {creds.ID},
		"client_secret": {creds.Secret},
	})
	if err != nil {
		return Bundle{}, err
	}
	return s.bundleFrom(tr, prev.RefreshToken, prev.Scopes, time.Now()), nil
}

// Revoke tells the provider to invalidate the credential. Best-effort:
// callers log a failure and delete the row regardless.
func (s *Service) Revoke(ctx context.Context, provider string, b Bundle) error {
	ep, err := s.endpoints(provider)
	if err != nil {
		return err
	}
	if ep.RevokeURL == "" {
		return nil
	}
	tok := b.RefreshToken
	if tok == "" {
		tok = b.AccessToken
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.RevokeURL,
		strings.NewReader(url.Values{"token": {tok}}.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("oauth: %s revoke status %d", provider, resp.StatusCode)
	}
	// Slack signals failure as 200 + ok:false, like its token endpoint.
	var out struct {
		OK    *bool  `json:"ok"`
		Error string `json:"error"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if json.Unmarshal(body, &out) == nil && out.OK != nil && !*out.OK {
		return fmt.Errorf("oauth: %s revoke: %s", provider, out.Error)
	}
	return nil
}

// EnvClients is the v1 platform-app resolver: client credentials from
// the environment, per provider. The per-tenant BYO record slots in
// front of this lookup when it exists.
func EnvClients(get func(string) string) ClientSource {
	return func(ctx context.Context, provider string) (ClientCreds, error) {
		prefix := "NIGHTSHIFT_OAUTH_" + strings.ToUpper(strings.ReplaceAll(provider, "-", "_"))
		c := ClientCreds{ID: get(prefix + "_CLIENT_ID"), Secret: get(prefix + "_CLIENT_SECRET")}
		if c.ID == "" || c.Secret == "" {
			return ClientCreds{}, fmt.Errorf("oauth: no platform app configured for %s (set %s_CLIENT_ID and %s_CLIENT_SECRET)", provider, prefix, prefix)
		}
		return c, nil
	}
}
