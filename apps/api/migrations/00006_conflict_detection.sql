-- +goose Up
ALTER TABLE tasks ADD CONSTRAINT tasks_id_repository_key
    UNIQUE (id, repository_id);

CREATE TABLE task_conflicts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    repository_id UUID NOT NULL REFERENCES repositories (id),
    task_a_id UUID NOT NULL,
    task_b_id UUID NOT NULL,
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
    CONSTRAINT task_conflicts_pair_key UNIQUE (task_a_id, task_b_id),
    CONSTRAINT task_conflicts_task_a_repository_fk
        FOREIGN KEY (task_a_id, repository_id)
        REFERENCES tasks (id, repository_id),
    CONSTRAINT task_conflicts_task_b_repository_fk
        FOREIGN KEY (task_b_id, repository_id)
        REFERENCES tasks (id, repository_id)
);

-- Pair lookups by the first member use the unique pair index; this covers
-- lookups by the second.
CREATE INDEX task_conflicts_task_b_idx ON task_conflicts (task_b_id);

-- +goose Down
DROP TABLE task_conflicts;
ALTER TABLE tasks DROP CONSTRAINT tasks_id_repository_key;
