package token_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/token"
)

func TestSignVerify(t *testing.T) {
	s := token.New([]byte("0123456789abcdef0123456789abcdef"))
	claims := token.RunClaims{
		RunID: uuid.New(), TenantID: uuid.New(),
		ExpiresAt: time.Now().Add(time.Hour),
	}
	tok, hash, err := s.Sign(claims)
	require.NoError(t, err)
	require.Equal(t, s.HashToken(tok), hash)

	got, err := s.Verify(tok)
	require.NoError(t, err)
	require.Equal(t, claims.RunID, got.RunID)
	require.Equal(t, claims.TenantID, got.TenantID)
}

func TestVerifyRejects(t *testing.T) {
	s := token.New([]byte("0123456789abcdef0123456789abcdef"))
	other := token.New([]byte("ffffffffffffffffffffffffffffffff"))

	claims := token.RunClaims{RunID: uuid.New(), TenantID: uuid.New(), ExpiresAt: time.Now().Add(time.Hour)}
	tok, _, err := s.Sign(claims)
	require.NoError(t, err)
	_, err = other.Verify(tok) // wrong key
	require.Error(t, err)

	expired := token.RunClaims{RunID: uuid.New(), TenantID: uuid.New(), ExpiresAt: time.Now().Add(-time.Minute)}
	tok, _, err = s.Sign(expired)
	require.NoError(t, err)
	_, err = s.Verify(tok)
	require.Error(t, err)
}
