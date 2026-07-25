-- Browser sessions and federated (OIDC) identities.
--
-- Two credential kinds now reach the API: opaque bearer tokens for native and
-- machine clients, and session cookies for the web UI. Both resolve to the same
-- users row, so the ownership model underneath is unchanged.

-- +goose Up

-- ---------------------------------------------------------------------------
-- Sessions
--
-- Stored by hash like api_tokens, so a database copy yields nothing replayable.
--
-- Two expiries on purpose: expires_at is an idle timeout that slides forward as
-- the session is used, and absolute_expires_at is a hard ceiling that it cannot
-- slide past. An idle timeout alone lets an active session live forever.
-- ---------------------------------------------------------------------------
CREATE TABLE sessions (
    id                  TEXT PRIMARY KEY,
    user_id             TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash          TEXT NOT NULL,
    expires_at          TEXT NOT NULL,
    absolute_expires_at TEXT NOT NULL,
    last_seen_at        TEXT,
    user_agent          TEXT,
    ip                  TEXT,
    revoked_at          TEXT,
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE UNIQUE INDEX sessions_token_key ON sessions (token_hash);
CREATE INDEX sessions_user_idx ON sessions (user_id);

-- Supports the periodic sweep of dead sessions.
CREATE INDEX sessions_expiry_idx ON sessions (expires_at);

-- ---------------------------------------------------------------------------
-- Federated identities
--
-- Linked on (provider, subject) rather than on email. The subject claim is the
-- provider's stable identifier for a person; an email address can be renamed or,
-- on some providers, reassigned to somebody else, so treating it as the join key
-- would eventually hand one person another's account.
-- ---------------------------------------------------------------------------
CREATE TABLE oidc_identities (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    provider   TEXT NOT NULL,
    subject    TEXT NOT NULL,
    email      TEXT COLLATE NOCASE,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE UNIQUE INDEX oidc_identities_subject_key ON oidc_identities (provider, subject);
CREATE INDEX oidc_identities_user_idx ON oidc_identities (user_id);

-- ---------------------------------------------------------------------------
-- In-flight login attempts
--
-- state, nonce and the PKCE verifier are held server-side for the duration of
-- the redirect to the provider. Server-side rather than in a cookie so a single
-- row can enforce single use: the flow is deleted when consumed, which is what
-- makes an intercepted callback URL unreplayable.
-- ---------------------------------------------------------------------------
CREATE TABLE oidc_flows (
    state         TEXT PRIMARY KEY,
    nonce         TEXT NOT NULL,
    code_verifier TEXT NOT NULL,
    provider      TEXT NOT NULL,
    redirect_to   TEXT,
    expires_at    TEXT NOT NULL,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX oidc_flows_expiry_idx ON oidc_flows (expires_at);

-- Note on what this migration deliberately does NOT do: users.name stays
-- NOT NULL. Relaxing it would mean rebuilding the users table, and with foreign
-- keys enabled DROP TABLE performs an implicit DELETE that cascades into every
-- child table -- contexts, projects, people, recurrences, tasks. PRAGMA
-- foreign_keys is a no-op inside a transaction, so a migration cannot turn the
-- cascade off. A provider that releases no name gets the email's local part
-- instead, which is not worth risking every row of user data over.

-- +goose Down

DROP TABLE oidc_flows;
DROP TABLE oidc_identities;
DROP TABLE sessions;
