-- Checkmate initial schema.
--
-- Conventions used throughout:
--   * ids are UUIDv7 stored as TEXT (time-sortable, safe for clients to generate)
--   * timestamps are RFC3339 UTC text: 2026-07-25T14:03:11.482Z
--   * calendar dates (due_on, planned_on, ...) are plain YYYY-MM-DD, no timezone
--   * user data tables carry user_id, deleted_at (tombstone) and rev (sync cursor)

-- +goose Up

-- ---------------------------------------------------------------------------
-- Sync cursor
--
-- Storage is sqlite with no offline mode, so the server is the single source of
-- truth and there is no merge problem to solve. Clients still need cheap
-- deltas, so every mutable row gets a rev from this global counter and clients
-- poll `rev > last_seen`. Deletes are tombstones so they show up in the delta.
-- ---------------------------------------------------------------------------
CREATE TABLE change_seq (
    id    INTEGER PRIMARY KEY CHECK (id = 1),
    value INTEGER NOT NULL DEFAULT 0
);

INSERT INTO change_seq (id, value) VALUES (1, 0);

-- ---------------------------------------------------------------------------
-- Identity
-- ---------------------------------------------------------------------------
CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    email         TEXT NOT NULL COLLATE NOCASE,
    name          TEXT NOT NULL,
    -- Set once the web UI grows a login form; API clients use api_tokens.
    password_hash TEXT,
    timezone      TEXT NOT NULL DEFAULT 'UTC',
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE UNIQUE INDEX users_email_key ON users (email);

-- One token per device / integration (iOS, macOS, Chrome extension, hermes MCP)
-- so any single one can be revoked without touching the others.
CREATE TABLE api_tokens (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    token_hash   TEXT NOT NULL,
    scopes       TEXT NOT NULL DEFAULT 'tasks:read tasks:write',
    last_used_at TEXT,
    expires_at   TEXT,
    revoked_at   TEXT,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE UNIQUE INDEX api_tokens_hash_key ON api_tokens (token_hash);
CREATE INDEX api_tokens_user_idx ON api_tokens (user_id);

-- ---------------------------------------------------------------------------
-- Sources: where a task came from (brief section A).
--
-- Shared lookup rather than children of a context, because the same handful of
-- names repeat under every context. Keyed by slug so the API can speak
-- {"source": "slack"} without a join.
-- ---------------------------------------------------------------------------
CREATE TABLE sources (
    key        TEXT PRIMARY KEY,
    label      TEXT NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0
);

INSERT INTO sources (key, label, sort_order) VALUES
    ('self',        'Self',        10),
    ('email',       'Email',       20),
    ('slack',       'Slack',       30),
    ('google_chat', 'Google Chat', 40),
    ('meeting',     'Meeting',     50),
    ('phone',       'Phone',       60);

-- ---------------------------------------------------------------------------
-- Contexts: Upsun / Personal / Gaal / Arkea. Seeded per user at signup, not
-- here, since they are user data.
-- ---------------------------------------------------------------------------
CREATE TABLE contexts (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    slug        TEXT NOT NULL,
    color       TEXT,
    sort_order  INTEGER NOT NULL DEFAULT 0,
    archived_at TEXT,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    deleted_at  TEXT,
    rev         INTEGER NOT NULL DEFAULT 0
);

-- Partial so a deleted context frees its slug for reuse.
CREATE UNIQUE INDEX contexts_user_slug_key ON contexts (user_id, slug) WHERE deleted_at IS NULL;
CREATE INDEX contexts_rev_idx ON contexts (rev);

-- ---------------------------------------------------------------------------
-- Projects: optional grouping, scoped to exactly one context.
-- ---------------------------------------------------------------------------
CREATE TABLE projects (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    context_id  TEXT NOT NULL REFERENCES contexts (id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    description TEXT,
    status      TEXT NOT NULL DEFAULT 'active'
                CHECK (status IN ('active', 'paused', 'done', 'archived')),
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    deleted_at  TEXT,
    rev         INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX projects_user_context_idx ON projects (user_id, context_id);
CREATE INDEX projects_rev_idx ON projects (rev);

-- ---------------------------------------------------------------------------
-- People: delegation targets and follow-up counterparties.
-- ---------------------------------------------------------------------------
CREATE TABLE people (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    email      TEXT COLLATE NOCASE,
    -- Most people belong to one context (a colleague at Upsun); NULL means
    -- they show up everywhere.
    context_id TEXT REFERENCES contexts (id) ON DELETE SET NULL,
    notes      TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    deleted_at TEXT,
    rev        INTEGER NOT NULL DEFAULT 0
);

-- Quick capture upserts people by name, so keep that lookup unique and fast.
CREATE UNIQUE INDEX people_user_name_key ON people (user_id, name COLLATE NOCASE) WHERE deleted_at IS NULL;
CREATE INDEX people_rev_idx ON people (rev);

-- ---------------------------------------------------------------------------
-- Recurrences: the template for a repeating task. Occurrences are materialized
-- as real rows in tasks, so completion history survives and every list query
-- treats recurring and one-shot tasks identically.
-- ---------------------------------------------------------------------------
CREATE TABLE recurrences (
    id                TEXT PRIMARY KEY,
    user_id           TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    context_id        TEXT NOT NULL REFERENCES contexts (id) ON DELETE CASCADE,
    project_id        TEXT REFERENCES projects (id) ON DELETE SET NULL,
    source_key        TEXT REFERENCES sources (key),
    title             TEXT NOT NULL,
    details           TEXT,
    -- RFC 5545 RRULE, e.g. FREQ=WEEKLY;BYDAY=MO. Covers "daily, weekly, more?"
    -- without another migration.
    rrule             TEXT NOT NULL,
    timezone          TEXT NOT NULL DEFAULT 'UTC',
    estimate_minutes  INTEGER CHECK (estimate_minutes IS NULL OR estimate_minutes > 0),
    delegated_to_id   TEXT REFERENCES people (id) ON DELETE SET NULL,
    -- How many days ahead of its due date an occurrence appears in the inbox.
    lead_days         INTEGER NOT NULL DEFAULT 0 CHECK (lead_days >= 0),
    starts_on         TEXT NOT NULL,
    ends_on           TEXT,
    next_occurrence_on TEXT,
    last_spawned_on   TEXT,
    active            INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    deleted_at        TEXT,
    rev               INTEGER NOT NULL DEFAULT 0
);

-- The scheduler's hot query: which series are due to spawn?
CREATE INDEX recurrences_due_idx ON recurrences (active, next_occurrence_on) WHERE deleted_at IS NULL;
CREATE INDEX recurrences_user_idx ON recurrences (user_id);
CREATE INDEX recurrences_rev_idx ON recurrences (rev);

-- ---------------------------------------------------------------------------
-- Tasks (brief section C).
--
-- The four "types" from section B are not stored: they are derived from these
-- columns (see the tasks_with_kind view at the bottom). A stored type would go
-- stale the moment a subtask is added.
-- ---------------------------------------------------------------------------
CREATE TABLE tasks (
    id               TEXT PRIMARY KEY,
    user_id          TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,

    -- NULL only while the task sits in the inbox awaiting triage: quick capture
    -- from a widget or voice note should never be blocked on picking a context.
    context_id       TEXT REFERENCES contexts (id) ON DELETE SET NULL,
    project_id       TEXT REFERENCES projects (id) ON DELETE SET NULL,
    parent_id        TEXT REFERENCES tasks (id) ON DELETE CASCADE,

    -- Set on occurrences spawned from a series; occurrence_on is the date this
    -- instance stands for, and makes spawning idempotent.
    recurrence_id    TEXT REFERENCES recurrences (id) ON DELETE SET NULL,
    occurrence_on    TEXT,

    -- Two distinct axes: where the task came from vs. how it entered Checkmate.
    source_key       TEXT REFERENCES sources (key),
    capture_method   TEXT NOT NULL DEFAULT 'api'
                     CHECK (capture_method IN ('form', 'api', 'hermes', 'chrome_ext', 'ios_widget', 'voice', 'recurrence')),

    title            TEXT NOT NULL CHECK (length(trim(title)) > 0),
    details          TEXT,

    status           TEXT NOT NULL DEFAULT 'inbox'
                     CHECK (status IN ('inbox', 'todo', 'in_progress', 'blocked', 'delegated', 'done', 'cancelled')),

    due_on           TEXT CHECK (due_on IS NULL OR due_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    planned_on       TEXT CHECK (planned_on IS NULL OR planned_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    estimate_minutes INTEGER CHECK (estimate_minutes IS NULL OR estimate_minutes > 0),

    delegated_to_id  TEXT REFERENCES people (id) ON DELETE SET NULL,
    blocked_by_id    TEXT REFERENCES tasks (id) ON DELETE SET NULL,

    reference_url    TEXT,
    reference_label  TEXT,

    completed_at     TEXT,
    cancelled_at     TEXT,
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    deleted_at       TEXT,
    rev              INTEGER NOT NULL DEFAULT 0,

    CHECK (parent_id IS NULL OR parent_id <> id),
    CHECK (blocked_by_id IS NULL OR blocked_by_id <> id),
    -- Delegation must name someone, otherwise "what am I waiting on?" leaks rows.
    CHECK (status <> 'delegated' OR delegated_to_id IS NOT NULL)
);

CREATE INDEX tasks_user_status_idx    ON tasks (user_id, status)                 WHERE deleted_at IS NULL;
CREATE INDEX tasks_user_planned_idx   ON tasks (user_id, planned_on)             WHERE deleted_at IS NULL;
CREATE INDEX tasks_user_due_idx       ON tasks (user_id, due_on)                 WHERE deleted_at IS NULL;
CREATE INDEX tasks_user_context_idx   ON tasks (user_id, context_id, status)     WHERE deleted_at IS NULL;
CREATE INDEX tasks_parent_idx         ON tasks (parent_id)                       WHERE parent_id IS NOT NULL;
CREATE INDEX tasks_blocked_by_idx     ON tasks (blocked_by_id)                   WHERE blocked_by_id IS NOT NULL;
CREATE INDEX tasks_delegated_to_idx   ON tasks (delegated_to_id)                 WHERE delegated_to_id IS NOT NULL;
CREATE INDEX tasks_rev_idx            ON tasks (rev);

-- One task per series per occurrence date: the spawner can run as often as it
-- likes without creating duplicates.
CREATE UNIQUE INDEX tasks_occurrence_key ON tasks (recurrence_id, occurrence_on)
    WHERE recurrence_id IS NOT NULL AND occurrence_on IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Derived task kind (brief section B).
--
-- Precedence is deliberate: a recurring task that is also delegated reads as
-- recurring, because that is the thing you manage it by.
-- ---------------------------------------------------------------------------
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

-- ---------------------------------------------------------------------------
-- rev triggers.
--
-- Each pair stamps a fresh global rev on insert and on any update. The WHEN
-- guard on the update trigger stops the trigger's own write from recursing,
-- so these are correct whether or not PRAGMA recursive_triggers is on.
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE TRIGGER contexts_rev_ai AFTER INSERT ON contexts
BEGIN
    UPDATE change_seq SET value = value + 1;
    UPDATE contexts SET rev = (SELECT value FROM change_seq) WHERE rowid = NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER contexts_rev_au AFTER UPDATE ON contexts
WHEN NEW.rev = OLD.rev
BEGIN
    UPDATE change_seq SET value = value + 1;
    UPDATE contexts SET rev = (SELECT value FROM change_seq) WHERE rowid = NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER projects_rev_ai AFTER INSERT ON projects
BEGIN
    UPDATE change_seq SET value = value + 1;
    UPDATE projects SET rev = (SELECT value FROM change_seq) WHERE rowid = NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER projects_rev_au AFTER UPDATE ON projects
WHEN NEW.rev = OLD.rev
BEGIN
    UPDATE change_seq SET value = value + 1;
    UPDATE projects SET rev = (SELECT value FROM change_seq) WHERE rowid = NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER people_rev_ai AFTER INSERT ON people
BEGIN
    UPDATE change_seq SET value = value + 1;
    UPDATE people SET rev = (SELECT value FROM change_seq) WHERE rowid = NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER people_rev_au AFTER UPDATE ON people
WHEN NEW.rev = OLD.rev
BEGIN
    UPDATE change_seq SET value = value + 1;
    UPDATE people SET rev = (SELECT value FROM change_seq) WHERE rowid = NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER recurrences_rev_ai AFTER INSERT ON recurrences
BEGIN
    UPDATE change_seq SET value = value + 1;
    UPDATE recurrences SET rev = (SELECT value FROM change_seq) WHERE rowid = NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER recurrences_rev_au AFTER UPDATE ON recurrences
WHEN NEW.rev = OLD.rev
BEGIN
    UPDATE change_seq SET value = value + 1;
    UPDATE recurrences SET rev = (SELECT value FROM change_seq) WHERE rowid = NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER tasks_rev_ai AFTER INSERT ON tasks
BEGIN
    UPDATE change_seq SET value = value + 1;
    UPDATE tasks SET rev = (SELECT value FROM change_seq) WHERE rowid = NEW.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER tasks_rev_au AFTER UPDATE ON tasks
WHEN NEW.rev = OLD.rev
BEGIN
    UPDATE change_seq SET value = value + 1;
    UPDATE tasks SET rev = (SELECT value FROM change_seq) WHERE rowid = NEW.rowid;
END;
-- +goose StatementEnd

-- +goose Down

DROP TRIGGER tasks_rev_au;
DROP TRIGGER tasks_rev_ai;
DROP TRIGGER recurrences_rev_au;
DROP TRIGGER recurrences_rev_ai;
DROP TRIGGER people_rev_au;
DROP TRIGGER people_rev_ai;
DROP TRIGGER projects_rev_au;
DROP TRIGGER projects_rev_ai;
DROP TRIGGER contexts_rev_au;
DROP TRIGGER contexts_rev_ai;
DROP VIEW tasks_with_kind;
DROP TABLE tasks;
DROP TABLE recurrences;
DROP TABLE people;
DROP TABLE projects;
DROP TABLE contexts;
DROP TABLE sources;
DROP TABLE api_tokens;
DROP TABLE users;
DROP TABLE change_seq;
