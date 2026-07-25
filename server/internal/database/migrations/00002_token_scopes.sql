-- Normalize API token scopes to the read/write vocabulary the middleware uses.
--
-- The initial schema defaulted to "tasks:read tasks:write", but the API scopes
-- cover contexts, projects, people and recurrences too, so the "tasks:" prefix
-- was misleading about what a token could reach.

-- +goose Up

UPDATE api_tokens
SET scopes = trim(
        replace(replace(scopes, 'tasks:read', 'read'), 'tasks:write', 'write')
    )
WHERE scopes LIKE '%tasks:%';

-- sqlite cannot ALTER a column default, so the table is rebuilt. Legacy_alter_table
-- is off by default in modern sqlite, meaning references to api_tokens elsewhere
-- follow the rename; there are none, but the order below is safe regardless.
-- +goose StatementBegin
CREATE TABLE api_tokens_new (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    token_hash   TEXT NOT NULL,
    scopes       TEXT NOT NULL DEFAULT 'read write',
    last_used_at TEXT,
    expires_at   TEXT,
    revoked_at   TEXT,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

INSERT INTO api_tokens_new (id, user_id, name, token_hash, scopes, last_used_at, expires_at, revoked_at, created_at)
SELECT id, user_id, name, token_hash, scopes, last_used_at, expires_at, revoked_at, created_at
FROM api_tokens;

DROP TABLE api_tokens;

ALTER TABLE api_tokens_new RENAME TO api_tokens;

CREATE UNIQUE INDEX api_tokens_hash_key ON api_tokens (token_hash);
CREATE INDEX api_tokens_user_idx ON api_tokens (user_id);

-- +goose Down

UPDATE api_tokens
SET scopes = trim(
        replace(replace(scopes, 'write', 'tasks:write'), 'read', 'tasks:read')
    )
WHERE scopes NOT LIKE '%tasks:%';

-- +goose StatementBegin
CREATE TABLE api_tokens_old (
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
-- +goose StatementEnd

INSERT INTO api_tokens_old (id, user_id, name, token_hash, scopes, last_used_at, expires_at, revoked_at, created_at)
SELECT id, user_id, name, token_hash, scopes, last_used_at, expires_at, revoked_at, created_at
FROM api_tokens;

DROP TABLE api_tokens;

ALTER TABLE api_tokens_old RENAME TO api_tokens;

CREATE UNIQUE INDEX api_tokens_hash_key ON api_tokens (token_hash);
CREATE INDEX api_tokens_user_idx ON api_tokens (user_id);
