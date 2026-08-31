package store_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/testpg"
)

var testKEK = []byte("test-wrapped-kek") // opaque to the store; real KEKs arrive with vault tests

func TestTenantRoundTrip(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()

	tn, err := s.CreateTenant(ctx, "acme", testKEK)
	require.NoError(t, err)
	require.Equal(t, "acme", tn.Name)
	require.NotEqual(t, uuid.Nil, tn.ID)

	got, err := s.GetTenant(ctx, tn.ID)
	require.NoError(t, err)
	require.Equal(t, tn.ID, got.ID)

	wrapped, version, err := s.TenantKEK(ctx, tn.ID)
	require.NoError(t, err)
	require.Equal(t, testKEK, wrapped)
	require.Equal(t, 1, version)
}

func TestGetTenantNotFound(t *testing.T) {
	s := store.New(testpg.New(t))
	_, err := s.GetTenant(context.Background(), uuid.New())
	require.ErrorIs(t, err, store.ErrNotFound)
}
