// Local-session surface: one install is one user, so the app shell — not
// a login flow — mints sessions. The shell calls the helpers below inside
// the process; the one HTTP piece is /local/handoff, the single-use
// exchange behind the tray's "open in browser".
package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"

	"github.com/gambtho/tomte/server/internal/store"
	"github.com/gambtho/tomte/server/internal/vault"
)

const (
	// firstLoginPath is where a fresh session lands: the UX spec's
	// build-conversation entry point. The SPA claims this route.
	firstLoginPath = "/build"
	// handoffTTL bounds the open-in-browser exchange: the token is dead
	// after one exchange or one minute, whichever comes first.
	handoffTTL = time.Minute
	// localOwnerEmail is the placeholder local identity ("Identity at its
	// floor"): the tenant and owner row survive, minted without a
	// verified email.
	localOwnerEmail = "owner@tomte.local"
	localTenantName = "local"
)

// EnsureLocalOwner resolves — or mints, on first run — the single local
// tenant and owner user. It is the dev-session flow with fixed names:
// tenant + KEK + owner in the existing transaction shapes.
func EnsureLocalOwner(ctx context.Context, s *store.Store, v *vault.Master) (tenantID, userID uuid.UUID, err error) {
	u, err := s.UserByEmail(ctx, localOwnerEmail)
	switch {
	case err == nil:
		return u.TenantID, u.ID, nil
	case errors.Is(err, store.ErrNotFound):
		wrappedKEK, kerr := v.NewTenantKEK()
		if kerr != nil {
			return uuid.Nil, uuid.Nil, kerr
		}
		tn, terr := s.CreateTenant(ctx, localTenantName, wrappedKEK)
		if terr != nil {
			return uuid.Nil, uuid.Nil, terr
		}
		user, uerr := s.UpsertUser(ctx, tn.ID, localOwnerEmail)
		if uerr != nil {
			return uuid.Nil, uuid.Nil, uerr
		}
		return tn.ID, user.ID, nil
	default:
		return uuid.Nil, uuid.Nil, err
	}
}

// MintLocalSession inserts a session row for the local owner and returns
// the cookie carrying its opaque token — what the shell injects into the
// webview at launch and on every window (re)open.
func MintLocalSession(ctx context.Context, s *store.Store, public *url.URL, tenantID, userID uuid.UUID) (*http.Cookie, error) {
	value, tokenHash, err := NewOpaqueToken()
	if err != nil {
		return nil, err
	}
	if err := s.CreateSession(ctx, tokenHash, tenantID, userID); err != nil {
		return nil, err
	}
	return SessionCookieFor(public, value), nil
}

// NewHandoffToken mints the single-use, short-TTL token behind "open in
// browser". The raw value goes into the /local/handoff URL; only its hash
// is stored.
func NewHandoffToken(ctx context.Context, s *store.Store, tenantID, userID uuid.UUID) (string, error) {
	value, tokenHash, err := NewOpaqueToken()
	if err != nil {
		return "", err
	}
	if err := s.CreateHandoffToken(ctx, tokenHash, tenantID, userID, handoffTTL); err != nil {
		return "", err
	}
	return value, nil
}

// localHandoff implements GET /local/handoff?token=&next=. The GET
// consumes: unlike an emailed magic link, this URL was minted seconds ago
// by the shell on this machine — no scanner sits in the path. The token
// is the credential, so the route carries no session middleware, and
// RequireSession still guards every /v1 route; nothing unauthenticated
// exists beyond this one exchange.
func (d Deps) localHandoff(w http.ResponseWriter, r *http.Request) {
	value, sessionHash, err := NewOpaqueToken()
	if err != nil {
		writeErr(w, err)
		return
	}
	_, _, err = d.Store.ConsumeHandoffToken(r.Context(), HashToken(r.URL.Query().Get("token")), sessionHash)
	if errors.Is(err, store.ErrHandoffTokenInvalid) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(handoffExpiredPage))
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	target := firstLoginPath
	if next := r.URL.Query().Get("next"); isSafeRelativePath(next) {
		target = next
	}
	http.SetCookie(w, SessionCookieFor(d.PublicBaseURL, value))
	http.Redirect(w, r, d.PublicBaseURL.String()+target, http.StatusSeeOther)
}

// handoffExpiredPage greets expired, reused, and unknown tokens
// identically — never an error page a non-technical user must interpret.
const handoffExpiredPage = `<!doctype html>
<meta charset="utf-8"><title>Tomte</title>
<p>This link has expired or was already used.</p>
<p>Open Tomte from the tray and choose “Open in browser” again.</p>
`
