-- +goose Up
-- trusted_execution separates platform-run checks from agent-reported ones

CREATE TABLE validation_results (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_attempt_id UUID NOT NULL REFERENCES task_attempts (id),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    category TEXT NOT NULL CHECK (category IN (
        'unit_test', 'integration_test', 'lint', 'format', 'typecheck',
        'security', 'dependency', 'migration', 'build', 'custom')),
    command_json JSONB NOT NULL,
    status TEXT NOT NULL
        CHECK (status IN ('passed', 'failed', 'timed_out', 'error')),
    exit_code INTEGER,
    duration_ms BIGINT NOT NULL CHECK (duration_ms >= 0),
    summary TEXT NOT NULL DEFAULT '',
    report_object_key TEXT,
    trusted_execution BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- expired-lease zombie owner replaying checks cannot double-record
    CONSTRAINT validation_results_attempt_name_key
        UNIQUE (task_attempt_id, name)
);

CREATE INDEX validation_results_attempt_idx
    ON validation_results (task_attempt_id, created_at);

CREATE TABLE evidence_reports (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_attempt_id UUID NOT NULL UNIQUE REFERENCES task_attempts (id),
    schema_version INTEGER NOT NULL,
    summary_markdown TEXT NOT NULL,
    report_json JSONB NOT NULL,
    report_object_key TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE evidence_reports;
DROP TABLE validation_results;
