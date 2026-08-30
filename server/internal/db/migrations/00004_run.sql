-- +goose Up
CREATE TABLE run (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    workflow_id uuid NOT NULL,
    version int NOT NULL,
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'running', 'succeeded', 'failed')),
    fire_reason text NOT NULL DEFAULT 'manual'
        CHECK (fire_reason IN ('manual', 'schedule')),
    started_at timestamptz,
    finished_at timestamptz,
    tokens_in int,
    tokens_out int,
    cost_cents int,
    error_kind text,
    error_msg text,
    output text,
    runner_token_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, workflow_id)
        REFERENCES workflow (tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workflow_id, version)
        REFERENCES workflow_version (workflow_id, version)
);

CREATE INDEX run_workflow_created_idx ON run (workflow_id, created_at DESC);

CREATE TABLE run_event (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id uuid NOT NULL REFERENCES run (id) ON DELETE CASCADE,
    tenant_id uuid NOT NULL,
    type text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX run_event_run_idx ON run_event (run_id, id);

-- +goose Down
DROP TABLE run_event;
DROP TABLE run;
