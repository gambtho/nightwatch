package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/testpg"
)

func newUser(t *testing.T) (*pgxpool.Pool, *store.Store, store.Tenant, store.User) {
	t.Helper()
	pool := testpg.New(t)
	s := store.New(pool)
	ctx := context.Background()
	tn, err := s.CreateTenant(ctx, "acme", testKEK)
	require.NoError(t, err)
	u, err := s.UpsertUser(ctx, tn.ID, "pat@acme.test")
	require.NoError(t, err)
	return pool, s, tn, u
}

func TestSessionUserJoinsCurrentRole(t *testing.T) {
	_, s, tn, u := newUser(t)
	ctx := context.Background()

	_, err := s.CreateSession(ctx, hash(1), tn.ID, u.ID)
	require.NoError(t, err)

	userID, tenantID, role, err := s.SessionUser(ctx, hash(1))
	require.NoError(t, err)
	require.Equal(t, u.ID, userID)
	require.Equal(t, tn.ID, tenantID)
	require.Equal(t, "owner", role, "role comes from app_user at request time")
}

func TestSessionUserRejectsUnknownToken(t *testing.T) {
	_, s, _, _ := newUser(t)
	_, _, _, err := s.SessionUser(context.Background(), hash(9))
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestSessionGoneWhenUserDeleted(t *testing.T) {
	pool, s, tn, u := newUser(t)
	ctx := context.Background()

	_, err := s.CreateSession(ctx, hash(1), tn.ID, u.ID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM app_user WHERE id = $1`, u.ID)
	require.NoError(t, err)

	_, _, _, err = s.SessionUser(ctx, hash(1))
	require.ErrorIs(t, err, store.ErrNotFound, "a session whose user row is gone is rejected")
}

func TestSessionCompositeFKRejectsCrossTenantPair(t *testing.T) {
	_, s, _, u := newUser(t)
	ctx := context.Background()

	other, err := s.CreateTenant(ctx, "bloom", testKEK)
	require.NoError(t, err)
	_, err = s.CreateSession(ctx, hash(1), other.ID, u.ID)
	require.Error(t, err, "user-A/tenant-B session must be a database error")
}

func TestSessionIdleAndAbsoluteExpiry(t *testing.T) {
	pool, s, tn, u := newUser(t)
	ctx := context.Background()

	// Idle: last seen just over 7 days ago inside the absolute cap.
	_, err := s.CreateSession(ctx, hash(1), tn.ID, u.ID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`UPDATE session SET last_seen_at = now() - interval '8 days' WHERE token_hash = $1`, hash(1))
	require.NoError(t, err)
	_, _, _, err = s.SessionUser(ctx, hash(1))
	require.ErrorIs(t, err, store.ErrNotFound, "idle timeout")

	// Absolute: recently seen but past the 30-day cap.
	_, err = s.CreateSession(ctx, hash(2), tn.ID, u.ID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`UPDATE session SET expires_at = now() - interval '1 minute' WHERE token_hash = $1`, hash(2))
	require.NoError(t, err)
	_, _, _, err = s.SessionUser(ctx, hash(2))
	require.ErrorIs(t, err, store.ErrNotFound, "absolute cap")
}

func TestSessionTouchThrottled(t *testing.T) {
	pool, s, tn, u := newUser(t)
	ctx := context.Background()

	_, err := s.CreateSession(ctx, hash(1), tn.ID, u.ID)
	require.NoError(t, err)

	// A fresh session (last_seen_at = now) is not touched again.
	var before time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT last_seen_at FROM session WHERE token_hash = $1`, hash(1)).Scan(&before))
	_, _, _, err = s.SessionUser(ctx, hash(1))
	require.NoError(t, err)
	var after time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT last_seen_at FROM session WHERE token_hash = $1`, hash(1)).Scan(&after))
	require.True(t, after.Equal(before), "recent sessions are not re-touched")

	// A stale-but-live session is touched on lookup.
	_, err = pool.Exec(ctx,
		`UPDATE session SET last_seen_at = now() - interval '2 hours' WHERE token_hash = $1`, hash(1))
	require.NoError(t, err)
	_, _, _, err = s.SessionUser(ctx, hash(1))
	require.NoError(t, err)
	require.NoError(t, pool.QueryRow(ctx, `SELECT last_seen_at FROM session WHERE token_hash = $1`, hash(1)).Scan(&after))
	require.Less(t, time.Since(after), time.Minute, "hour-old last_seen_at is refreshed")
}

func TestDeleteSessionAndLogoutEverywhere(t *testing.T) {
	_, s, tn, u := newUser(t)
	ctx := context.Background()

	_, err := s.CreateSession(ctx, hash(1), tn.ID, u.ID)
	require.NoError(t, err)
	_, err = s.CreateSession(ctx, hash(2), tn.ID, u.ID)
	require.NoError(t, err)

	require.NoError(t, s.DeleteSession(ctx, hash(1)))
	_, _, _, err = s.SessionUser(ctx, hash(1))
	require.ErrorIs(t, err, store.ErrNotFound)
	_, _, _, err = s.SessionUser(ctx, hash(2))
	require.NoError(t, err)

	require.NoError(t, s.DeleteUserSessions(ctx, tn.ID, u.ID))
	_, _, _, err = s.SessionUser(ctx, hash(2))
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestUserByEmail(t *testing.T) {
	_, s, _, u := newUser(t)
	ctx := context.Background()

	got, err := s.UserByEmail(ctx, " PAT@acme.test ")
	require.NoError(t, err)
	require.Equal(t, u.ID, got.ID)

	_, err = s.UserByEmail(ctx, "nobody@acme.test")
	require.ErrorIs(t, err, store.ErrNotFound)
}
