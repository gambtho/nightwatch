package store

import (
	"context"

	"github.com/google/uuid"
)

// RecordSpend appends one non-run spend entry — cost the product incurred
// outside any run (today: the paste-time endpoint verify call). Entries
// are append-only and summed into MonthSpendCents alongside run costs.
func (s *Store) RecordSpend(ctx context.Context, tenantID uuid.UUID, kind string, costCents, inputTokens, outputTokens int, baseURL, model string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO spend_entry (tenant_id, kind, cost_cents, input_tokens, output_tokens, base_url, model)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		tenantID, kind, costCents, inputTokens, outputTokens, baseURL, model)
	return err
}
