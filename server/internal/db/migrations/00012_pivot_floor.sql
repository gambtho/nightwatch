-- +goose Up
-- P1 pivot floor (click-install spec).

-- Login as a surface dies: magic links, their tokens, and the mailer.
-- The session core (session table, RequireSession, cookies) survives.
DROP TABLE login_token;

-- OAuth credentials are dead with the pivot; delete rows BEFORE narrowing
-- the kind check, or the constraint fails on any install that has one.
-- metadata and epoch existed only for the OAuth machinery; status stays —
-- a pasted token can still be revoked upstream (needs_reauth).
DELETE FROM connection WHERE kind = 'oauth';
ALTER TABLE connection DROP COLUMN metadata, DROP COLUMN epoch;
ALTER TABLE connection DROP CONSTRAINT connection_kind_check;
ALTER TABLE connection ADD CONSTRAINT connection_kind_check
    CHECK (kind IN ('llm_api_key', 'api_key'));

-- System table: single row, no tenant scope — the scheduler's
-- last-completed-tick, persisted so fire-on-wake also covers
-- quit-and-relaunch ("The sleeping machine").
CREATE TABLE scheduler_heartbeat (
    id int PRIMARY KEY CHECK (id = 1),
    last_tick_at timestamptz NOT NULL
);

-- Single-use, short-TTL browser handoff (login_token's consume shape,
-- minus the email): the tray's "open in browser" mints one, the browser
-- exchanges it for a session cookie exactly once.
CREATE TABLE handoff_token (
    token_hash bytea PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenant (id) ON DELETE CASCADE,
    user_id uuid NOT NULL,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, user_id) REFERENCES app_user (tenant_id, id) ON DELETE CASCADE
);

-- One configured LLM endpoint per tenant ("Endpoint agnosticism": one
-- endpoint, switchable; switching is a recorded governance act).
CREATE TABLE llm_endpoint (
    tenant_id uuid PRIMARY KEY REFERENCES tenant (id) ON DELETE CASCADE,
    preset text NOT NULL CHECK (preset IN ('anthropic', 'openai', 'openrouter', 'github', 'azure', 'custom', 'local')),
    kind text NOT NULL CHECK (kind IN ('anthropic', 'openai_compatible')),
    base_url text NOT NULL,
    connection_name text,
    run_model text NOT NULL,
    -- Explicit $0 classification, never inferred: local always; github only
    -- on the free included quota (paid GitHub usage takes the user-entered
    -- price path); everything else is priced.
    zero_cost boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT now(),
    -- A local endpoint carries no credential, ever.
    CONSTRAINT llm_endpoint_local_no_connection CHECK (preset <> 'local' OR connection_name IS NULL),
    CONSTRAINT llm_endpoint_local_zero_cost CHECK (preset <> 'local' OR zero_cost),
    CONSTRAINT llm_endpoint_zero_cost_presets CHECK (NOT zero_cost OR preset IN ('local', 'github'))
);

-- User-entered prices, keyed by the endpoint's canonical base URL so a
-- custom endpoint can never inherit a price entered for a different one
-- ("The priced-pair gate, reworked").
CREATE TABLE model_price (
    tenant_id uuid NOT NULL REFERENCES tenant (id) ON DELETE CASCADE,
    base_url text NOT NULL,
    model text NOT NULL,
    input_cents_per_1m int NOT NULL CHECK (input_cents_per_1m >= 0),
    output_cents_per_1m int NOT NULL CHECK (output_cents_per_1m >= 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, base_url, model)
);

-- Tenant-scoped governance events (endpoint switches, and whatever joins
-- them later). run_event is FK'd to a run and cannot hold these.
CREATE TABLE tenant_event (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenant (id) ON DELETE CASCADE,
    type text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
-- Recreated exactly as 00009 defined it.
CREATE TABLE login_token (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash bytea NOT NULL UNIQUE,
    email text NOT NULL,
    next_path text,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz
);
ALTER TABLE connection DROP CONSTRAINT connection_kind_check;
ALTER TABLE connection ADD CONSTRAINT connection_kind_check
    CHECK (kind IN ('llm_api_key', 'oauth', 'api_key'));
ALTER TABLE connection ADD COLUMN metadata jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE connection ADD COLUMN epoch bigint NOT NULL DEFAULT 1;
DROP TABLE tenant_event;
DROP TABLE model_price;
DROP TABLE llm_endpoint;
DROP TABLE handoff_token;
DROP TABLE scheduler_heartbeat;
