package httpapi

import (
	"net/http"
	"strings"
)

type authHandlers struct {
	d Deps
}

// isSafeRelativePath admits only a same-origin absolute path: one leading
// slash (not scheme-relative //), no backslashes, no control characters —
// the value is later written into a Location header, so CR/LF especially
// must never be stored.
func isSafeRelativePath(p string) bool {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.Contains(p, "\\") {
		return false
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
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
