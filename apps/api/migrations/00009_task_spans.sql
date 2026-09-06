-- +goose Up
-- trace read model, populated by the worker's otel exporter

ALTER TABLE task_attempts ADD CONSTRAINT task_attempts_id_task_unique
    UNIQUE (id, task_id);

CREATE TABLE task_spans (
    task_id UUID NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    task_attempt_id UUID,
    trace_id TEXT NOT NULL CHECK (trace_id ~ '^[0-9a-f]{32}$'),
    span_id TEXT NOT NULL CHECK (span_id ~ '^[0-9a-f]{16}$'),
    parent_span_id TEXT CHECK (parent_span_id ~ '^[0-9a-f]{16}$'),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 500),
    kind TEXT NOT NULL CHECK (char_length(kind) BETWEEN 1 AND 50),
    start_time TIMESTAMPTZ NOT NULL,
    end_time TIMESTAMPTZ NOT NULL,
    attributes_json JSONB NOT NULL DEFAULT '{}',
    status_code TEXT NOT NULL CHECK (status_code IN ('Unset', 'Error', 'Ok')),
    status_message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (task_id, trace_id, span_id),
    FOREIGN KEY (task_attempt_id, task_id)
        REFERENCES task_attempts (id, task_id) ON DELETE CASCADE,
    CHECK (end_time >= start_time)
);

CREATE INDEX task_spans_waterfall_idx
    ON task_spans (task_id, start_time, span_id);

-- +goose Down
DROP TABLE task_spans;
ALTER TABLE task_attempts DROP CONSTRAINT task_attempts_id_task_unique;
