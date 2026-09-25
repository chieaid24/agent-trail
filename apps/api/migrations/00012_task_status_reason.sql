-- +goose Up
-- the reason recorded with the latest transition, so list views can show why a
-- task completed or was cancelled without reading its events

ALTER TABLE tasks
    ADD COLUMN status_reason TEXT
        CHECK (char_length(status_reason) BETWEEN 1 AND 1000);

UPDATE tasks t SET status_reason = latest.reason
FROM (
    SELECT DISTINCT ON (a.task_id) a.task_id,
        left(e.payload_json->>'reason', 1000) AS reason
    FROM activity_events e
    JOIN task_attempts a ON a.id = e.task_attempt_id
    JOIN tasks tk ON tk.id = a.task_id
    WHERE e.event_type = 'task.' || tk.status
    ORDER BY a.task_id, a.attempt_number DESC, e.sequence_number DESC
) latest
WHERE latest.task_id = t.id AND latest.reason <> '';

-- +goose Down
ALTER TABLE tasks DROP COLUMN status_reason;
