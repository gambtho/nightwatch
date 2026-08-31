-- +goose Up
-- Non-run spend ledger (P2 phase B). The first-run LLM key verify is a
-- disclosed, metered call made before any run exists; its cost must
-- count against the monthly budget once one is set (pivot spec, "First
-- run"). Runs stay the ledger for run spend (run.cost_cents); this table
-- holds everything the product spends outside a run. The kind CHECK is
-- deliberately single-valued — future non-run spenders (build agent,
-- grader) widen it when they arrive.
CREATE TABLE spend_entry (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
    kind          text NOT NULL CHECK (kind IN ('endpoint_verify')),
    cost_cents    integer NOT NULL CHECK (cost_cents >= 0),
    -- Token counts are recorded even when the cost rounds (or is priced)
    -- to zero, so the spend is honest about usage on unpriced endpoints.
    input_tokens  integer NOT NULL CHECK (input_tokens >= 0),
    output_tokens integer NOT NULL CHECK (output_tokens >= 0),
    base_url      text NOT NULL,
    model         text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX spend_entry_tenant_created ON spend_entry (tenant_id, created_at);

-- +goose Down
DROP TABLE spend_entry;
