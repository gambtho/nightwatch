package oauth

import (
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// State is the signed, expiring nonce the connect flow round-trips
// through the provider: everything the public callback needs to finish
// without a session. ReturnTo is a same-origin path the frontend is sent
// back to (build item 7) — a path, never a full URL, so the callback
// cannot become an open redirect.
type State struct {
	TenantID  uuid.UUID
	UserID    uuid.UUID
	Connector string
	Provider  string
	Scopes    []string
	ReturnTo  string
}

const stateAudience = "tomte-oauth-state"

// validReturnTo accepts only a same-origin path: it must start with a
// single "/" — "//host" is a scheme-relative URL and "/\" is treated
// as "//" by browsers, so both are open-redirect vectors, not paths.
func validReturnTo(p string) error {
	if p == "" {
		return nil
	}
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") {
		return fmt.Errorf("oauth: return_to must be a same-origin path")
	}
	return nil
}

type stateClaims struct {
	jwt.RegisteredClaims
	UserID    string   `json:"uid"`
	Connector string   `json:"connector"`
	Provider  string   `json:"provider"`
	Scopes    []string `json:"scopes,omitempty"`
	ReturnTo  string   `json:"return_to,omitempty"`
}

// Signer signs and verifies state nonces. The key must be dedicated to
// this purpose (derive it, never reuse another signing key raw).
type Signer struct {
	key []byte
	ttl time.Duration
	now func() time.Time
}

func NewSigner(key []byte, ttl time.Duration) *Signer {
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	return &Signer{key: append([]byte(nil), key...), ttl: ttl, now: time.Now}
}

func (s *Signer) Sign(st State) (string, error) {
	if err := validReturnTo(st.ReturnTo); err != nil {
		return "", err
	}
	now := s.now()
	claims := stateClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   st.TenantID.String(),
			Audience:  jwt.ClaimStrings{stateAudience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.ttl)),
			ID:        uuid.NewString(),
		},
		UserID:    st.UserID.String(),
		Connector: st.Connector,
		Provider:  st.Provider,
		Scopes:    st.Scopes,
		ReturnTo:  st.ReturnTo,
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.key)
}

func (s *Signer) Verify(raw string) (State, error) {
	var claims stateClaims
	_, err := jwt.ParseWithClaims(raw, &claims,
		func(t *jwt.Token) (any, error) { return s.key, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithAudience(stateAudience),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(func() time.Time { return s.now() }),
	)
	if err != nil {
		return State{}, fmt.Errorf("oauth: state: %w", err)
	}
	tenantID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return State{}, fmt.Errorf("oauth: state: bad tenant")
	}
	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		return State{}, fmt.Errorf("oauth: state: bad user")
	}
	if err := validReturnTo(claims.ReturnTo); err != nil {
		return State{}, err
	}
	return State{
		TenantID: tenantID, UserID: userID,
		Connector: claims.Connector, Provider: claims.Provider,
		Scopes: claims.Scopes, ReturnTo: claims.ReturnTo,
	}, nil
}
