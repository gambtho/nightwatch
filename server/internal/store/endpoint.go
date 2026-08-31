package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// LLMEndpoint is the tenant's one configured LLM endpoint. ZeroCost is an
// explicit classification (local always; github only on the free included
// quota), never inferred from the base URL — a loopback host can front a
// paid service.
type LLMEndpoint struct {
	Preset         string
	Kind           string
	BaseURL        string
	ConnectionName *string
	RunModel       string
	ZeroCost       bool
}

const llmEndpointCols = `preset, kind, base_url, connection_name, run_model, zero_cost`

// GetLLMEndpoint returns ErrNotFound when the tenant has not configured an
// endpoint yet — callers treat that as legacy env mode.
func (s *Store) GetLLMEndpoint(ctx context.Context, tenantID uuid.UUID) (*LLMEndpoint, error) {
	var e LLMEndpoint
	err := s.pool.QueryRow(ctx,
		`SELECT `+llmEndpointCols+` FROM llm_endpoint WHERE tenant_id = $1`,
		tenantID,
	).Scan(&e.Preset, &e.Kind, &e.BaseURL, &e.ConnectionName, &e.RunModel, &e.ZeroCost)
	if err != nil {
		return nil, notFound(err)
	}
	return &e, nil
}

func (s *Store) PutLLMEndpoint(ctx context.Context, tenantID uuid.UUID, e LLMEndpoint) error {
	return putLLMEndpoint(ctx, s.pool, tenantID, e)
}

func putLLMEndpoint(ctx context.Context, q querier, tenantID uuid.UUID, e LLMEndpoint) error {
	_, err := q.Exec(ctx,
		`INSERT INTO llm_endpoint (tenant_id, `+llmEndpointCols+`)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (tenant_id) DO UPDATE SET
		   preset = EXCLUDED.preset, kind = EXCLUDED.kind,
		   base_url = EXCLUDED.base_url, connection_name = EXCLUDED.connection_name,
		   run_model = EXCLUDED.run_model, zero_cost = EXCLUDED.zero_cost,
		   updated_at = now()`,
		tenantID, e.Preset, e.Kind, e.BaseURL, e.ConnectionName, e.RunModel, e.ZeroCost)
	return err
}

// ModelPrice is a user-entered price for a model on one endpoint, keyed by
// the endpoint's canonical base URL so no endpoint inherits another's price.
type ModelPrice struct {
	BaseURL          string
	Model            string
	InputCentsPer1M  int
	OutputCentsPer1M int
}

func (s *Store) UpsertModelPrice(ctx context.Context, tenantID uuid.UUID, baseURL, model string, inCents, outCents int) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO model_price (tenant_id, base_url, model, input_cents_per_1m, output_cents_per_1m)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (tenant_id, base_url, model) DO UPDATE SET
		   input_cents_per_1m = EXCLUDED.input_cents_per_1m,
		   output_cents_per_1m = EXCLUDED.output_cents_per_1m,
		   updated_at = now()`,
		tenantID, baseURL, model, inCents, outCents)
	return err
}

func (s *Store) GetModelPrice(ctx context.Context, tenantID uuid.UUID, baseURL, model string) (inCents, outCents int, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT input_cents_per_1m, output_cents_per_1m FROM model_price
		 WHERE tenant_id = $1 AND base_url = $2 AND model = $3`,
		tenantID, baseURL, model,
	).Scan(&inCents, &outCents)
	return inCents, outCents, notFound(err)
}

func (s *Store) ListModelPrices(ctx context.Context, tenantID uuid.UUID) ([]ModelPrice, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT base_url, model, input_cents_per_1m, output_cents_per_1m
		 FROM model_price WHERE tenant_id = $1 ORDER BY base_url, model`,
		tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelPrice
	for rows.Next() {
		var p ModelPrice
		if err := rows.Scan(&p.BaseURL, &p.Model, &p.InputCentsPer1M, &p.OutputCentsPer1M); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// TenantEvent is a tenant-scoped governance record (endpoint switches);
// run_event is FK'd to a run and cannot hold these.
type TenantEvent struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	Type      string
	Payload   json.RawMessage
	CreatedAt time.Time
}

func (s *Store) AppendTenantEvent(ctx context.Context, tenantID uuid.UUID, eventType string, payload json.RawMessage) error {
	return appendTenantEvent(ctx, s.pool, tenantID, eventType, payload)
}

func appendTenantEvent(ctx context.Context, q querier, tenantID uuid.UUID, eventType string, payload json.RawMessage) error {
	if payload == nil {
		payload = json.RawMessage(`{}`)
	}
	_, err := q.Exec(ctx,
		`INSERT INTO tenant_event (tenant_id, type, payload) VALUES ($1, $2, $3)`,
		tenantID, eventType, payload)
	return err
}

func (s *Store) ListTenantEvents(ctx context.Context, tenantID uuid.UUID) ([]TenantEvent, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, tenant_id, type, payload, created_at FROM tenant_event
		 WHERE tenant_id = $1 ORDER BY created_at, id`,
		tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TenantEvent
	for rows.Next() {
		var e TenantEvent
		if err := rows.Scan(&e.ID, &e.TenantID, &e.Type, &e.Payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CompiledUpdate names one approved version whose compiled document an
// endpoint switch replaces.
type CompiledUpdate struct {
	WorkflowID uuid.UUID
	Version    int
	Compiled   json.RawMessage
}

// SwitchLLMEndpoint applies an endpoint switch as one transaction: the
// endpoint upsert, every recompiled approved version, and the governance
// event commit together or not at all — a partial switch would leave
// routing, pricing, and audit inconsistent. The caller computes the
// recompilations (steps.Compile is pure); the store only applies them.
func (s *Store) SwitchLLMEndpoint(ctx context.Context, tenantID uuid.UUID, e LLMEndpoint, recompiled []CompiledUpdate, eventPayload json.RawMessage) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := putLLMEndpoint(ctx, tx, tenantID, e); err != nil {
		return err
	}
	for _, cu := range recompiled {
		tag, err := tx.Exec(ctx,
			`UPDATE workflow_version SET compiled = $4
			 WHERE workflow_id = $1 AND tenant_id = $2 AND version = $3
			   AND status = 'approved'`,
			cu.WorkflowID, tenantID, cu.Version, cu.Compiled)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
	}
	if err := appendTenantEvent(ctx, tx, tenantID, "endpoint.switched", eventPayload); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
