package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/tomte/server/internal/store"
	"github.com/gambtho/tomte/server/internal/testpg"
)

func hash(b byte) []byte {
	h := make([]byte, 32)
	for i := range h {
		h[i] = b
	}
	return h
}

func TestEmailGloballyUniqueAcrossTenants(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()

	a, err := s.CreateTenant(ctx, "acme", testKEK)
	require.NoError(t, err)
	b, err := s.CreateTenant(ctx, "bloom", testKEK)
	require.NoError(t, err)

	_, err = s.UpsertUser(ctx, a.ID, "pat@acme.test")
	require.NoError(t, err)
	_, err = s.UpsertUser(ctx, b.ID, "pat@acme.test")
	require.Error(t, err, "same email under a second tenant must be rejected")
}

func TestUpsertUserNormalizesEmail(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()

	tn, err := s.CreateTenant(ctx, "acme", testKEK)
	require.NoError(t, err)

	u1, err := s.UpsertUser(ctx, tn.ID, "  Pat@Acme.TEST ")
	require.NoError(t, err)
	require.Equal(t, "pat@acme.test", u1.Email)

	u2, err := s.UpsertUser(ctx, tn.ID, "pat@acme.test")
	require.NoError(t, err)
	require.Equal(t, u1.ID, u2.ID, "normalized forms are the same user")
}
