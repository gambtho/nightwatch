package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Tenant struct {
	ID              uuid.UUID
	Name            string
	CreatedAt       time.Time
	// MonthlyCapCents is the user's local budget: how much Tomte may
	// spend from their key per month. Same enforcement as ever (meter
	// pre-call check, scheduler skip); Tomte meters only what goes
	// through Tomte. nil = the serve-wide default.
	MonthlyCapCents *int
}

// CreateTenant inserts the tenant and its wrapped KEK in one transaction:
// a tenant without a KEK cannot hold secrets, so the two are born together.
func (s *Store) CreateTenant(ctx context.Context, name string, wrappedKEK []byte) (Tenant, error) {
	var t Tenant
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return t, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if t, err = createTenant(ctx, tx, name, wrappedKEK); err != nil {
		return t, err
	}
	return t, tx.Commit(ctx)
}

func createTenant(ctx context.Context, q querier, name string, wrappedKEK []byte) (Tenant, error) {
	var t Tenant
	err := q.QueryRow(ctx,
		`INSERT INTO tenant (name) VALUES ($1) RETURNING id, name, created_at, monthly_cap_cents`,
		name,
	).Scan(&t.ID, &t.Name, &t.CreatedAt, &t.MonthlyCapCents)
	if err != nil {
		return t, err
	}
	_, err = q.Exec(ctx,
		`INSERT INTO tenant_kek (tenant_id, wrapped_kek) VALUES ($1, $2)`,
		t.ID, wrappedKEK)
	return t, err
}

// TenantKEK returns the current (highest-version) KEK — the encrypt path.
func (s *Store) TenantKEK(ctx context.Context, tenantID uuid.UUID) ([]byte, int, error) {
	var wrapped []byte
	var version int
	err := s.pool.QueryRow(ctx,
		`SELECT wrapped_kek, version FROM tenant_kek
		 WHERE tenant_id = $1 ORDER BY version DESC LIMIT 1`,
		tenantID,
	).Scan(&wrapped, &version)
	return wrapped, version, notFound(err)
}

// TenantKEKAt returns a specific KEK version — the decrypt path, driven by
// connection.kek_version, which keeps rotation resumable.
func (s *Store) TenantKEKAt(ctx context.Context, tenantID uuid.UUID, version int) ([]byte, error) {
	var wrapped []byte
	err := s.pool.QueryRow(ctx,
		`SELECT wrapped_kek FROM tenant_kek WHERE tenant_id = $1 AND version = $2`,
		tenantID, version,
	).Scan(&wrapped)
	return wrapped, notFound(err)
}

func (s *Store) GetTenant(ctx context.Context, id uuid.UUID) (Tenant, error) {
	var t Tenant
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, created_at, monthly_cap_cents FROM tenant WHERE id = $1`,
		id,
	).Scan(&t.ID, &t.Name, &t.CreatedAt, &t.MonthlyCapCents)
	return t, notFound(err)
}

func (s *Store) SetTenantMonthlyCap(ctx context.Context, tenantID uuid.UUID, capCents *int) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE tenant SET monthly_cap_cents = $2 WHERE id = $1`,
		tenantID, capCents)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
