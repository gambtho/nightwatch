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
// that accepts an email (UpsertUser, UserByEmail, dev-session)
// applies it before touching the store, so lookup, conflict target, and
// the global unique index all agree.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// GetUser fetches one user within a tenant.
func (s *Store) GetUser(ctx context.Context, tenantID, userID uuid.UUID) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		`SELECT id, tenant_id, email, role FROM app_user WHERE tenant_id = $1 AND id = $2`,
		tenantID, userID,
	).Scan(&u.ID, &u.TenantID, &u.Email, &u.Role)
	return u, notFound(err)
}

// UserByEmail resolves a normalized email to its (single, globally unique)
// user — the login-resolution and dev-session lookup.
func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		`SELECT id, tenant_id, email, role FROM app_user WHERE lower(email) = $1`,
		NormalizeEmail(email),
	).Scan(&u.ID, &u.TenantID, &u.Email, &u.Role)
	return u, notFound(err)
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
