package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/tomte/server/internal/store"
	"github.com/gambtho/tomte/server/internal/testpg"
)

func TestHandoffTokenConsumeOnce(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, err := s.CreateTenant(ctx, "local", testKEK)
	require.NoError(t, err)
	u, err := s.UpsertUser(ctx, tn.ID, "owner@tomte.local")
	require.NoError(t, err)

	tok := hash(1)
	require.NoError(t, s.CreateHandoffToken(ctx, tok, tn.ID, u.ID, time.Minute))

	tenantID, userID, err := s.ConsumeHandoffToken(ctx, tok, hash(2))
	require.NoError(t, err)
	require.Equal(t, tn.ID, tenantID)
	require.Equal(t, u.ID, userID)

	// The minted session authenticates.
	gotUser, gotTenant, role, err := s.SessionUser(ctx, hash(2))
	require.NoError(t, err)
	require.Equal(t, u.ID, gotUser)
	require.Equal(t, tn.ID, gotTenant)
	require.Equal(t, "owner", role)

	// Second consume loses, and mints no second session.
	_, _, err = s.ConsumeHandoffToken(ctx, tok, hash(3))
	require.ErrorIs(t, err, store.ErrHandoffTokenInvalid)
	_, _, _, err = s.SessionUser(ctx, hash(3))
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestHandoffTokenExpiredAndUnknown(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, err := s.CreateTenant(ctx, "local", testKEK)
	require.NoError(t, err)
	u, err := s.UpsertUser(ctx, tn.ID, "owner@tomte.local")
	require.NoError(t, err)

	// Expired at birth.
	tok := hash(4)
	require.NoError(t, s.CreateHandoffToken(ctx, tok, tn.ID, u.ID, -time.Second))
	_, _, err = s.ConsumeHandoffToken(ctx, tok, hash(5))
	require.ErrorIs(t, err, store.ErrHandoffTokenInvalid)

	// Unknown token: same single error.
	_, _, err = s.ConsumeHandoffToken(ctx, hash(6), hash(7))
	require.ErrorIs(t, err, store.ErrHandoffTokenInvalid)
}
