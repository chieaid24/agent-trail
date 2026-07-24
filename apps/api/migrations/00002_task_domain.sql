-- +goose Up
-- Task domain: tasks, task attempts, and the append-only activity timeline.
-- Spec: docs/architecture/data-model.md, docs/architecture/task-state-machine.md.
-- organization_id / repository_id gain FKs when the GitHub integration
-- milestone creates those tables; runner_id likewise with the runner milestone.

CREATE TABLE tasks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID,
    repository_id UUID,
    source_type TEXT NOT NULL DEFAULT 'api'
        CHECK (source_type IN ('api', 'github_issue')),
    source_issue_number BIGINT CHECK (source_issue_number > 0),
    source_comment_id BIGINT CHECK (source_comment_id > 0),
    title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 500),
    instructions TEXT NOT NULL
        CHECK (char_length(instructions) BETWEEN 1 AND 100000),
    status TEXT NOT NULL DEFAULT 'created' CHECK (status IN (
        'created', 'queued', 'provisioning', 'planning', 'executing',
        'validating', 'publishing', 'awaiting_review', 'revision_requested',
        'completed', 'failed', 'cancelled', 'timed_out')),
    phase TEXT NOT NULL DEFAULT 'pending'
        CHECK (phase IN ('pending', 'running', 'review', 'terminal')),
    priority INTEGER NOT NULL DEFAULT 0 CHECK (priority BETWEEN -100 AND 100),
    base_branch TEXT NOT NULL DEFAULT 'main'
        CHECK (char_length(base_branch) BETWEEN 1 AND 255),
    base_commit_sha TEXT CHECK (base_commit_sha ~ '^[0-9a-f]{40}$'),
    working_branch TEXT CHECK (char_length(working_branch) BETWEEN 1 AND 255),
    agent_provider TEXT CHECK (char_length(agent_provider) BETWEEN 1 AND 100),
    agent_model TEXT CHECK (char_length(agent_model) BETWEEN 1 AND 100),
    policy_id UUID,
    requested_by_user_id UUID,
    max_runtime_seconds INTEGER CHECK (max_runtime_seconds BETWEEN 1 AND 86400),
    max_cost_usd NUMERIC(10, 2) CHECK (max_cost_usd >= 0),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    cancel_requested_at TIMESTAMPTZ,
    failure_code TEXT CHECK (char_length(failure_code) BETWEEN 1 AND 100),
    failure_message TEXT
        CHECK (char_length(failure_message) BETWEEN 1 AND 10000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
    -- phase is derived from status; keep the pair consistent at the DB level.
    CONSTRAINT tasks_phase_matches_status CHECK (phase = CASE
        WHEN status IN ('created', 'queued') THEN 'pending'
        WHEN status IN ('provisioning', 'planning', 'executing',
                        'validating', 'publishing') THEN 'running'
        WHEN status IN ('awaiting_review', 'revision_requested') THEN 'review'
        ELSE 'terminal' END),
    CONSTRAINT tasks_terminal_has_completed_at CHECK (
        (status IN ('completed', 'failed', 'cancelled', 'timed_out'))
        = (completed_at IS NOT NULL))
);

CREATE INDEX tasks_status_idx ON tasks (status);
CREATE INDEX tasks_created_at_idx ON tasks (created_at DESC, id DESC);

CREATE TABLE task_attempts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id UUID NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    attempt_number INTEGER NOT NULL CHECK (attempt_number >= 1),
    runner_id UUID,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN (
        'active', 'superseded', 'completed', 'failed', 'cancelled',
        'timed_out')),
    base_commit_sha TEXT CHECK (base_commit_sha ~ '^[0-9a-f]{40}$'),
    final_commit_sha TEXT CHECK (final_commit_sha ~ '^[0-9a-f]{40}$'),
    pull_request_number INTEGER CHECK (pull_request_number > 0),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    failure_code TEXT CHECK (char_length(failure_code) BETWEEN 1 AND 100),
    failure_message TEXT
        CHECK (char_length(failure_message) BETWEEN 1 AND 10000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (task_id, attempt_number)
);

-- Invariant: a task has exactly one active attempt until it terminates.
CREATE UNIQUE INDEX task_attempts_one_active_idx
    ON task_attempts (task_id) WHERE status = 'active';

CREATE TABLE activity_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_attempt_id UUID NOT NULL
        REFERENCES task_attempts (id) ON DELETE CASCADE,
    sequence_number BIGINT NOT NULL CHECK (sequence_number >= 1),
    event_type TEXT NOT NULL CHECK (char_length(event_type) BETWEEN 1 AND 200),
    source TEXT NOT NULL CHECK (source IN ('api', 'system', 'runner', 'agent')),
    "timestamp" TIMESTAMPTZ NOT NULL DEFAULT now(),
    payload_json JSONB NOT NULL DEFAULT '{}',
    redaction_status TEXT NOT NULL DEFAULT 'none'
        CHECK (redaction_status IN ('none', 'pending', 'redacted')),
    idempotency_key TEXT CHECK (char_length(idempotency_key) BETWEEN 1 AND 200),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (task_attempt_id, sequence_number)
);

-- Backstop for duplicate transition messages (the store also checks first).
CREATE UNIQUE INDEX activity_events_idempotency_idx
    ON activity_events (task_attempt_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- The timeline is append-only: reject UPDATE and DELETE at the DB level.
-- This also blocks task deletion (the FK cascade trips the trigger), which
-- is intended: audit history outlives the task.
-- +goose StatementBegin
CREATE FUNCTION activity_events_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'activity_events is append-only';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER activity_events_append_only
    BEFORE UPDATE OR DELETE ON activity_events
    FOR EACH ROW EXECUTE FUNCTION activity_events_append_only();

-- +goose Down
DROP TABLE activity_events;
DROP FUNCTION activity_events_append_only;
DROP TABLE task_attempts;
DROP TABLE tasks;
