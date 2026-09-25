-- +goose Up
-- a revision attempt carries its own instructions, base, requester, and trigger; attempt 1 inherits the task's

ALTER TABLE task_attempts
    ADD COLUMN instructions TEXT
        CHECK (char_length(instructions) BETWEEN 1 AND 100000),
    ADD COLUMN requested_by_login TEXT
        CHECK (char_length(requested_by_login) BETWEEN 1 AND 255),
    ADD COLUMN trigger_comment_id BIGINT CHECK (trigger_comment_id > 0),
    ADD COLUMN trigger_check_run_id BIGINT CHECK (trigger_check_run_id > 0),
    ADD COLUMN trigger_check_run_completed_at TIMESTAMPTZ,
    ADD COLUMN feedback_json JSONB,
    ADD CONSTRAINT task_attempts_trigger_check_run_completion CHECK (
        trigger_check_run_completed_at IS NULL
        OR trigger_check_run_id IS NOT NULL);

-- +goose Down
ALTER TABLE task_attempts
    DROP CONSTRAINT task_attempts_trigger_check_run_completion,
    DROP COLUMN feedback_json,
    DROP COLUMN trigger_check_run_completed_at,
    DROP COLUMN trigger_check_run_id,
    DROP COLUMN trigger_comment_id,
    DROP COLUMN requested_by_login,
    DROP COLUMN instructions;
