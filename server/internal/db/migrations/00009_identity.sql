-- +goose Up
-- One canonical email representation, then the v1 one-tenant-per-email
-- constraint. A dev database holding cross-tenant duplicates (repeated
-- default dev-session runs created them) fails here and is recreated
-- against a fresh database; no production data exists.
UPDATE app_user SET email = lower(btrim(email));
CREATE UNIQUE INDEX app_user_email_global ON app_user (lower(email));

CREATE TABLE login_token (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash bytea NOT NULL UNIQUE,
    email text NOT NULL,
    next_path text,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz
);

CREATE TABLE session (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash bytea NOT NULL UNIQUE,
    user_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    -- Composite FK: same-tenant parentage, the pattern every child table
    -- uses. The database itself refuses a session pairing user A with
    -- tenant B; cascade means a deleted user takes its sessions with it.
    FOREIGN KEY (tenant_id, user_id)
        REFERENCES app_user (tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX session_by_user ON session (tenant_id, user_id);

-- +goose Down
DROP TABLE session;
DROP TABLE login_token;
DROP INDEX app_user_email_global;
