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
        jsonb_typeof(kinds) = 'array'
        AND jsonb_array_length(kinds) >= 1
        AND kinds <@ '["file_overlap", "adjacent_lines", "merge_conflict",
            "migration", "dependency"]'::jsonb),
    files JSONB NOT NULL CHECK (
        jsonb_typeof(files) = 'array'
        AND NOT jsonb_path_exists(files, '$[*] ? (@.type() != "string")')),
    detected_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT task_conflicts_pair_ordered CHECK (task_a_id < task_b_id),
    CONSTRAINT task_conflicts_pair_key UNIQUE (task_a_id, task_b_id)
);

-- Pair lookups by the first member use the unique pair index; this covers
-- lookups by the second.
CREATE INDEX task_conflicts_task_b_idx ON task_conflicts (task_b_id);

-- +goose StatementBegin
CREATE FUNCTION validate_task_conflict_repository() RETURNS TRIGGER AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM tasks a, tasks b
        WHERE a.id = NEW.task_a_id
          AND b.id = NEW.task_b_id
          AND a.repository_id = NEW.repository_id
          AND b.repository_id = NEW.repository_id
    ) THEN
        RAISE EXCEPTION 'task conflict repository mismatch'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER task_conflicts_repository_check
BEFORE INSERT OR UPDATE ON task_conflicts
FOR EACH ROW EXECUTE FUNCTION validate_task_conflict_repository();

-- +goose Down
DROP TABLE task_conflicts;
DROP FUNCTION validate_task_conflict_repository();
