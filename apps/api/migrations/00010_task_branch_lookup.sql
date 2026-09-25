-- +goose Up
-- a closed pull request resolves its task by repository and head branch

CREATE INDEX tasks_repository_working_branch_idx
    ON tasks (repository_id, working_branch)
    WHERE working_branch IS NOT NULL;

-- +goose Down
DROP INDEX tasks_repository_working_branch_idx;
