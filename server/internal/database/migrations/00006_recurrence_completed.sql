-- Distinguish a paused recurrence from a finished one.
--
-- `active` alone conflated two states that mean opposite things to a person: "I
-- turned this off" and "this ran its course". A series that reached its ends_on or
-- exhausted a COUNT= was indistinguishable from one deliberately paused, so a UI
-- could only offer to resume both, and resuming a finished series does nothing.
--
-- completed_at is set by the spawner when it retires a series, and only then. A
-- user pausing one leaves it null. The API derives a three-way `state` from the
-- pair, so nothing has to remember the combination:
--
--   active = 1                              -> active
--   active = 0 AND completed_at IS NULL     -> paused    (a person turned it off)
--   active = 0 AND completed_at IS NOT NULL -> finished   (it ran out)
--
-- ALTER TABLE ADD COLUMN rather than a rebuild: recurrences is a parent of the
-- tasks it spawned, and with foreign keys on, DROP TABLE fires ON DELETE CASCADE.

-- +goose Up

ALTER TABLE recurrences ADD COLUMN completed_at TEXT;

-- Existing inactive series cannot be classified retroactively: nothing recorded
-- why they stopped. They are left with a null completed_at, which reads as paused
-- -- the more forgiving of the two, since it offers to resume rather than
-- presenting a series as over when it may not be.

-- +goose Down

ALTER TABLE recurrences DROP COLUMN completed_at;
