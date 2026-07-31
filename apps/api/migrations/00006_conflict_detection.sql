-- +goose Up
-- Conflict detection phase 1 (docs/architecture/conflict-detection.md,
-- docs/architecture/data-model.md): deterministic overlap warnings between
-- tasks that share a repository. One row per unordered task pair, normalized
-- so the pair is unique no matter which side detection ran from; the row is
-- rewritten whenever either side publishes. Reads filter both tasks to a
-- non-terminal phase, so a finished task's warnings disappear without a
-- delete.

CREATE TABLE task_conflicts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    repository_id UUID NOT NULL REFERENCES repositories (id),
    task_a_id UUID NOT NULL REFERENCES tasks (id),
    task_b_id UUID NOT NULL REFERENCES tasks (id),
    -- JSON string arrays. kinds are the detectors that fired
    -- (file_overlap, adjacent_lines, merge_conflict, migration, dependency);
    -- a row exists only when at least one fired.
    kinds JSONB NOT NULL CHECK (
        jsonb_typeof(kinds) = 'array' AND jsonb_array_length(kinds) >= 1),
    files JSONB NOT NULL CHECK (jsonb_typeof(files) = 'array'),
    detected_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT task_conflicts_pair_ordered CHECK (task_a_id < task_b_id),
    CONSTRAINT task_conflicts_pair_key UNIQUE (task_a_id, task_b_id)
);

-- Pair lookups by the first member use the unique pair index; this covers
-- lookups by the second.
CREATE INDEX task_conflicts_task_b_idx ON task_conflicts (task_b_id);

-- +goose Down
DROP TABLE task_conflicts;
