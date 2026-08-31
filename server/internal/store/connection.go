package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Connection is a stored credential. The value is present only as
// ciphertext; decryption belongs to the vault/proxyadapter layer, on the
// proxy's request path.
type Connection struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	Name       string
	Kind       string
	Provider   string
	DEKWrapped []byte
	Ciphertext []byte
	Nonce      []byte
	KEKVersion int
	// Status is "ok" or "needs_reauth" — a pasted token can be revoked
	// upstream; a 401 demotes it (MarkConnectionNeedsReauth) and a
	// re-paste (UpsertConnection) restores it.
	Status     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	LastUsedAt *time.Time
}

const connectionCols = `id, tenant_id, name, kind, provider, dek_wrapped,
	ciphertext, nonce, kek_version, status, created_at, updated_at, last_used_at`

func scanConnection(row pgx.Row) (Connection, error) {
	var c Connection
	err := row.Scan(&c.ID, &c.TenantID, &c.Name, &c.Kind, &c.Provider,
		&c.DEKWrapped, &c.Ciphertext, &c.Nonce, &c.KEKVersion,
		&c.Status, &c.CreatedAt, &c.UpdatedAt, &c.LastUsedAt)
	return c, notFound(err)
}

func (s *Store) UpsertConnection(ctx context.Context, tenantID uuid.UUID, name, kind, provider string, dekWrapped, ciphertext, nonce []byte, kekVersion int) (Connection, error) {
	return scanConnection(s.pool.QueryRow(ctx,
		`INSERT INTO connection
		   (tenant_id, name, kind, provider, dek_wrapped, ciphertext, nonce, kek_version)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT (tenant_id, provider, name) DO UPDATE SET
		   kind = EXCLUDED.kind,
		   dek_wrapped = EXCLUDED.dek_wrapped,
		   ciphertext = EXCLUDED.ciphertext,
		   nonce = EXCLUDED.nonce,
		   kek_version = EXCLUDED.kek_version,
		   status = 'ok',
		   updated_at = now()
		 RETURNING `+connectionCols,
		tenantID, name, kind, provider, dekWrapped, ciphertext, nonce, kekVersion))
}

func (s *Store) GetConnection(ctx context.Context, tenantID uuid.UUID, provider, name string) (Connection, error) {
	return scanConnection(s.pool.QueryRow(ctx,
		`SELECT `+connectionCols+` FROM connection
		 WHERE tenant_id = $1 AND provider = $2 AND name = $3`,
		tenantID, provider, name))
}

func (s *Store) ListConnections(ctx context.Context, tenantID uuid.UUID) ([]Connection, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+connectionCols+` FROM connection
		 WHERE tenant_id = $1 ORDER BY provider, name`,
		tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Connection
	for rows.Next() {
		c, err := scanConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) DeleteConnection(ctx context.Context, tenantID uuid.UUID, provider, name string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM connection WHERE tenant_id = $1 AND provider = $2 AND name = $3`,
		tenantID, provider, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkConnectionNeedsReauth demotes a connection after an upstream 401 —
// a pasted token revoked at the provider. It reports whether the demotion
// applied (the row was still "ok"); a re-paste restores the status.
func (s *Store) MarkConnectionNeedsReauth(ctx context.Context, tenantID, id uuid.UUID) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE connection SET status = 'needs_reauth', updated_at = now()
		 WHERE id = $1 AND tenant_id = $2 AND status = 'ok'`,
		id, tenantID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (s *Store) TouchConnection(ctx context.Context, tenantID, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE connection SET last_used_at = now() WHERE id = $1 AND tenant_id = $2`,
		id, tenantID)
	return err
}
