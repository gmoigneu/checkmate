-- Identify which delete a tombstone belongs to.
--
-- Deleting a task tombstones its whole subtree, and restoring one has to bring back
-- exactly that set: a child deleted separately and earlier must stay deleted, or a
-- restore silently resurrects work removed on purpose.
--
-- The first attempt grouped by deleted_at equality, on the reasoning that sqlite
-- fixes the current time for the duration of a statement so one delete stamps its
-- subtree identically. That part is true, but the timestamps only carry
-- milliseconds, so two deletes issued back to back land in the same millisecond and
-- become indistinguishable. A test caught it. An explicit batch id is exact
-- regardless of timing.
--
-- ALTER TABLE ADD COLUMN is safe here: it does not rebuild the table, so it avoids
-- the implicit-DELETE cascade that makes rebuilding a parent table dangerous.

-- +goose Up

ALTER TABLE tasks ADD COLUMN deleted_batch TEXT;

-- Restore looks rows up by batch, so give it an index. Partial, because only
-- tombstones carry one.
CREATE INDEX tasks_deleted_batch_idx ON tasks (deleted_batch)
    WHERE deleted_batch IS NOT NULL;

-- +goose Down

DROP INDEX tasks_deleted_batch_idx;

ALTER TABLE tasks DROP COLUMN deleted_batch;
