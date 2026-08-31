-- +goose Up
-- Connector credentials join the vault (connectors spec, vault section).
-- Pre-release: widen in place.
--   oauth   — ciphertext holds one encrypted JSON bundle: access token,
--             refresh token, expiry, granted scopes.
--   api_key — static bearer secrets for remote MCP servers (accepted by
--             the API only once that surface exists).
ALTER TABLE connection DROP CONSTRAINT connection_kind_check;
ALTER TABLE connection ADD CONSTRAINT connection_kind_check
    CHECK (kind IN ('llm_api_key', 'oauth', 'api_key'));

-- Non-secret facts a session may see: granted scopes, account label,
-- provider hints. Never tokens.
ALTER TABLE connection ADD COLUMN metadata jsonb NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE connection ADD COLUMN status text NOT NULL DEFAULT 'ok'
    CHECK (status IN ('ok', 'needs_reauth'));

-- Credential epoch: every bundle write bumps it; a 401 earned by a
-- stale token demotes to needs_reauth only via compare-and-swap on the
-- epoch it was issued under, so it can never demote a connection that
-- was refreshed in the meantime.
ALTER TABLE connection ADD COLUMN epoch bigint NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE connection DROP COLUMN epoch;
ALTER TABLE connection DROP COLUMN status;
ALTER TABLE connection DROP COLUMN metadata;
ALTER TABLE connection DROP CONSTRAINT connection_kind_check;
ALTER TABLE connection ADD CONSTRAINT connection_kind_check
    CHECK (kind IN ('llm_api_key'));
