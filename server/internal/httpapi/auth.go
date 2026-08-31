package httpapi

import (
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gambtho/nightwatch/server/internal/store"
)

const (
	loginTokenTTL = 15 * time.Minute
	// firstLoginPath is where a freshly minted tenant lands: the UX spec's
	// build-conversation entry point. The SPA claims this route later.
	firstLoginPath = "/build"
	// maxOutstandingLinks is the per-email budget of unconsumed, unexpired
	// tokens; ipLimitMax/ipLimitWindow cap magic-link requests per source
	// IP (RemoteAddr — proxy headers are never trusted). Anti-abuse, not
	// UX: over budget still answers 202 and sends nothing.
	maxOutstandingLinks = 3
	ipLimitMax          = 10
	ipLimitWindow       = time.Hour
)

type authHandlers struct {
	d   Deps
	ips *ipLimiter
}

// interstitialPage is served by GET /auth/verify. Mail scanners prefetch
// every link, so the GET consumes nothing; only the button's POST does.
var interstitialPage = template.Must(template.New("verify").Parse(`<!doctype html>
<meta charset="utf-8"><title>Sign in — Nightshift</title>
<p>Click to finish signing in.</p>
<form method="post" action="/v1/auth/verify">
<input type="hidden" name="token" value="{{.}}">
<button type="submit">Continue to Nightshift</button>
</form>
`))

// expiredPage greets expired, reused, and unknown tokens identically —
// never an error page a non-technical user has to interpret.
const expiredPage = `<!doctype html>
<meta charset="utf-8"><title>Sign in — Nightshift</title>
<p>This sign-in link has expired or was already used.</p>
<p><a href="/">Request a new one</a> — links only last a few minutes.</p>
`

// magicLink implements POST /v1/auth/magic-link. It answers 202 with an
// identical body whether or not the email is known, over budget, or
// undeliverable — no account enumeration.
func (a *authHandlers) magicLink(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
		Next  string `json:"next"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)

	a.tryToSendLink(r, store.NormalizeEmail(in.Email), in.Next)
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (a *authHandlers) tryToSendLink(r *http.Request, email, next string) {
	ctx := r.Context()
	if !strings.Contains(email, "@") {
		return
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	if !a.ips.allow(ip) {
		return
	}
	n, err := a.d.Store.CountActiveLoginTokens(ctx, email)
	if err != nil {
		slog.Error("auth: count login tokens", "err", err)
		return
	}
	if n >= maxOutstandingLinks {
		return
	}

	value, tokenHash, err := NewSessionToken()
	if err != nil {
		slog.Error("auth: mint login token", "err", err)
		return
	}
	var nextPath *string
	if isSafeRelativePath(next) {
		nextPath = &next
	}
	if err := a.d.Store.CreateLoginToken(ctx, tokenHash, email, nextPath, time.Now().Add(loginTokenTTL)); err != nil {
		slog.Error("auth: store login token", "err", err)
		return
	}
	link := a.d.PublicBaseURL.String() + "/auth/verify?token=" + url.QueryEscape(value)
	body := "Sign in to Nightshift:\n\n" + link + "\n\nThis link expires in 15 minutes. " +
		"If you didn't request it, ignore this email."
	if err := a.d.Mailer.Send(ctx, email, "Sign in to Nightshift", body); err != nil {
		slog.Error("auth: send magic link", "err", err)
	}
}

// isSafeRelativePath admits only a same-origin absolute path: one leading
// slash (not scheme-relative //), no backslashes.
func isSafeRelativePath(p string) bool {
	return strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//") && !strings.Contains(p, "\\")
}

// verifyPage implements GET /auth/verify: the interstitial. Read-only.
func (a *authHandlers) verifyPage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	ok, err := a.d.Store.LoginTokenValid(r.Context(), HashToken(token))
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if !ok {
		_, _ = w.Write([]byte(expiredPage))
		return
	}
	if err := interstitialPage.Execute(w, token); err != nil {
		slog.Error("auth: render interstitial", "err", err)
	}
}

// verify implements POST /v1/auth/verify: the consuming step. The request
// carries only the token — a next submitted alongside it is ignored; the
// redirect target was fixed at request time and travels on the token row.
func (a *authHandlers) verify(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad form"})
		return
	}
	token := r.PostFormValue("token")

	wrappedKEK, err := a.d.Vault.NewTenantKEK()
	if err != nil {
		writeErr(w, err)
		return
	}
	value, sessionHash, err := NewSessionToken()
	if err != nil {
		writeErr(w, err)
		return
	}
	res, err := a.d.Store.ConsumeLoginToken(r.Context(), HashToken(token), sessionHash,
		store.NewSignup{WrappedKEK: wrappedKEK})
	if errors.Is(err, store.ErrLoginTokenInvalid) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(expiredPage))
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}

	target := "/"
	switch {
	case res.FirstLogin:
		target = firstLoginPath
	case res.NextPath != nil:
		target = *res.NextPath
	}
	http.SetCookie(w, SessionCookie(value))
	http.Redirect(w, r, a.d.PublicBaseURL.String()+target, http.StatusSeeOther)
}

// logout implements POST /v1/auth/logout: delete the row, clear the
// cookie. Idempotent — logging out while logged out is still a 204.
func (a *authHandlers) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(SessionCookieName); err == nil {
		if err := a.d.Store.DeleteSession(r.Context(), HashToken(cookie.Value)); err != nil {
			writeErr(w, err)
			return
		}
	}
	http.SetCookie(w, ClearSessionCookie())
	w.WriteHeader(http.StatusNoContent)
}

// me implements GET /v1/me — the UI's bootstrap call.
func (a *authHandlers) me(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	user, err := a.d.Store.GetUser(r.Context(), claims.TenantID, claims.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	tenant, err := a.d.Store.GetTenant(r.Context(), claims.TenantID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user":   map[string]any{"id": user.ID, "email": user.Email, "role": user.Role},
		"tenant": map[string]any{"id": tenant.ID, "name": tenant.Name},
	})
}

// ipLimiter is a fixed-window in-memory counter. Per-process and reset on
// restart — acceptable for an anti-abuse budget at v1.
type ipLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string]*ipWindow
}

type ipWindow struct {
	count int
	start time.Time
}

func newIPLimiter(max int, window time.Duration) *ipLimiter {
	return &ipLimiter{max: max, window: window, hits: make(map[string]*ipWindow)}
}

func (l *ipLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.hits) > 4096 {
		for k, v := range l.hits {
			if now.Sub(v.start) > l.window {
				delete(l.hits, k)
			}
		}
	}
	e := l.hits[ip]
	if e == nil || now.Sub(e.start) > l.window {
		l.hits[ip] = &ipWindow{count: 1, start: now}
		return true
	}
	e.count++
	return e.count <= l.max
}
