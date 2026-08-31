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

-- Approved implies a compiled document — enforced by the database, not by
-- application discipline (the pattern 00003 set for this table). The
-- jsonb_typeof check also rejects JSONB null and non-object values, which
-- would satisfy a bare IS NOT NULL yet decode to an unusable zero-valued
-- context. Safe against existing data: the backfill above populated every
-- row with an object.
ALTER TABLE workflow_version ADD CONSTRAINT workflow_version_approved_compiled
    CHECK (status <> 'approved'
           OR (compiled IS NOT NULL AND jsonb_typeof(compiled) = 'object'));

-- +goose Down
ALTER TABLE workflow_version DROP CONSTRAINT workflow_version_approved_compiled;
-- The steps transform is one-way (the old execution form survives only in
-- compiled); down just drops the derived column.
ALTER TABLE workflow_version DROP COLUMN compiled;
