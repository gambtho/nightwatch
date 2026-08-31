package store

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrHandoffTokenInvalid: the conditional claim matched no row — the token
// is unknown, expired, or already consumed. One error on purpose: the
// handoff page renders the same refusal for all three.
var ErrHandoffTokenInvalid = errors.New("store: handoff token expired or already used")

// CreateHandoffToken records the hash of a single-use browser-handoff
// token for an existing user. It opportunistically sweeps rows expired
// for more than an hour — cleanup on the write path, no background loop.
func (s *Store) CreateHandoffToken(ctx context.Context, tokenHash []byte, tenantID, userID uuid.UUID, ttl time.Duration) error {
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM handoff_token WHERE expires_at < now() - interval '1 hour'`); err != nil {
		// Best-effort for real: a failed sweep must not block the mint.
		slog.Error("store: handoff sweep", "err", err)
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO handoff_token (token_hash, tenant_id, user_id, expires_at)
		 VALUES ($1, $2, $3, now() + $4)`,
		tokenHash, tenantID, userID, ttl)
	return err
}

// ConsumeHandoffToken is the browser-handoff exchange: conditionally claim
// the token (single-use even under concurrent exchanges — the UPDATE's row
// lock serializes the loser, whose re-checked WHERE sees consumed_at set)
// and insert the session row in the same transaction. On rollback the
// token remains valid and the browser retries by reloading.
func (s *Store) ConsumeHandoffToken(ctx context.Context, tokenHash, sessionTokenHash []byte) (tenantID, userID uuid.UUID, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return tenantID, userID, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	err = tx.QueryRow(ctx,
		`UPDATE handoff_token SET consumed_at = now()
		 WHERE token_hash = $1 AND consumed_at IS NULL AND expires_at > now()
		 RETURNING tenant_id, user_id`,
		tokenHash,
	).Scan(&tenantID, &userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return tenantID, userID, ErrHandoffTokenInvalid
	}
	if err != nil {
		return tenantID, userID, err
	}

	if err = createSession(ctx, tx, sessionTokenHash, tenantID, userID); err != nil {
		return tenantID, userID, err
	}
	return tenantID, userID, tx.Commit(ctx)
}
