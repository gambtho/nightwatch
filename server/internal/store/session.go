package store

import (
	"context"

	"github.com/google/uuid"
)

// Session lifetime: a 7-day idle timeout inside a 30-day absolute cap.
// The absolute cap is stamped into expires_at at insert; the idle window
// is evaluated against last_seen_at at lookup.
const (
	sessionIdleWindow  = "7 days"
	sessionAbsoluteCap = "30 days"
)

// CreateSession inserts a session row for an existing user. The composite
// FK makes a cross-tenant (tenantID, userID) pair a database error.
func (s *Store) CreateSession(ctx context.Context, tokenHash []byte, tenantID, userID uuid.UUID) error {
	return createSession(ctx, s.pool, tokenHash, tenantID, userID)
}

// SessionUser resolves a session token hash to its user, tenant, and the
// user's CURRENT role — role is never persisted on the session row; the
// join reads it from app_user so a role change takes effect on the next
// request, and a session whose user row is gone matches nothing → 401.
// The CTE touches last_seen_at at most once per hour, and only for a
// session still inside both expiry windows — a rejected lookup must not
// refresh (and thereby resurrect) an expired session. The touch is not
// visible to the SELECT's snapshot, which is fine: the pre-touch value
// still satisfies the idle window whenever the touch fired.
func (s *Store) SessionUser(ctx context.Context, tokenHash []byte) (userID, tenantID uuid.UUID, role string, err error) {
	err = s.pool.QueryRow(ctx, `
		WITH touched AS (
		    UPDATE session SET last_seen_at = now()
		    WHERE token_hash = $1
		      AND last_seen_at < now() - interval '1 hour'
		      AND last_seen_at > now() - interval '`+sessionIdleWindow+`'
		      AND expires_at > now()
		)
		SELECT se.user_id, se.tenant_id, u.role
		FROM session se
		JOIN app_user u ON (u.tenant_id, u.id) = (se.tenant_id, se.user_id)
		WHERE se.token_hash = $1
		  AND se.last_seen_at > now() - interval '`+sessionIdleWindow+`'
		  AND se.expires_at > now()`,
		tokenHash,
	).Scan(&userID, &tenantID, &role)
	return userID, tenantID, role, notFound(err)
}

// DeleteSession removes one session (logout). Deleting an already-gone
// session is not an error — logout is idempotent.
func (s *Store) DeleteSession(ctx context.Context, tokenHash []byte) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM session WHERE token_hash = $1`, tokenHash)
	return err
}

// DeleteUserSessions removes every session for a user — the
// "log out everywhere" and support-revocation lever.
func (s *Store) DeleteUserSessions(ctx context.Context, tenantID, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM session WHERE tenant_id = $1 AND user_id = $2`, tenantID, userID)
	return err
}

func createSession(ctx context.Context, q querier, tokenHash []byte, tenantID, userID uuid.UUID) error {
	_, err := q.Exec(ctx,
		`INSERT INTO session (token_hash, tenant_id, user_id, expires_at)
		 VALUES ($1, $2, $3, now() + interval '`+sessionAbsoluteCap+`')`,
		tokenHash, tenantID, userID)
	return err
}
