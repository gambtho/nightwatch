package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Run struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	WorkflowID uuid.UUID
	Version    int
	Status     string
	StartedAt  *time.Time
	FinishedAt *time.Time
	TokensIn   *int
	TokensOut  *int
	CostCents  *int
	ErrorKind  *string
	ErrorMsg   *string
	Output     *string
	TokenHash  string
	CreatedAt  time.Time
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

const runCols = `id, tenant_id, workflow_id, version, status, started_at,
	finished_at, tokens_in, tokens_out, cost_cents, error_kind, error_msg,
	output, COALESCE(runner_token_hash, ''), created_at`

func scanRun(row pgx.Row) (Run, error) {
	var r Run
	err := row.Scan(&r.ID, &r.TenantID, &r.WorkflowID, &r.Version, &r.Status,
		&r.StartedAt, &r.FinishedAt, &r.TokensIn, &r.TokensOut, &r.CostCents,
		&r.ErrorKind, &r.ErrorMsg, &r.Output, &r.TokenHash, &r.CreatedAt)
	return r, notFound(err)
}

func (s *Store) CreateRun(ctx context.Context, tenantID, workflowID, id uuid.UUID, version int, tokenHash string) (Run, error) {
	return scanRun(s.pool.QueryRow(ctx,
		`INSERT INTO run (id, tenant_id, workflow_id, version, runner_token_hash)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING `+runCols,
		id, tenantID, workflowID, version, tokenHash))
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

func (s *Store) FinalizeRun(ctx context.Context, tenantID, id uuid.UUID, fin RunFinal) (Run, error) {
	return scanRun(s.pool.QueryRow(ctx,
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
