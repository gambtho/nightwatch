package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type StepsDoc struct {
	SystemPrompt string `json:"system_prompt"`
	Kickoff      string `json:"kickoff"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	MaxTokens    int    `json:"max_tokens"`
}

type VersionDoc struct {
	Steps    StepsDoc        `json:"steps"`
	Permit   json.RawMessage `json:"permit"`
	Rubric   json.RawMessage `json:"rubric"`
	Schedule json.RawMessage `json:"schedule,omitempty"`
}

type Workflow struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	Name      string
	CreatedAt time.Time
}

type Version struct {
	WorkflowID uuid.UUID
	TenantID   uuid.UUID
	Number     int
	Doc        VersionDoc
	Status     string
	ApprovedBy *uuid.UUID
	ApprovedAt *time.Time
	CreatedAt  time.Time
}

const versionCols = `workflow_id, tenant_id, version, steps, permit, rubric,
	schedule, status, approved_by, approved_at, created_at`

func scanVersion(row pgx.Row) (Version, error) {
	var v Version
	var steps []byte
	err := row.Scan(&v.WorkflowID, &v.TenantID, &v.Number, &steps,
		&v.Doc.Permit, &v.Doc.Rubric, &v.Doc.Schedule, &v.Status, &v.ApprovedBy,
		&v.ApprovedAt, &v.CreatedAt)
	if err != nil {
		return v, notFound(err)
	}
	if err := json.Unmarshal(steps, &v.Doc.Steps); err != nil {
		return v, err
	}
	return v, nil
}

func (s *Store) CreateWorkflow(ctx context.Context, tenantID uuid.UUID, name string, doc VersionDoc) (Workflow, Version, error) {
	var wf Workflow
	var v Version
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return wf, v, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	err = tx.QueryRow(ctx,
		`INSERT INTO workflow (tenant_id, name) VALUES ($1, $2)
		 RETURNING id, tenant_id, name, created_at`,
		tenantID, name,
	).Scan(&wf.ID, &wf.TenantID, &wf.Name, &wf.CreatedAt)
	if err != nil {
		return wf, v, err
	}

	steps, err := json.Marshal(doc.Steps)
	if err != nil {
		return wf, v, err
	}
	v, err = scanVersion(tx.QueryRow(ctx,
		`INSERT INTO workflow_version
		   (workflow_id, tenant_id, version, steps, permit, rubric, schedule)
		 VALUES ($1, $2, 1, $3, $4, $5, $6)
		 RETURNING `+versionCols,
		wf.ID, tenantID, steps, doc.Permit, doc.Rubric, doc.Schedule))
	if err != nil {
		return wf, v, err
	}
	return wf, v, tx.Commit(ctx)
}

func (s *Store) AddVersion(ctx context.Context, tenantID, workflowID uuid.UUID, doc VersionDoc) (Version, error) {
	steps, err := json.Marshal(doc.Steps)
	if err != nil {
		return Version{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Version{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// FOR UPDATE serializes concurrent AddVersion calls on one workflow so
	// MAX(version)+1 cannot collide; the tenant filter doubles as the
	// cross-tenant guard (wrong tenant -> no row -> ErrNotFound).
	var id uuid.UUID
	err = tx.QueryRow(ctx,
		`SELECT id FROM workflow WHERE id = $1 AND tenant_id = $2 FOR UPDATE`,
		workflowID, tenantID).Scan(&id)
	if err != nil {
		return Version{}, notFound(err)
	}

	v, err := scanVersion(tx.QueryRow(ctx,
		`INSERT INTO workflow_version
		   (workflow_id, tenant_id, version, steps, permit, rubric, schedule)
		 VALUES ($1, $2,
		        (SELECT COALESCE(MAX(version), 0) + 1 FROM workflow_version WHERE workflow_id = $1),
		        $3, $4, $5, $6)
		 RETURNING `+versionCols,
		workflowID, tenantID, steps, doc.Permit, doc.Rubric, doc.Schedule))
	if err != nil {
		return Version{}, err
	}
	return v, tx.Commit(ctx)
}

func (s *Store) ApproveVersion(ctx context.Context, tenantID, workflowID uuid.UUID, number int, approvedBy uuid.UUID) (Version, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Version{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx,
		`UPDATE workflow_version SET status = 'superseded'
		 WHERE workflow_id = $1 AND tenant_id = $2 AND status = 'approved'`,
		workflowID, tenantID)
	if err != nil {
		return Version{}, err
	}
	v, err := scanVersion(tx.QueryRow(ctx,
		`UPDATE workflow_version
		 SET status = 'approved', approved_by = $3, approved_at = now()
		 WHERE workflow_id = $1 AND tenant_id = $2 AND version = $4
		   AND status = 'draft'
		 RETURNING `+versionCols,
		workflowID, tenantID, approvedBy, number))
	if err != nil {
		return Version{}, err
	}
	return v, tx.Commit(ctx)
}

func (s *Store) GetWorkflow(ctx context.Context, tenantID, id uuid.UUID) (Workflow, error) {
	var wf Workflow
	err := s.pool.QueryRow(ctx,
		`SELECT id, tenant_id, name, created_at FROM workflow
		 WHERE id = $1 AND tenant_id = $2`,
		id, tenantID,
	).Scan(&wf.ID, &wf.TenantID, &wf.Name, &wf.CreatedAt)
	return wf, notFound(err)
}

func (s *Store) ListWorkflows(ctx context.Context, tenantID uuid.UUID) ([]Workflow, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, tenant_id, name, created_at FROM workflow
		 WHERE tenant_id = $1 ORDER BY created_at`,
		tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Workflow
	for rows.Next() {
		var wf Workflow
		if err := rows.Scan(&wf.ID, &wf.TenantID, &wf.Name, &wf.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, wf)
	}
	return out, rows.Err()
}

func (s *Store) GetVersion(ctx context.Context, tenantID, workflowID uuid.UUID, number int) (Version, error) {
	return scanVersion(s.pool.QueryRow(ctx,
		`SELECT `+versionCols+` FROM workflow_version
		 WHERE workflow_id = $1 AND tenant_id = $2 AND version = $3`,
		workflowID, tenantID, number))
}

func (s *Store) ListVersions(ctx context.Context, tenantID, workflowID uuid.UUID) ([]Version, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+versionCols+` FROM workflow_version
		 WHERE workflow_id = $1 AND tenant_id = $2 ORDER BY version`,
		workflowID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Version
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) GetApprovedVersion(ctx context.Context, tenantID, workflowID uuid.UUID) (Version, error) {
	return scanVersion(s.pool.QueryRow(ctx,
		`SELECT `+versionCols+` FROM workflow_version
		 WHERE workflow_id = $1 AND tenant_id = $2 AND status = 'approved'`,
		workflowID, tenantID))
}

type SchedulableWorkflow struct {
	TenantID   uuid.UUID
	WorkflowID uuid.UUID
	Version    int
	Schedule   json.RawMessage
}

// System query: the scheduler is a platform actor scanning every tenant's
// approved, scheduled versions; rows carry their TenantID.
func (s *Store) ListSchedulableWorkflows(ctx context.Context) ([]SchedulableWorkflow, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT tenant_id, workflow_id, version, schedule FROM workflow_version
		 WHERE status = 'approved' AND schedule IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SchedulableWorkflow
	for rows.Next() {
		var w SchedulableWorkflow
		if err := rows.Scan(&w.TenantID, &w.WorkflowID, &w.Version, &w.Schedule); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
