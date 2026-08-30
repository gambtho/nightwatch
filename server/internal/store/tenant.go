package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Tenant struct {
	ID        uuid.UUID
	Name      string
	CreatedAt time.Time
}

func (s *Store) CreateTenant(ctx context.Context, name string) (Tenant, error) {
	var t Tenant
	err := s.pool.QueryRow(ctx,
		`INSERT INTO tenant (name) VALUES ($1) RETURNING id, name, created_at`,
		name,
	).Scan(&t.ID, &t.Name, &t.CreatedAt)
	return t, err
}

func (s *Store) GetTenant(ctx context.Context, id uuid.UUID) (Tenant, error) {
	var t Tenant
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, created_at FROM tenant WHERE id = $1`,
		id,
	).Scan(&t.ID, &t.Name, &t.CreatedAt)
	return t, notFound(err)
}
