-- +goose Up
-- Decision 9 (build-conversation spec): the steps column becomes the
-- user-facing {v, steps: [{id, text}]} artifact, and the execution form
-- moves to a server-derived compiled column written at approval time by
-- the deterministic compiler.
ALTER TABLE workflow_version ADD COLUMN compiled jsonb;

-- Dev-data migration (pre-release, spent alpha notice): existing rows
-- copy their old execution-form steps document into compiled, stamped
-- compiler_v 0, and synthesize a v1 user-facing artifact from the old
-- kickoff — dev data stays runnable, nothing is silently reinterpreted.
UPDATE workflow_version
SET compiled = steps || '{"compiler_v": 0}'::jsonb,
    steps = jsonb_build_object(
        'v', 1,
        'steps', jsonb_build_array(jsonb_build_object(
            'id', 'job',
            'text', coalesce(steps ->> 'kickoff', ''))));

-- +goose Down
-- The steps transform is one-way (the old execution form survives only in
-- compiled); down just drops the derived column.
ALTER TABLE workflow_version DROP COLUMN compiled;
