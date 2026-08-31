-- +goose Up
-- Finalization clears the run's token hash (atomic revocation), so the
-- column must accept NULL.
ALTER TABLE run ALTER COLUMN runner_token_hash DROP NOT NULL;

-- +goose Down
UPDATE run SET runner_token_hash = '' WHERE runner_token_hash IS NULL;
ALTER TABLE run ALTER COLUMN runner_token_hash SET NOT NULL;
