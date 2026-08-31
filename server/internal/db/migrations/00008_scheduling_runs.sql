-- +goose Up
ALTER TABLE run ADD COLUMN fire_time timestamptz;
ALTER TABLE run ADD COLUMN dispatched_at timestamptz;
ALTER TABLE tenant ADD COLUMN monthly_cap_cents int;

-- Idempotent occurrences: one run per (workflow, scheduled instant).
CREATE UNIQUE INDEX run_workflow_firetime_unique
    ON run (workflow_id, fire_time) WHERE fire_time IS NOT NULL;

-- One active run per workflow, DB-enforced: the platform spec's "default
-- to serialize" as an admission rule, racing the index instead of a
-- check-then-fire. Applies to manual and scheduled fires alike.
CREATE UNIQUE INDEX run_one_active_per_workflow
    ON run (workflow_id) WHERE status IN ('pending', 'running');

ALTER TABLE run ADD CONSTRAINT run_fire_reason_time_consistent
    CHECK ((fire_reason = 'schedule') = (fire_time IS NOT NULL));

-- Month-to-date spend is a per-request check at the proxy hook: keep it an
-- index range scan, never a tenant-wide table scan.
CREATE INDEX run_tenant_spend_idx
    ON run (tenant_id, finished_at) WHERE cost_cents IS NOT NULL;

-- +goose Down
DROP INDEX run_tenant_spend_idx;
ALTER TABLE run DROP CONSTRAINT run_fire_reason_time_consistent;
DROP INDEX run_one_active_per_workflow;
DROP INDEX run_workflow_firetime_unique;
ALTER TABLE tenant DROP COLUMN monthly_cap_cents;
ALTER TABLE run DROP COLUMN dispatched_at;
ALTER TABLE run DROP COLUMN fire_time;
