package store

import (
	"context"

	"github.com/google/uuid"
)

type User struct {
	ID       uuid.UUID
	TenantID uuid.UUID
	Email    string
	Role     string
}

func (s *Store) UpsertUser(ctx context.Context, tenantID uuid.UUID, email string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		INSERT INTO app_user (tenant_id, email) VALUES ($1, $2)
		ON CONFLICT (tenant_id, email) DO UPDATE SET email = EXCLUDED.email
		RETURNING id, tenant_id, email, role`,
		tenantID, email,
	).Scan(&u.ID, &u.TenantID, &u.Email, &u.Role)
	return u, err
}
