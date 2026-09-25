-- +goose Up
-- every attempt records its requester and trigger; a revision also carries its own instructions,
-- base, and feedback, while attempt 1 inherits the task's instructions and base

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
        OR trigger_check_run_id IS NOT NULL),
    ADD CONSTRAINT task_attempts_feedback_is_bounded_array CHECK (
        feedback_json IS NULL
        OR (jsonb_typeof(feedback_json) = 'array'
            AND jsonb_array_length(feedback_json) <= 1000));

-- +goose Down
ALTER TABLE task_attempts
    DROP CONSTRAINT task_attempts_feedback_is_bounded_array,
    DROP CONSTRAINT task_attempts_trigger_check_run_completion,
    DROP COLUMN feedback_json,
    DROP COLUMN trigger_check_run_completed_at,
    DROP COLUMN trigger_check_run_id,
    DROP COLUMN trigger_comment_id,
    DROP COLUMN requested_by_login,
    DROP COLUMN instructions;
