-- +goose Up
ALTER TABLE workflow_version ADD COLUMN schedule jsonb;

-- +goose Down
ALTER TABLE workflow_version DROP COLUMN schedule;
