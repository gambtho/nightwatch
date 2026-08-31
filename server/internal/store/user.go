package store

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

type User struct {
	ID       uuid.UUID
	TenantID uuid.UUID
	Email    string
	Role     string
}

// NormalizeEmail is the one canonical email representation: every boundary
// that accepts an email (magic-link request, UpsertUser, dev-session)
// applies it before touching the store, so lookup, conflict target, and
// the global unique index all agree.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func (s *Store) UpsertUser(ctx context.Context, tenantID uuid.UUID, email string) (User, error) {
	return upsertUser(ctx, s.pool, tenantID, email)
}

func upsertUser(ctx context.Context, q querier, tenantID uuid.UUID, email string) (User, error) {
	var u User
	err := q.QueryRow(ctx, `
		INSERT INTO app_user (tenant_id, email) VALUES ($1, $2)
		ON CONFLICT (tenant_id, email) DO UPDATE SET email = EXCLUDED.email
		RETURNING id, tenant_id, email, role`,
		tenantID, NormalizeEmail(email),
	).Scan(&u.ID, &u.TenantID, &u.Email, &u.Role)
	return u, err
}
