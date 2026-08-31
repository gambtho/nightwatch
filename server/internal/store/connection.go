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
	// Metadata is the non-secret face of the credential (granted
	// scopes, account label) — what GET /v1/connections may show.
	Metadata []byte
	// Status is "ok" or "needs_reauth"; Epoch increments on every
	// bundle write and gates needs_reauth demotion (see
	// MarkConnectionNeedsReauth).
	Status     string
	Epoch      int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
	LastUsedAt *time.Time
}

const connectionCols = `id, tenant_id, name, kind, provider, dek_wrapped,
	ciphertext, nonce, kek_version, metadata, status, epoch, created_at, updated_at, last_used_at`

func scanConnection(row pgx.Row) (Connection, error) {
	var c Connection
	err := row.Scan(&c.ID, &c.TenantID, &c.Name, &c.Kind, &c.Provider,
		&c.DEKWrapped, &c.Ciphertext, &c.Nonce, &c.KEKVersion,
		&c.Metadata, &c.Status, &c.Epoch,
		&c.CreatedAt, &c.UpdatedAt, &c.LastUsedAt)
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
		   epoch = connection.epoch + 1,
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

// UpsertConnectionBundle writes an oauth-kind credential with its
// non-secret metadata. A rewrite (re-consent on the shared connection)
// bumps the epoch and resets status, like every bundle write.
func (s *Store) UpsertConnectionBundle(ctx context.Context, tenantID uuid.UUID, name, provider string, up BundleUpdate) (Connection, error) {
	return scanConnection(s.pool.QueryRow(ctx,
		`INSERT INTO connection
		   (tenant_id, name, kind, provider, dek_wrapped, ciphertext, nonce, kek_version, metadata)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 ON CONFLICT (tenant_id, provider, name) DO UPDATE SET
		   kind = EXCLUDED.kind,
		   dek_wrapped = EXCLUDED.dek_wrapped,
		   ciphertext = EXCLUDED.ciphertext,
		   nonce = EXCLUDED.nonce,
		   kek_version = EXCLUDED.kek_version,
		   metadata = EXCLUDED.metadata,
		   status = 'ok',
		   epoch = connection.epoch + 1,
		   updated_at = now()
		 RETURNING `+connectionCols,
		tenantID, name, up.Kind, provider, up.DEKWrapped, up.Ciphertext, up.Nonce, up.KEKVersion, up.Metadata))
}

// BundleUpdate is a new sealed credential for a connection: fresh
// ciphertext plus its non-secret metadata. Applying one bumps the epoch
// and resets status to ok.
type BundleUpdate struct {
	Kind       string
	DEKWrapped []byte
	Ciphertext []byte
	Nonce      []byte
	KEKVersion int
	Metadata   []byte
}

// connLockKey derives the advisory-lock key every serialized connection
// mutation (refresh, delete) uses, so a revoke landing mid-refresh
// cannot interleave with the refresh persisting.
const connLockKey = `hashtextextended($1::text, 7239)`

// WithConnectionLock runs fn under a transaction-scoped advisory lock on
// the connection named by (tenant, provider, name). fn receives the row
// as re-read INSIDE the transaction — any decision made before the lock
// must be discarded and re-derived from it. A non-nil BundleUpdate is
// persisted in the same transaction (epoch+1, status ok) and the updated
// row returned; nil leaves the row untouched.
func (s *Store) WithConnectionLock(ctx context.Context, tenantID uuid.UUID, provider, name string,
	fn func(cur Connection) (*BundleUpdate, error)) (Connection, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Connection{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Resolve the id first: the advisory key is the connection id, which
	// delete also locks on, and the id is stable under rename-free
	// updates.
	var id uuid.UUID
	err = tx.QueryRow(ctx,
		`SELECT id FROM connection WHERE tenant_id = $1 AND provider = $2 AND name = $3`,
		tenantID, provider, name).Scan(&id)
	if err != nil {
		return Connection{}, notFound(err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(`+connLockKey+`)`, id); err != nil {
		return Connection{}, err
	}
	// Re-read under the lock: the row may have been refreshed, demoted,
	// or deleted between the id lookup and lock acquisition.
	cur, err := scanConnection(tx.QueryRow(ctx,
		`SELECT `+connectionCols+` FROM connection WHERE id = $1 AND tenant_id = $2`,
		id, tenantID))
	if err != nil {
		return Connection{}, err
	}
	up, err := fn(cur)
	if err != nil {
		return Connection{}, err
	}
	if up == nil {
		return cur, tx.Commit(ctx)
	}
	updated, err := scanConnection(tx.QueryRow(ctx,
		`UPDATE connection SET
		   kind = $3, dek_wrapped = $4, ciphertext = $5, nonce = $6,
		   kek_version = $7, metadata = $8, status = 'ok',
		   epoch = epoch + 1, updated_at = now()
		 WHERE id = $1 AND tenant_id = $2
		 RETURNING `+connectionCols,
		id, tenantID, up.Kind, up.DEKWrapped, up.Ciphertext, up.Nonce, up.KEKVersion, up.Metadata))
	if err != nil {
		return Connection{}, err
	}
	return updated, tx.Commit(ctx)
}

// MarkConnectionNeedsReauth demotes a connection via compare-and-swap on
// the epoch: a demotion earned by a stale token (an epoch that has since
// been superseded by a refresh) misses and reports false.
func (s *Store) MarkConnectionNeedsReauth(ctx context.Context, tenantID, id uuid.UUID, epoch int64) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE connection SET status = 'needs_reauth', updated_at = now()
		 WHERE id = $1 AND tenant_id = $2 AND epoch = $3 AND status = 'ok'`,
		id, tenantID, epoch)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// DeleteConnectionLocked deletes under the same advisory lock refresh
// holds, so a revoke landing mid-refresh cannot be followed by the
// refresh persisting credentials for a row that no longer exists. It
// returns the row as it was, for best-effort provider-side revocation.
func (s *Store) DeleteConnectionLocked(ctx context.Context, tenantID uuid.UUID, provider, name string) (Connection, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Connection{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var id uuid.UUID
	err = tx.QueryRow(ctx,
		`SELECT id FROM connection WHERE tenant_id = $1 AND provider = $2 AND name = $3`,
		tenantID, provider, name).Scan(&id)
	if err != nil {
		return Connection{}, notFound(err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(`+connLockKey+`)`, id); err != nil {
		return Connection{}, err
	}
	deleted, err := scanConnection(tx.QueryRow(ctx,
		`DELETE FROM connection WHERE id = $1 AND tenant_id = $2 RETURNING `+connectionCols,
		id, tenantID))
	if err != nil {
		return Connection{}, err
	}
	return deleted, tx.Commit(ctx)
}

func (s *Store) TouchConnection(ctx context.Context, tenantID, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE connection SET last_used_at = now() WHERE id = $1 AND tenant_id = $2`,
		id, tenantID)
	return err
}
