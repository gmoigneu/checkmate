-- Daily routines reuse recurrence templates while remaining a distinct product
-- surface. Slots are also available to ordinary tasks as a time-of-day plan.
--
-- Expiration is represented by expired_at while the stored status remains
-- cancelled. The store exposes such rows as status "expired". This avoids
-- rebuilding tasks merely to widen its status CHECK: tasks is a self-referencing
-- parent table, and rebuilding it with foreign keys enabled can delete user data.

-- +goose Up

ALTER TABLE recurrences ADD COLUMN kind TEXT NOT NULL DEFAULT 'classic'
    CHECK (kind IN ('classic', 'routine'));
ALTER TABLE recurrences ADD COLUMN day_slot TEXT
    CHECK (day_slot IN ('morning', 'midday', 'afternoon', 'evening', 'night'));
ALTER TABLE recurrences ADD COLUMN slot_order INTEGER NOT NULL DEFAULT 0
    CHECK (slot_order >= 0);

ALTER TABLE tasks ADD COLUMN day_slot TEXT
    CHECK (
        day_slot IS NULL OR (
            day_slot IN ('morning', 'midday', 'afternoon', 'evening', 'night')
            AND planned_on IS NOT NULL
        )
    );
ALTER TABLE tasks ADD COLUMN slot_order INTEGER NOT NULL DEFAULT 0
    CHECK (slot_order >= 0);
ALTER TABLE tasks ADD COLUMN expired_at TEXT;

CREATE INDEX recurrences_user_kind_slot_idx
    ON recurrences (user_id, kind, day_slot, slot_order)
    WHERE deleted_at IS NULL;
CREATE INDEX tasks_user_planned_slot_idx
    ON tasks (user_id, planned_on, day_slot, slot_order)
    WHERE deleted_at IS NULL;

DROP VIEW tasks_with_kind;

CREATE VIEW tasks_with_kind AS
SELECT
    t.*,
    CASE
        WHEN r.kind = 'routine' THEN 'routine'
        WHEN t.recurrence_id IS NOT NULL THEN 'recurring'
        WHEN t.delegated_to_id IS NOT NULL THEN 'delegated'
        WHEN t.blocked_by_id IS NOT NULL OR t.status = 'blocked' THEN 'blocked'
        WHEN EXISTS (
            SELECT 1 FROM tasks c WHERE c.parent_id = t.id AND c.deleted_at IS NULL
        ) THEN 'long'
        ELSE 'short'
    END AS kind
FROM tasks t
LEFT JOIN recurrences r ON r.id = t.recurrence_id;

-- +goose Down

DROP VIEW tasks_with_kind;
DROP INDEX tasks_user_planned_slot_idx;
DROP INDEX recurrences_user_kind_slot_idx;

ALTER TABLE tasks DROP COLUMN expired_at;
ALTER TABLE tasks DROP COLUMN slot_order;
ALTER TABLE tasks DROP COLUMN day_slot;

ALTER TABLE recurrences DROP COLUMN slot_order;
ALTER TABLE recurrences DROP COLUMN day_slot;
ALTER TABLE recurrences DROP COLUMN kind;

CREATE VIEW tasks_with_kind AS
SELECT
    t.*,
    CASE
        WHEN t.recurrence_id IS NOT NULL THEN 'recurring'
        WHEN t.delegated_to_id IS NOT NULL THEN 'delegated'
        WHEN t.blocked_by_id IS NOT NULL OR t.status = 'blocked' THEN 'blocked'
        WHEN EXISTS (
            SELECT 1 FROM tasks c WHERE c.parent_id = t.id AND c.deleted_at IS NULL
        ) THEN 'long'
        ELSE 'short'
    END AS kind
FROM tasks t;
