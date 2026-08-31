package store_test

import (
	"context"
	"testing"
	"time"

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

func TestLoginTokenLifecycle(t *testing.T) {
	pool := testpg.New(t)
	s := store.New(pool)
	ctx := context.Background()

	// Fresh token: valid, counted against the email's budget.
	require.NoError(t, s.CreateLoginToken(ctx, hash(1), "pat@acme.test", nil, time.Now().Add(15*time.Minute)))
	ok, err := s.LoginTokenValid(ctx, hash(1))
	require.NoError(t, err)
	require.True(t, ok)

	// Expired token: invalid, not counted.
	require.NoError(t, s.CreateLoginToken(ctx, hash(2), "pat@acme.test", nil, time.Now().Add(-time.Minute)))
	ok, err = s.LoginTokenValid(ctx, hash(2))
	require.NoError(t, err)
	require.False(t, ok)

	// Consumed token: invalid, not counted.
	require.NoError(t, s.CreateLoginToken(ctx, hash(3), "pat@acme.test", nil, time.Now().Add(15*time.Minute)))
	_, err = pool.Exec(ctx, `UPDATE login_token SET consumed_at = now() WHERE token_hash = $1`, hash(3))
	require.NoError(t, err)
	ok, err = s.LoginTokenValid(ctx, hash(3))
	require.NoError(t, err)
	require.False(t, ok)

	// Unknown token: invalid, no error.
	ok, err = s.LoginTokenValid(ctx, hash(9))
	require.NoError(t, err)
	require.False(t, ok)

	n, err := s.CountActiveLoginTokens(ctx, "pat@acme.test")
	require.NoError(t, err)
	require.Equal(t, 1, n)
}

func TestCreateLoginTokenSweepsLongExpired(t *testing.T) {
	pool := testpg.New(t)
	s := store.New(pool)
	ctx := context.Background()

	require.NoError(t, s.CreateLoginToken(ctx, hash(1), "old@acme.test", nil, time.Now().Add(15*time.Minute)))
	_, err := pool.Exec(ctx, `UPDATE login_token SET expires_at = now() - interval '25 hours' WHERE token_hash = $1`, hash(1))
	require.NoError(t, err)

	require.NoError(t, s.CreateLoginToken(ctx, hash(2), "new@acme.test", nil, time.Now().Add(15*time.Minute)))

	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM login_token`).Scan(&n))
	require.Equal(t, 1, n, "long-expired rows are swept opportunistically")
}

func signup() store.NewSignup {
	return store.NewSignup{WrappedKEK: testKEK}
}

func TestConsumeLoginTokenFirstLoginMintsTenant(t *testing.T) {
	pool := testpg.New(t)
	s := store.New(pool)
	ctx := context.Background()

	next := "/runs/42"
	require.NoError(t, s.CreateLoginToken(ctx, hash(1), "pat@acme.test", &next, time.Now().Add(15*time.Minute)))

	res, err := s.ConsumeLoginToken(ctx, hash(1), hash(101), signup())
	require.NoError(t, err)
	require.True(t, res.FirstLogin)
	require.Equal(t, "pat", res.Tenant.Name)
	require.Equal(t, "pat@acme.test", res.User.Email)
	require.Equal(t, "owner", res.User.Role)
	require.NotNil(t, res.NextPath)
	require.Equal(t, "/runs/42", *res.NextPath)

	// Tenant + KEK + user + consumed token + session all landed.
	wrapped, _, err := s.TenantKEK(ctx, res.Tenant.ID)
	require.NoError(t, err)
	require.Equal(t, testKEK, wrapped)
	var sessions int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM session WHERE token_hash = $1`, hash(101)).Scan(&sessions))
	require.Equal(t, 1, sessions)
	ok, err := s.LoginTokenValid(ctx, hash(1))
	require.NoError(t, err)
	require.False(t, ok, "token is consumed")
}

func TestConsumeLoginTokenReturningLoginReusesTenant(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()

	tn, err := s.CreateTenant(ctx, "acme", testKEK)
	require.NoError(t, err)
	u, err := s.UpsertUser(ctx, tn.ID, "pat@acme.test")
	require.NoError(t, err)

	require.NoError(t, s.CreateLoginToken(ctx, hash(1), "Pat@Acme.test", nil, time.Now().Add(15*time.Minute)))
	res, err := s.ConsumeLoginToken(ctx, hash(1), hash(101), signup())
	require.NoError(t, err)
	require.False(t, res.FirstLogin)
	require.Equal(t, tn.ID, res.Tenant.ID)
	require.Equal(t, u.ID, res.User.ID)
	require.Nil(t, res.NextPath)
}

func TestConsumeLoginTokenRejectsExpiredAndReused(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()

	require.NoError(t, s.CreateLoginToken(ctx, hash(1), "pat@acme.test", nil, time.Now().Add(-time.Minute)))
	_, err := s.ConsumeLoginToken(ctx, hash(1), hash(101), signup())
	require.ErrorIs(t, err, store.ErrLoginTokenInvalid)

	require.NoError(t, s.CreateLoginToken(ctx, hash(2), "pat@acme.test", nil, time.Now().Add(15*time.Minute)))
	_, err = s.ConsumeLoginToken(ctx, hash(2), hash(102), signup())
	require.NoError(t, err)
	_, err = s.ConsumeLoginToken(ctx, hash(2), hash(103), signup())
	require.ErrorIs(t, err, store.ErrLoginTokenInvalid)
}

func TestConsumeLoginTokenSingleUseUnderConcurrency(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()

	require.NoError(t, s.CreateLoginToken(ctx, hash(1), "pat@acme.test", nil, time.Now().Add(15*time.Minute)))

	errs := make(chan error, 2)
	for i := byte(0); i < 2; i++ {
		go func(i byte) {
			_, err := s.ConsumeLoginToken(ctx, hash(1), hash(101+i), signup())
			errs <- err
		}(i)
	}
	var wins, losses int
	for i := 0; i < 2; i++ {
		switch err := <-errs; {
		case err == nil:
			wins++
		default:
			require.ErrorIs(t, err, store.ErrLoginTokenInvalid)
			losses++
		}
	}
	require.Equal(t, 1, wins, "exactly one concurrent verify wins")
	require.Equal(t, 1, losses)
}

func TestConsumeLoginTokenRollsBackWholeSignup(t *testing.T) {
	pool := testpg.New(t)
	s := store.New(pool)
	ctx := context.Background()

	// Occupy the session token hash so the final insert of the signup
	// transaction fails, then prove nothing else survived either.
	tn, err := s.CreateTenant(ctx, "occupied", testKEK)
	require.NoError(t, err)
	u, err := s.UpsertUser(ctx, tn.ID, "other@acme.test")
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`INSERT INTO session (token_hash, tenant_id, user_id, expires_at)
		 VALUES ($1, $2, $3, now() + interval '1 day')`,
		hash(101), tn.ID, u.ID)
	require.NoError(t, err)

	require.NoError(t, s.CreateLoginToken(ctx, hash(1), "pat@acme.test", nil, time.Now().Add(15*time.Minute)))
	_, err = s.ConsumeLoginToken(ctx, hash(1), hash(101), signup())
	require.Error(t, err)

	var tenants, users int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM tenant`).Scan(&tenants))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM app_user`).Scan(&users))
	require.Equal(t, 1, tenants, "no orphaned tenant")
	require.Equal(t, 1, users, "no orphaned user")
	ok, err := s.LoginTokenValid(ctx, hash(1))
	require.NoError(t, err)
	require.True(t, ok, "token consumption rolled back; the link stays valid")
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
