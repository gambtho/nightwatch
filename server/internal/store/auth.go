package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrLoginTokenInvalid: the conditional claim matched no row — the token is
// unknown, expired, or already consumed. One error on purpose: the verify
// page renders the same friendly "request a new link" for all three.
var ErrLoginTokenInvalid = errors.New("store: login token expired or already used")

// NewSignup carries what a first login needs to mint a tenant. The caller
// pre-generates the wrapped KEK so the store never depends on the vault;
// it goes unused (harmlessly) when the email already has a tenant. The
// tenant is named from the email's local part — the user is never asked
// to name a workspace.
type NewSignup struct {
	WrappedKEK []byte
}

func tenantNameFromEmail(email string) string {
	if i := strings.Index(email, "@"); i > 0 {
		return email[:i]
	}
	return email
}

type ConsumeResult struct {
	User       User
	Tenant     Tenant
	Session    Session
	NextPath   *string
	FirstLogin bool
}

// ConsumeLoginToken is the verify transaction: conditionally claim the
// token (single-use even under concurrent verifies — the UPDATE's row lock
// serializes the loser, whose re-checked WHERE sees consumed_at set),
// resolve the email to a user or mint tenant + KEK + owner, and insert the
// session row. Everything commits or rolls back as one unit; on rollback
// the link remains valid and the user retries by clicking it again.
func (s *Store) ConsumeLoginToken(ctx context.Context, tokenHash, sessionTokenHash []byte, signup NewSignup) (ConsumeResult, error) {
	var res ConsumeResult
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var email string
	err = tx.QueryRow(ctx,
		`UPDATE login_token SET consumed_at = now()
		 WHERE token_hash = $1 AND consumed_at IS NULL AND expires_at > now()
		 RETURNING email, next_path`,
		tokenHash,
	).Scan(&email, &res.NextPath)
	if errors.Is(err, pgx.ErrNoRows) {
		return res, ErrLoginTokenInvalid
	}
	if err != nil {
		return res, err
	}

	err = tx.QueryRow(ctx,
		`SELECT id, tenant_id, email, role FROM app_user WHERE lower(email) = $1`,
		email,
	).Scan(&res.User.ID, &res.User.TenantID, &res.User.Email, &res.User.Role)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		res.FirstLogin = true
		if res.Tenant, err = createTenant(ctx, tx, tenantNameFromEmail(email), signup.WrappedKEK); err != nil {
			return res, err
		}
		if res.User, err = upsertUser(ctx, tx, res.Tenant.ID, email); err != nil {
			return res, err
		}
	case err != nil:
		return res, err
	default:
		err = tx.QueryRow(ctx,
			`SELECT id, name, created_at, monthly_cap_cents FROM tenant WHERE id = $1`,
			res.User.TenantID,
		).Scan(&res.Tenant.ID, &res.Tenant.Name, &res.Tenant.CreatedAt, &res.Tenant.MonthlyCapCents)
		if err != nil {
			return res, err
		}
	}

	if res.Session, err = createSession(ctx, tx, sessionTokenHash, res.Tenant.ID, res.User.ID); err != nil {
		return res, err
	}
	return res, tx.Commit(ctx)
}

// CreateLoginToken records the hash of an outstanding magic-link token. It
// also opportunistically sweeps rows expired for more than a day — the
// spec's cleanup job, done on the write path instead of a loop.
func (s *Store) CreateLoginToken(ctx context.Context, tokenHash []byte, email string, nextPath *string, expiresAt time.Time) error {
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM login_token WHERE expires_at < now() - interval '24 hours'`); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO login_token (token_hash, email, next_path, expires_at)
		 VALUES ($1, $2, $3, $4)`,
		tokenHash, NormalizeEmail(email), nextPath, expiresAt)
	return err
}

// LoginTokenValid reports whether the token exists, unconsumed and
// unexpired. It is the interstitial GET's read-only check; it consumes
// nothing.
func (s *Store) LoginTokenValid(ctx context.Context, tokenHash []byte) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (
		    SELECT 1 FROM login_token
		    WHERE token_hash = $1 AND consumed_at IS NULL AND expires_at > now())`,
		tokenHash,
	).Scan(&ok)
	return ok, err
}

// CountActiveLoginTokens counts outstanding (unconsumed, unexpired) tokens
// for an email — the per-email rate budget.
func (s *Store) CountActiveLoginTokens(ctx context.Context, email string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM login_token
		 WHERE email = $1 AND consumed_at IS NULL AND expires_at > now()`,
		NormalizeEmail(email),
	).Scan(&n)
	return n, err
}
