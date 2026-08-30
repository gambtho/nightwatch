-- +goose Up
CREATE TABLE app_user (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenant (id) ON DELETE CASCADE,
    email text NOT NULL,
    role text NOT NULL DEFAULT 'owner' CHECK (role IN ('owner')),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, email)
);

-- +goose Down
DROP TABLE app_user;
