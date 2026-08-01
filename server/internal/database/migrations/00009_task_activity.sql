-- Keep an append-only audit trail for every user-visible task mutation.
--
-- Triggers live at the database boundary so changes made by REST, MCP,
-- recurrence jobs, and relationship cleanup all produce the same history.
-- The update trigger deliberately ignores rev and updated_at: rev stamping is
-- itself an UPDATE, and recording it would duplicate every real mutation.

-- +goose Up

CREATE TABLE task_activity (
    id             INTEGER PRIMARY KEY,
    user_id        TEXT NOT NULL,
    task_id        TEXT NOT NULL,
    task_title     TEXT NOT NULL,
    action         TEXT NOT NULL CHECK (action IN ('created', 'updated', 'deleted', 'restored')),
    changed_fields TEXT NOT NULL DEFAULT '',
    status_before  TEXT,
    status_after   TEXT,
    occurred_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX task_activity_user_id_idx
    ON task_activity (user_id, id DESC);
CREATE INDEX task_activity_user_task_idx
    ON task_activity (user_id, task_id, id DESC);

-- User deletion cascades through contexts before it reaches tasks. Those
-- relationship changes are themselves task mutations, so they can append
-- activity while the cascade is in flight. Clean the final feed in an AFTER
-- trigger, once all child actions have finished.
-- +goose StatementBegin
CREATE TRIGGER users_activity_ad AFTER DELETE ON users
BEGIN
    DELETE FROM task_activity WHERE user_id = OLD.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER tasks_activity_ai AFTER INSERT ON tasks
BEGIN
    INSERT INTO task_activity (
        user_id, task_id, task_title, action, status_after
    ) VALUES (
        NEW.user_id, NEW.id, NEW.title, 'created', NEW.status
    );
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER tasks_activity_au AFTER UPDATE ON tasks
WHEN
    OLD.context_id IS NOT NEW.context_id OR
    OLD.project_id IS NOT NEW.project_id OR
    OLD.parent_id IS NOT NEW.parent_id OR
    OLD.recurrence_id IS NOT NEW.recurrence_id OR
    OLD.occurrence_on IS NOT NEW.occurrence_on OR
    OLD.source_key IS NOT NEW.source_key OR
    OLD.capture_method IS NOT NEW.capture_method OR
    OLD.title IS NOT NEW.title OR
    OLD.details IS NOT NEW.details OR
    OLD.status IS NOT NEW.status OR
    OLD.priority IS NOT NEW.priority OR
    OLD.due_on IS NOT NEW.due_on OR
    OLD.planned_on IS NOT NEW.planned_on OR
    OLD.day_slot IS NOT NEW.day_slot OR
    OLD.slot_order IS NOT NEW.slot_order OR
    OLD.estimate_minutes IS NOT NEW.estimate_minutes OR
    OLD.delegated_to_id IS NOT NEW.delegated_to_id OR
    OLD.blocked_by_id IS NOT NEW.blocked_by_id OR
    OLD.reference_url IS NOT NEW.reference_url OR
    OLD.reference_label IS NOT NEW.reference_label OR
    OLD.deleted_at IS NOT NEW.deleted_at OR
    OLD.deleted_batch IS NOT NEW.deleted_batch OR
    OLD.expired_at IS NOT NEW.expired_at
BEGIN
    INSERT INTO task_activity (
        user_id, task_id, task_title, action, changed_fields,
        status_before, status_after
    ) VALUES (
        NEW.user_id,
        NEW.id,
        NEW.title,
        CASE
            WHEN OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL THEN 'deleted'
            WHEN OLD.deleted_at IS NOT NULL AND NEW.deleted_at IS NULL THEN 'restored'
            ELSE 'updated'
        END,
        trim(
            CASE WHEN OLD.context_id IS NOT NEW.context_id THEN 'context_id,' ELSE '' END ||
            CASE WHEN OLD.project_id IS NOT NEW.project_id THEN 'project_id,' ELSE '' END ||
            CASE WHEN OLD.parent_id IS NOT NEW.parent_id THEN 'parent_id,' ELSE '' END ||
            CASE WHEN OLD.recurrence_id IS NOT NEW.recurrence_id THEN 'recurrence_id,' ELSE '' END ||
            CASE WHEN OLD.occurrence_on IS NOT NEW.occurrence_on THEN 'occurrence_on,' ELSE '' END ||
            CASE WHEN OLD.source_key IS NOT NEW.source_key THEN 'source,' ELSE '' END ||
            CASE WHEN OLD.capture_method IS NOT NEW.capture_method THEN 'capture_method,' ELSE '' END ||
            CASE WHEN OLD.title IS NOT NEW.title THEN 'title,' ELSE '' END ||
            CASE WHEN OLD.details IS NOT NEW.details THEN 'details,' ELSE '' END ||
            CASE WHEN OLD.status IS NOT NEW.status THEN 'status,' ELSE '' END ||
            CASE WHEN OLD.priority IS NOT NEW.priority THEN 'priority,' ELSE '' END ||
            CASE WHEN OLD.due_on IS NOT NEW.due_on THEN 'due_on,' ELSE '' END ||
            CASE WHEN OLD.planned_on IS NOT NEW.planned_on THEN 'planned_on,' ELSE '' END ||
            CASE WHEN OLD.day_slot IS NOT NEW.day_slot THEN 'day_slot,' ELSE '' END ||
            CASE WHEN OLD.slot_order IS NOT NEW.slot_order THEN 'slot_order,' ELSE '' END ||
            CASE WHEN OLD.estimate_minutes IS NOT NEW.estimate_minutes THEN 'estimate_minutes,' ELSE '' END ||
            CASE WHEN OLD.delegated_to_id IS NOT NEW.delegated_to_id THEN 'delegated_to_id,' ELSE '' END ||
            CASE WHEN OLD.blocked_by_id IS NOT NEW.blocked_by_id THEN 'blocked_by_id,' ELSE '' END ||
            CASE WHEN OLD.reference_url IS NOT NEW.reference_url THEN 'reference_url,' ELSE '' END ||
            CASE WHEN OLD.reference_label IS NOT NEW.reference_label THEN 'reference_label,' ELSE '' END ||
            CASE WHEN OLD.deleted_at IS NOT NEW.deleted_at THEN 'deleted_at,' ELSE '' END ||
            CASE WHEN OLD.deleted_batch IS NOT NEW.deleted_batch THEN 'deleted_batch,' ELSE '' END ||
            CASE WHEN OLD.expired_at IS NOT NEW.expired_at THEN 'expired_at,' ELSE '' END,
            ','
        ),
        OLD.status,
        NEW.status
    );
END;
-- +goose StatementEnd

-- +goose Down

DROP TRIGGER tasks_activity_au;
DROP TRIGGER tasks_activity_ai;
DROP TRIGGER users_activity_ad;
DROP TABLE task_activity;
