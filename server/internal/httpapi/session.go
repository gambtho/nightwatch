// Package httpapi is the public, session-authenticated /v1 API.
package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/gambtho/tomte/server/internal/store"
)

// SessionCookieName carries the __Host- prefix: browsers accept it only
// with Secure, Path=/, and no Domain attribute, which pins the cookie to
// exactly our origin. Local dev over plain-HTTP localhost works in
// browsers that extend Secure-cookie lenience to localhost (Chromium,
// Firefox); Safari does not.
const SessionCookieName = "__Host-tomte_session"

// sessionCookieMaxAge mirrors the session row's 30-day absolute cap; the
// row's 7-day idle window can end the session earlier than the cookie.
const sessionCookieMaxAge = 30 * 24 * time.Hour

type SessionClaims struct {
	UserID   uuid.UUID
	TenantID uuid.UUID
	Role     string
}

type claimsKey struct{}

// NewOpaqueToken mints an opaque credential (used for both sessions and
// magic-link tokens): the presented value is base64url of 256 random
// bits, and only its SHA-256 reaches the database.
func NewOpaqueToken() (value string, tokenHash []byte, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	value = base64.RawURLEncoding.EncodeToString(raw)
	return value, HashToken(value), nil
}

// HashToken maps a presented token value to its stored hash.
func HashToken(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

func SessionCookie(value string) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   int(sessionCookieMaxAge / time.Second),
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

func ClearSessionCookie() *http.Cookie {
	c := SessionCookie("")
	c.MaxAge = -1
	return c
}

// RequireSession authenticates the opaque cookie against the session
// table. One indexed query joins session to app_user and returns the
// user's CURRENT role — a revoked session, an expired window, or a
// vanished user row are all the same plain 401. An infrastructure error
// is NOT a 401: a database outage must not read as "session revoked".
func RequireSession(s *store.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(SessionCookieName)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		userID, tenantID, role, err := s.SessionUser(r.Context(), HashToken(cookie.Value))
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err != nil {
			slog.Error("session: lookup", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		claims := SessionClaims{UserID: userID, TenantID: tenantID, Role: role}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), claimsKey{}, claims)))
	})
}

func ClaimsFrom(ctx context.Context) SessionClaims {
	c, _ := ctx.Value(claimsKey{}).(SessionClaims)
	return c
}
