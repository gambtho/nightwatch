package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/testpg"
)

func TestConnectionLifecycle(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, err := s.CreateTenant(ctx, "acme", testKEK)
	require.NoError(t, err)

	c, err := s.UpsertConnection(ctx, tn.ID, "default", "llm_api_key", "anthropic",
		[]byte("dek"), []byte("ct"), []byte("nonce"), 1)
	require.NoError(t, err)
	require.Equal(t, "anthropic", c.Provider)
	require.Nil(t, c.LastUsedAt)

	// Upsert replaces the secret material for the same (provider, name).
	c2, err := s.UpsertConnection(ctx, tn.ID, "default", "llm_api_key", "anthropic",
		[]byte("dek2"), []byte("ct2"), []byte("nonce2"), 1)
	require.NoError(t, err)
	require.Equal(t, c.ID, c2.ID)
	require.Equal(t, []byte("ct2"), c2.Ciphertext)

	// Same name under a different provider is a separate connection.
	_, err = s.UpsertConnection(ctx, tn.ID, "default", "llm_api_key", "openai",
		[]byte("dek"), []byte("ct"), []byte("nonce"), 1)
	require.NoError(t, err)

	got, err := s.GetConnection(ctx, tn.ID, "anthropic", "default")
	require.NoError(t, err)
	require.Equal(t, []byte("ct2"), got.Ciphertext)

	list, err := s.ListConnections(ctx, tn.ID)
	require.NoError(t, err)
	require.Len(t, list, 2)

	require.NoError(t, s.TouchConnection(ctx, tn.ID, got.ID))
	got, err = s.GetConnection(ctx, tn.ID, "anthropic", "default")
	require.NoError(t, err)
	require.NotNil(t, got.LastUsedAt)

	require.NoError(t, s.DeleteConnection(ctx, tn.ID, "anthropic", "default"))
	_, err = s.GetConnection(ctx, tn.ID, "anthropic", "default")
	require.ErrorIs(t, err, store.ErrNotFound)
	require.ErrorIs(t, s.DeleteConnection(ctx, tn.ID, "anthropic", "default"), store.ErrNotFound)
}

func TestConnectionCrossTenantIsolation(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tnA, err := s.CreateTenant(ctx, "a", testKEK)
	require.NoError(t, err)
	tnB, err := s.CreateTenant(ctx, "b", testKEK)
	require.NoError(t, err)

	_, err = s.UpsertConnection(ctx, tnA.ID, "default", "llm_api_key", "anthropic",
		[]byte("dek"), []byte("ct"), []byte("nonce"), 1)
	require.NoError(t, err)

	_, err = s.GetConnection(ctx, tnB.ID, "anthropic", "default")
	require.ErrorIs(t, err, store.ErrNotFound)
	require.ErrorIs(t, s.DeleteConnection(ctx, tnB.ID, "anthropic", "default"), store.ErrNotFound)
	list, err := s.ListConnections(ctx, tnB.ID)
	require.NoError(t, err)
	require.Empty(t, list)
}
