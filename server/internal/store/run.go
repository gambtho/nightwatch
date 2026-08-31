package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Run struct {
	ID           uuid.UUID
	TenantID     uuid.UUID
	WorkflowID   uuid.UUID
	Version      int
	Status       string
	FireReason   string
	FireTime     *time.Time
	DispatchedAt *time.Time
	StartedAt    *time.Time
	FinishedAt   *time.Time
	TokensIn     *int
	TokensOut    *int
	CostCents    *int
	ErrorKind    *string
	ErrorMsg     *string
	Output       *string
	TokenHash    string
	CreatedAt    time.Time
}

type RunEvent struct {
	ID        int64
	RunID     uuid.UUID
	Type      string
	Payload   json.RawMessage
	CreatedAt time.Time
}

type RunFinal struct {
	Status    string
	ErrorKind string
	ErrorMsg  string
	Output    string
	TokensIn  int
	TokensOut int
	CostCents int
}

const runCols = `id, tenant_id, workflow_id, version, status, fire_reason,
	fire_time, dispatched_at, started_at, finished_at, tokens_in, tokens_out,
	cost_cents, error_kind, error_msg, output,
	COALESCE(runner_token_hash, ''), created_at`

func scanRun(row pgx.Row) (Run, error) {
	var r Run
	err := row.Scan(&r.ID, &r.TenantID, &r.WorkflowID, &r.Version, &r.Status,
		&r.FireReason, &r.FireTime, &r.DispatchedAt, &r.StartedAt, &r.FinishedAt,
		&r.TokensIn, &r.TokensOut, &r.CostCents,
		&r.ErrorKind, &r.ErrorMsg, &r.Output, &r.TokenHash, &r.CreatedAt)
	return r, notFound(err)
}

func (s *Store) CreateRun(ctx context.Context, tenantID, workflowID, id uuid.UUID, version int, tokenHash, fireReason string, fireTime *time.Time) (Run, error) {
	run, err := scanRun(s.pool.QueryRow(ctx,
		`INSERT INTO run (id, tenant_id, workflow_id, version, runner_token_hash, fire_reason, fire_time)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING `+runCols,
		id, tenantID, workflowID, version, tokenHash, fireReason, fireTime))
	return run, admissionErr(err)
}

// admissionErr maps the two coordination indexes' unique violations to
// their sentinels; everything else passes through.
func admissionErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		switch pgErr.ConstraintName {
		case "run_one_active_per_workflow":
			return ErrActiveRun
		case "run_workflow_firetime_unique":
			return ErrAlreadyFired
		}
	}
	return err
}

func (s *Store) GetRun(ctx context.Context, tenantID, id uuid.UUID) (Run, error) {
	return scanRun(s.pool.QueryRow(ctx,
		`SELECT `+runCols+` FROM run WHERE id = $1 AND tenant_id = $2`,
		id, tenantID))
}

func (s *Store) ListRuns(ctx context.Context, tenantID, workflowID uuid.UUID) ([]Run, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+runCols+` FROM run
		 WHERE workflow_id = $1 AND tenant_id = $2
		 ORDER BY created_at DESC`,
		workflowID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) MarkRunRunning(ctx context.Context, tenantID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE run SET status = 'running', started_at = COALESCE(started_at, now())
		 WHERE id = $1 AND tenant_id = $2 AND status IN ('pending', 'running')`,
		id, tenantID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// FinalizeRun sets the terminal status, clears the token hash (revocation),
// and — when the finalized cost exceeds the approved per-run cap — records
// the spend.exceeded event in the SAME transaction: the event insert happens
// while the run is still non-terminal (satisfying the immutability guard),
// and a finalize that loses the terminal-transition race rolls the event
// back with it.
func (s *Store) FinalizeRun(ctx context.Context, tenantID, id uuid.UUID, fin RunFinal, perRunCapCents int) (Run, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Run{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if perRunCapCents > 0 && fin.CostCents > perRunCapCents {
		if _, err := tx.Exec(ctx,
			`INSERT INTO run_event (run_id, tenant_id, type, payload)
			 SELECT $1, $2, 'spend.exceeded', $3
			 WHERE EXISTS (SELECT 1 FROM run WHERE id = $1 AND tenant_id = $2
			               AND status IN ('pending', 'running'))`,
			id, tenantID,
			[]byte(fmt.Sprintf(`{"cost_cents":%d,"per_run_cap_cents":%d}`, fin.CostCents, perRunCapCents)),
		); err != nil {
			return Run{}, err
		}
	}

	run, err := scanRun(tx.QueryRow(ctx,
		`UPDATE run SET status = $3, finished_at = now(),
		        tokens_in = $4, tokens_out = $5, cost_cents = $6,
		        error_kind = NULLIF($7, ''), error_msg = NULLIF($8, ''),
		        output = $9,
		        runner_token_hash = NULL
		 WHERE id = $1 AND tenant_id = $2
		   AND status IN ('pending', 'running')
		 RETURNING `+runCols,
		id, tenantID, fin.Status, fin.TokensIn, fin.TokensOut, fin.CostCents,
		fin.ErrorKind, fin.ErrorMsg, fin.Output))
	if err != nil {
		return Run{}, err
	}
	return run, tx.Commit(ctx)
}

func (s *Store) MarkRunDispatched(ctx context.Context, tenantID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE run SET dispatched_at = now()
		 WHERE id = $1 AND tenant_id = $2 AND dispatched_at IS NULL`,
		id, tenantID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ResetRunToken replaces the stored token hash for a run that was created
// but never dispatched — the redispatch path must mint a fresh bearer
// (only the hash is persisted), and an undispatched pending run has no
// token holder, so the swap is safe by construction.
func (s *Store) ResetRunToken(ctx context.Context, tenantID, id uuid.UUID, newHash string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE run SET runner_token_hash = $3
		 WHERE id = $1 AND tenant_id = $2
		   AND status = 'pending' AND dispatched_at IS NULL`,
		id, tenantID, newHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// System query: the scheduler is a platform actor sweeping all tenants for
// crash-recovery redispatch; rows carry their TenantID.
func (s *Store) ListUndispatchedScheduledRuns(ctx context.Context) ([]Run, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+runCols+` FROM run
		 WHERE status = 'pending' AND fire_reason = 'schedule'
		   AND dispatched_at IS NULL
		 ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// System query: the reaper is a platform actor; rows carry their TenantID.
func (s *Store) ListStuckRuns(ctx context.Context, cutoff time.Time) ([]Run, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+runCols+` FROM run
		 WHERE status IN ('pending', 'running') AND created_at < $1
		 ORDER BY created_at`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) MonthSpendCents(ctx context.Context, tenantID uuid.UUID, monthStart time.Time) (int, error) {
	var cents int
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(cost_cents), 0) FROM run
		 WHERE tenant_id = $1 AND finished_at >= $2 AND cost_cents IS NOT NULL`,
		tenantID, monthStart).Scan(&cents)
	return cents, err
}

func (s *Store) AppendRunEvent(ctx context.Context, tenantID, runID uuid.UUID, typ string, payload json.RawMessage) error {
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	tag, err := s.pool.Exec(ctx,
		`INSERT INTO run_event (run_id, tenant_id, type, payload)
		 SELECT $1, $2, $3, $4
		 WHERE EXISTS (SELECT 1 FROM run WHERE id = $1 AND tenant_id = $2
		               AND status IN ('pending', 'running'))`,
		runID, tenantID, typ, payload)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListRunEvents(ctx context.Context, tenantID, runID uuid.UUID) ([]RunEvent, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, run_id, type, payload, created_at FROM run_event
		 WHERE run_id = $1 AND tenant_id = $2 ORDER BY id`,
		runID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunEvent
	for rows.Next() {
		var e RunEvent
		if err := rows.Scan(&e.ID, &e.RunID, &e.Type, &e.Payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
