package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Session lifetime: a 7-day idle timeout inside a 30-day absolute cap.
// The absolute cap is stamped into expires_at at insert; the idle window
// is evaluated against last_seen_at at lookup.
const (
	sessionIdleWindow  = "7 days"
	sessionAbsoluteCap = "30 days"
)

type Session struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	TenantID   uuid.UUID
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

// CreateSession inserts a session row for an existing user. The composite
// FK makes a cross-tenant (tenantID, userID) pair a database error.
func (s *Store) CreateSession(ctx context.Context, tokenHash []byte, tenantID, userID uuid.UUID) (Session, error) {
	return createSession(ctx, s.pool, tokenHash, tenantID, userID)
}

func createSession(ctx context.Context, q querier, tokenHash []byte, tenantID, userID uuid.UUID) (Session, error) {
	var sess Session
	err := q.QueryRow(ctx,
		`INSERT INTO session (token_hash, tenant_id, user_id, expires_at)
		 VALUES ($1, $2, $3, now() + interval '`+sessionAbsoluteCap+`')
		 RETURNING id, user_id, tenant_id, created_at, last_seen_at, expires_at`,
		tokenHash, tenantID, userID,
	).Scan(&sess.ID, &sess.UserID, &sess.TenantID, &sess.CreatedAt, &sess.LastSeenAt, &sess.ExpiresAt)
	return sess, err
}
