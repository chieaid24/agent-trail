-- +goose Up
-- Baseline migration: proves the migration tooling end to end. The task
-- domain schema lands with milestone 2 (docs/architecture/data-model.md).
SELECT 1;

-- +goose Down
SELECT 1;
