-- Preserve the post-change task state needed to reconstruct historical reports.
-- Existing activity remains readable with a NULL snapshot and is treated as
-- legacy history by the report builder.

-- +goose Up

ALTER TABLE task_activity ADD COLUMN snapshot_json TEXT
    CHECK (snapshot_json IS NULL OR json_valid(snapshot_json));

-- The task activity triggers from migration 9 remain the single place that
-- decides whether a mutation is user-visible. This second-stage trigger fills
-- the snapshot for every activity producer, including REST, MCP, recurrence
-- jobs, relationship cleanup, and direct fixture inserts.
-- +goose StatementBegin
CREATE TRIGGER task_activity_snapshot_ai AFTER INSERT ON task_activity
WHEN NEW.snapshot_json IS NULL
BEGIN
    UPDATE task_activity
    SET snapshot_json = (
        SELECT json_object(
            'context_id', context_id,
            'project_id', project_id,
            'parent_id', parent_id,
            'recurrence_id', recurrence_id,
            'occurrence_on', occurrence_on,
            'source', source_key,
            'capture_method', capture_method,
            'title', title,
            'details', details,
            'status', status,
            'priority', priority,
            'due_on', due_on,
            'planned_on', planned_on,
            'day_slot', day_slot,
            'slot_order', slot_order,
            'estimate_minutes', estimate_minutes,
            'delegated_to_id', delegated_to_id,
            'blocked_by_id', blocked_by_id,
            'completed_at', completed_at,
            'cancelled_at', cancelled_at,
            'expired_at', expired_at,
            'created_at', created_at,
            'updated_at', updated_at,
            'deleted_at', deleted_at
        )
        FROM tasks
        WHERE id = NEW.task_id AND user_id = NEW.user_id
    )
    WHERE id = NEW.id;
END;
-- +goose StatementEnd

-- +goose Down

DROP TRIGGER task_activity_snapshot_ai;
ALTER TABLE task_activity DROP COLUMN snapshot_json;
