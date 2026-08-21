-- +goose Up
ALTER TABLE task_conflicts
    ADD COLUMN semantic_severity TEXT CHECK (
        semantic_severity IS NULL OR semantic_severity IN ('low', 'medium', 'high')),
    ADD COLUMN semantic_explanation TEXT,
    ADD COLUMN semantic_evidence JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (
        jsonb_typeof(semantic_evidence) = 'array'
        AND NOT jsonb_path_exists(semantic_evidence, '$[*] ? (@.type() != "string")'));

ALTER TABLE task_conflicts DROP CONSTRAINT task_conflicts_kinds_check;
ALTER TABLE task_conflicts ADD CONSTRAINT task_conflicts_kinds_check CHECK (
    jsonb_typeof(kinds) = 'array'
    AND jsonb_array_length(kinds) >= 1
    AND kinds <@ '["file_overlap", "adjacent_lines", "merge_conflict",
        "migration", "dependency", "semantic"]'::jsonb);

ALTER TABLE task_conflicts ADD CONSTRAINT task_conflicts_semantic_fields_check CHECK (
    (kinds ? 'semantic') = (semantic_severity IS NOT NULL
        AND semantic_explanation IS NOT NULL
        AND jsonb_array_length(semantic_evidence) > 0));

-- +goose Down
ALTER TABLE task_conflicts DROP CONSTRAINT task_conflicts_semantic_fields_check;
DELETE FROM task_conflicts WHERE kinds = '["semantic"]'::jsonb;
UPDATE task_conflicts
SET kinds = kinds - 'semantic', semantic_severity = NULL,
    semantic_explanation = NULL, semantic_evidence = '[]'::jsonb
WHERE kinds ? 'semantic';
ALTER TABLE task_conflicts DROP CONSTRAINT task_conflicts_kinds_check;
ALTER TABLE task_conflicts ADD CONSTRAINT task_conflicts_kinds_check CHECK (
    jsonb_typeof(kinds) = 'array'
    AND jsonb_array_length(kinds) >= 1
    AND kinds <@ '["file_overlap", "adjacent_lines", "merge_conflict",
        "migration", "dependency"]'::jsonb);
ALTER TABLE task_conflicts
    DROP COLUMN semantic_evidence,
    DROP COLUMN semantic_explanation,
    DROP COLUMN semantic_severity;
