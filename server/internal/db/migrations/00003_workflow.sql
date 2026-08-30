-- +goose Up
CREATE TABLE workflow (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenant (id) ON DELETE CASCADE,
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id)
);

CREATE TABLE workflow_version (
    workflow_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    version int NOT NULL,
    steps jsonb NOT NULL,
    permit jsonb NOT NULL,
    rubric jsonb NOT NULL,
    status text NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'approved', 'superseded')),
    approved_by uuid,
    approved_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workflow_id, version),
    -- Composite FK: enforces same-tenant parentage, the gap cronfoundry's
    -- own migration comments admit its single-column FKs leave open.
    FOREIGN KEY (tenant_id, workflow_id)
        REFERENCES workflow (tenant_id, id) ON DELETE CASCADE
);

-- At most one approved version per workflow; enforced by the database,
-- not by application discipline.
CREATE UNIQUE INDEX workflow_version_one_approved
    ON workflow_version (workflow_id) WHERE status = 'approved';

-- +goose Down
DROP TABLE workflow_version;
DROP TABLE workflow;
