// Package token signs and verifies the per-run bearer JWT the harness uses
// against the internal API. The stored hash lets the control plane hold
// proof of issuance without holding the plaintext token.
package token

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/hkdf"
)

const hkdfInfo = "nightshift:run-jwt"

type RunClaims struct {
	RunID     uuid.UUID
	TenantID  uuid.UUID
	ExpiresAt time.Time
}

type Signer struct {
	key []byte
}

// New derives the signing key from the master key so the master itself is
// never used directly for HMAC.
func New(master []byte) *Signer {
	r := hkdf.New(sha256.New, master, nil, []byte(hkdfInfo))
	key := make([]byte, 32)
	if _, err := io.ReadFull(r, key); err != nil {
		panic(fmt.Sprintf("token: hkdf: %v", err)) // cannot fail for sha256
	}
	return &Signer{key: key}
}

func (s *Signer) Sign(c RunClaims) (string, string, error) {
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"run_id": c.RunID.String(),
		"tid":    c.TenantID.String(),
		"exp":    c.ExpiresAt.Unix(),
	})
	tok, err := t.SignedString(s.key)
	if err != nil {
		return "", "", err
	}
	return tok, s.HashToken(tok), nil
}

func (s *Signer) Verify(bearer string) (RunClaims, error) {
	var out RunClaims
	parsed, err := jwt.Parse(bearer,
		func(t *jwt.Token) (any, error) { return s.key, nil },
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return out, err
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return out, errors.New("token: unexpected claims type")
	}
	runID, _ := claims["run_id"].(string)
	tid, _ := claims["tid"].(string)
	if out.RunID, err = uuid.Parse(runID); err != nil {
		return out, errors.New("token: bad run_id")
	}
	if out.TenantID, err = uuid.Parse(tid); err != nil {
		return out, errors.New("token: bad tid")
	}
	exp, err := claims.GetExpirationTime()
	if err != nil || exp == nil {
		return out, errors.New("token: bad exp")
	}
	out.ExpiresAt = exp.Time
	return out, nil
}

func (s *Signer) HashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// EqualHash compares two token hashes in constant time.
func EqualHash(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
