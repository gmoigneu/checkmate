-- Add an optional, deliberately small priority vocabulary to tasks.
--
-- Priority is independent from status and dates: it says how important the work
-- is, not when it is due or whether it has started. NULL means the user has not
-- assigned a priority.

-- +goose Up

ALTER TABLE tasks ADD COLUMN priority TEXT
    CHECK (priority IN ('urgent', 'high', 'medium', 'low'));

-- Match the default task listing: ranked priorities first, unprioritized work
-- last, and newest first within each rank. This rank expression is duplicated by
-- priorityRankExpr in tasksort.go; keep the two immutable definitions aligned.
CREATE INDEX tasks_user_priority_idx ON tasks (
    user_id,
    coalesce(CASE priority
        WHEN 'urgent' THEN 0
        WHEN 'high' THEN 1
        WHEN 'medium' THEN 2
        WHEN 'low' THEN 3
    END, 4),
    id DESC
) WHERE deleted_at IS NULL;

-- +goose Down

DROP INDEX tasks_user_priority_idx;
ALTER TABLE tasks DROP COLUMN priority;
