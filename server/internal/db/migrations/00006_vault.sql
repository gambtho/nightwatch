-- +goose Up
CREATE TABLE tenant_kek (
    tenant_id uuid NOT NULL REFERENCES tenant (id) ON DELETE CASCADE,
    -- History table: rotation ADDS a row with version+1; old versions stay
    -- decryptable while connections still name them via kek_version.
    version int NOT NULL DEFAULT 1,
    wrapped_kek bytea NOT NULL,
    master_version int NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, version)
);

CREATE TABLE connection (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenant (id) ON DELETE CASCADE,
    name text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('llm_api_key')),
    provider text NOT NULL,
    dek_wrapped bytea NOT NULL,
    ciphertext bytea NOT NULL,
    nonce bytea NOT NULL,
    kek_version int NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    UNIQUE (tenant_id, provider, name)
);

-- +goose Down
DROP TABLE connection;
DROP TABLE tenant_kek;
