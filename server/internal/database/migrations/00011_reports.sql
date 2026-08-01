-- Saved AI-assisted reports and their immutable generation versions.

-- +goose Up

CREATE TABLE reports (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title      TEXT NOT NULL CHECK (length(trim(title)) > 0),
    start_on   TEXT NOT NULL,
    end_on     TEXT NOT NULL,
    focus      TEXT,
    include_inbox INTEGER NOT NULL DEFAULT 0 CHECK (include_inbox IN (0, 1)),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX reports_user_updated_idx ON reports (user_id, updated_at DESC);

CREATE TABLE report_contexts (
    report_id  TEXT NOT NULL REFERENCES reports (id) ON DELETE CASCADE,
    context_id TEXT NOT NULL REFERENCES contexts (id) ON DELETE CASCADE,
    PRIMARY KEY (report_id, context_id)
);

CREATE TABLE report_versions (
    id                  TEXT PRIMARY KEY,
    report_id           TEXT NOT NULL REFERENCES reports (id) ON DELETE CASCADE,
    version_number      INTEGER NOT NULL CHECK (version_number > 0),
    content_markdown    TEXT NOT NULL,
    source_snapshot     TEXT NOT NULL CHECK (json_valid(source_snapshot)),
    model               TEXT NOT NULL,
    input_tokens        INTEGER,
    output_tokens       INTEGER,
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (report_id, version_number)
);

CREATE INDEX report_versions_report_idx
    ON report_versions (report_id, version_number DESC);

-- +goose Down

DROP TABLE report_versions;
DROP TABLE report_contexts;
DROP TABLE reports;
