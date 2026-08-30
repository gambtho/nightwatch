// Package httpapi is the public, session-authenticated /v1 API.
package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const SessionCookieName = "ns_session"

type SessionClaims struct {
	UserID   uuid.UUID `json:"uid"`
	TenantID uuid.UUID `json:"tid"`
	Role     string    `json:"role"`
	Exp      int64     `json:"exp"`
}

type claimsKey struct{}

func mac(key, payload []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(payload)
	return m.Sum(nil)
}

func SignSession(c SessionClaims, key []byte, ttl time.Duration) (string, error) {
	c.Exp = time.Now().Add(ttl).Unix()
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString(payload) + "." + enc.EncodeToString(mac(key, payload)), nil
}

func VerifySession(value string, key []byte) (SessionClaims, error) {
	var c SessionClaims
	part, sig, ok := strings.Cut(value, ".")
	if !ok {
		return c, errors.New("session: malformed")
	}
	enc := base64.RawURLEncoding
	payload, err := enc.DecodeString(part)
	if err != nil {
		return c, errors.New("session: malformed")
	}
	gotMAC, err := enc.DecodeString(sig)
	if err != nil || !hmac.Equal(gotMAC, mac(key, payload)) {
		return c, errors.New("session: bad signature")
	}
	if err := json.Unmarshal(payload, &c); err != nil {
		return c, err
	}
	if time.Now().Unix() >= c.Exp {
		return SessionClaims{}, errors.New("session: expired")
	}
	return c, nil
}

func SessionCookie(key []byte, c SessionClaims, ttl time.Duration) (*http.Cookie, error) {
	val, err := SignSession(c, key, ttl)
	if err != nil {
		return nil, err
	}
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    val,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}, nil
}

func RequireSession(key []byte, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(SessionCookieName)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		claims, err := VerifySession(cookie.Value, key)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), claimsKey{}, claims)))
	})
}

func ClaimsFrom(ctx context.Context) SessionClaims {
	c, _ := ctx.Value(claimsKey{}).(SessionClaims)
	return c
}
