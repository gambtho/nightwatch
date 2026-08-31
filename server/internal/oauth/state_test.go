package oauth

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestStateRoundTrip(t *testing.T) {
	s := NewSigner([]byte("0123456789abcdef0123456789abcdef"), 15*time.Minute)
	in := State{
		TenantID: uuid.New(), UserID: uuid.New(),
		Connector: "slack", Provider: "slack",
		Scopes: []string{"chat:write"}, ReturnTo: "/builds/42?step=3",
	}
	raw, err := s.Sign(in)
	require.NoError(t, err)
	out, err := s.Verify(raw)
	require.NoError(t, err)
	require.Equal(t, in, out)
}

func TestStateRejects(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	s := NewSigner(key, 15*time.Minute)
	good, err := s.Sign(State{TenantID: uuid.New(), UserID: uuid.New(), Connector: "slack", Provider: "slack"})
	require.NoError(t, err)

	// Wrong key.
	other := NewSigner([]byte("fedcba9876543210fedcba9876543210"), 15*time.Minute)
	_, err = other.Verify(good)
	require.Error(t, err)

	// Expired.
	past := NewSigner(key, time.Minute)
	past.now = func() time.Time { return time.Now().Add(-time.Hour) }
	old, err := past.Sign(State{TenantID: uuid.New(), UserID: uuid.New(), Connector: "slack", Provider: "slack"})
	require.NoError(t, err)
	_, err = s.Verify(old)
	require.Error(t, err)

	// Garbage.
	_, err = s.Verify("not-a-jwt")
	require.Error(t, err)
}

// return_to is a same-origin path only: absolute URLs and the two
// browser tricks that turn a "path" into a foreign origin are refused
// at both ends.
func TestStateReturnToOpenRedirectVectors(t *testing.T) {
	s := NewSigner([]byte("0123456789abcdef0123456789abcdef"), 15*time.Minute)
	base := State{TenantID: uuid.New(), UserID: uuid.New(), Connector: "slack", Provider: "slack"}
	for _, bad := range []string{
		"https://evil.example.com/",
		"//evil.example.com/",
		"/\\evil.example.com/",
		"relative/path",
	} {
		st := base
		st.ReturnTo = bad
		_, err := s.Sign(st)
		require.Error(t, err, bad)
	}
	st := base
	st.ReturnTo = "/ok"
	_, err := s.Sign(st)
	require.NoError(t, err)
}
