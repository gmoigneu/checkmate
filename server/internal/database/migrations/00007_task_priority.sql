-- Add an optional, deliberately small priority vocabulary to tasks.
--
-- Priority is independent from status and dates: it says how important the work
-- is, not when it is due or whether it has started. NULL means the user has not
-- assigned a priority.

-- +goose Up

ALTER TABLE tasks ADD COLUMN priority TEXT
    CHECK (priority IN ('urgent', 'high', 'medium', 'low'));

-- Match the default task listing: ranked priorities first, unprioritized work
-- last, and newest first within each rank.
CREATE INDEX tasks_user_priority_idx ON tasks (
    user_id,
    CASE priority
        WHEN 'urgent' THEN 0
        WHEN 'high' THEN 1
        WHEN 'medium' THEN 2
        WHEN 'low' THEN 3
        ELSE 4
    END,
    id DESC
) WHERE deleted_at IS NULL;

-- +goose Down

DROP INDEX tasks_user_priority_idx;
ALTER TABLE tasks DROP COLUMN priority;
