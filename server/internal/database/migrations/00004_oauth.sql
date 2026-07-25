-- OAuth 2.1 authorization server, for remote MCP clients.
--
-- Checkmate is both the authorization server and the resource server. Google
-- authenticates the human at the /oauth/authorize step; the tokens issued here
-- are Checkmate's own and are audience-bound to Checkmate, never passed through
-- to anything upstream (MCP forbids token passthrough).
--
-- Access tokens live in their own table rather than joining api_tokens: they are
-- short-lived, client-bound, audience-bound and refresh-rotated, which is a
-- different lifecycle from a device token that a human pastes into a config file.

-- +goose Up

-- ---------------------------------------------------------------------------
-- Clients
--
-- Three registration routes, per the MCP client-registration spec:
--   cimd          - client_id is an https URL serving a metadata document.
--                   Preferred by the spec; the row is a cache of that document.
--   dynamic       - RFC 7591 registration. Deprecated by the draft spec but
--                   still what current MCP clients use.
--   preregistered - created out of band by the CLI.
-- ---------------------------------------------------------------------------
CREATE TABLE oauth_clients (
    id                         TEXT PRIMARY KEY,
    kind                       TEXT NOT NULL
                               CHECK (kind IN ('cimd', 'dynamic', 'preregistered')),
    client_name                TEXT NOT NULL,
    client_uri                 TEXT,
    logo_uri                   TEXT,

    -- JSON arrays. Redirect URIs are matched exactly, never by prefix.
    redirect_uris              TEXT NOT NULL,
    grant_types                TEXT NOT NULL DEFAULT '["authorization_code","refresh_token"]',
    response_types             TEXT NOT NULL DEFAULT '["code"]',

    scope                      TEXT,

    -- OIDC application_type. Native clients are allowed loopback redirects;
    -- defaulting web clients to loopback is what the draft spec warns about.
    application_type           TEXT NOT NULL DEFAULT 'native'
                               CHECK (application_type IN ('native', 'web')),

    token_endpoint_auth_method TEXT NOT NULL DEFAULT 'none'
                               CHECK (token_endpoint_auth_method IN ('none', 'client_secret_basic', 'client_secret_post')),
    client_secret_hash         TEXT,

    software_id                TEXT,
    software_version           TEXT,

    -- CIMD only: when the document was fetched and when the cache entry goes
    -- stale, honouring the document's HTTP cache headers.
    metadata_fetched_at        TEXT,
    metadata_expires_at        TEXT,

    created_at                 TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at                 TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),

    -- A confidential client without a secret could never authenticate.
    CHECK (token_endpoint_auth_method = 'none' OR client_secret_hash IS NOT NULL)
);

CREATE INDEX oauth_clients_kind_idx ON oauth_clients (kind);

-- ---------------------------------------------------------------------------
-- Authorization requests awaiting consent
--
-- Held server-side rather than round-tripped through hidden form fields, so the
-- parameters the user consented to are exactly the ones the code is minted
-- from. A tampered form cannot widen the scope or move the redirect URI.
-- ---------------------------------------------------------------------------
CREATE TABLE oauth_authorize_requests (
    id                    TEXT PRIMARY KEY,
    client_id             TEXT NOT NULL REFERENCES oauth_clients (id) ON DELETE CASCADE,
    user_id               TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    redirect_uri          TEXT NOT NULL,
    scope                 TEXT NOT NULL,
    resource              TEXT NOT NULL,
    state                 TEXT,
    code_challenge        TEXT NOT NULL,
    code_challenge_method TEXT NOT NULL DEFAULT 'S256'
                          CHECK (code_challenge_method = 'S256'),
    expires_at            TEXT NOT NULL,
    created_at            TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX oauth_authorize_requests_expiry_idx ON oauth_authorize_requests (expires_at);

-- ---------------------------------------------------------------------------
-- Grants: the consent record. One row per (user, client, audience).
--
-- Access and refresh tokens hang off a grant, so revoking consent kills every
-- token issued under it in one statement.
-- ---------------------------------------------------------------------------
CREATE TABLE oauth_grants (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    client_id  TEXT NOT NULL REFERENCES oauth_clients (id) ON DELETE CASCADE,
    scope      TEXT NOT NULL,
    audience   TEXT NOT NULL,
    revoked_at TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE UNIQUE INDEX oauth_grants_key ON oauth_grants (user_id, client_id, audience)
    WHERE revoked_at IS NULL;
CREATE INDEX oauth_grants_user_idx ON oauth_grants (user_id);

-- ---------------------------------------------------------------------------
-- Authorization codes
--
-- Stored by hash, single-use, and short-lived. The PKCE challenge is captured
-- here so the token request has to prove it came from the same client that
-- started the flow.
-- ---------------------------------------------------------------------------
CREATE TABLE oauth_authorization_codes (
    code_hash             TEXT PRIMARY KEY,
    grant_id              TEXT NOT NULL REFERENCES oauth_grants (id) ON DELETE CASCADE,
    client_id             TEXT NOT NULL REFERENCES oauth_clients (id) ON DELETE CASCADE,
    user_id               TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    redirect_uri          TEXT NOT NULL,
    scope                 TEXT NOT NULL,
    resource              TEXT NOT NULL,
    code_challenge        TEXT NOT NULL,
    code_challenge_method TEXT NOT NULL,
    expires_at            TEXT NOT NULL,
    created_at            TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX oauth_codes_expiry_idx ON oauth_authorization_codes (expires_at);

-- ---------------------------------------------------------------------------
-- Access tokens
--
-- Opaque and stored hashed, so validation is one indexed read and revocation is
-- immediate. A JWT would let the resource server validate without the database,
-- which buys nothing here and costs instant revocation plus key rotation.
--
-- audience is the RFC 8707 resource the token was issued for. The resource
-- server MUST reject a token whose audience is not itself.
-- ---------------------------------------------------------------------------
CREATE TABLE oauth_access_tokens (
    id         TEXT PRIMARY KEY,
    token_hash TEXT NOT NULL,
    grant_id   TEXT NOT NULL REFERENCES oauth_grants (id) ON DELETE CASCADE,
    client_id  TEXT NOT NULL REFERENCES oauth_clients (id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    scope      TEXT NOT NULL,
    audience   TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    revoked_at TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE UNIQUE INDEX oauth_access_tokens_hash_key ON oauth_access_tokens (token_hash);
CREATE INDEX oauth_access_tokens_grant_idx ON oauth_access_tokens (grant_id);
CREATE INDEX oauth_access_tokens_expiry_idx ON oauth_access_tokens (expires_at);

-- ---------------------------------------------------------------------------
-- Refresh tokens
--
-- Rotated on every use, which OAuth 2.1 requires for public clients. used_at
-- and replaced_by make replay detectable: a second exchange of an already-used
-- token means the token leaked, so the whole grant is revoked rather than just
-- refusing that one request.
-- ---------------------------------------------------------------------------
CREATE TABLE oauth_refresh_tokens (
    id          TEXT PRIMARY KEY,
    token_hash  TEXT NOT NULL,
    grant_id    TEXT NOT NULL REFERENCES oauth_grants (id) ON DELETE CASCADE,
    client_id   TEXT NOT NULL REFERENCES oauth_clients (id) ON DELETE CASCADE,
    user_id     TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    scope       TEXT NOT NULL,
    audience    TEXT NOT NULL,
    expires_at  TEXT NOT NULL,
    used_at     TEXT,
    replaced_by TEXT,
    revoked_at  TEXT,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE UNIQUE INDEX oauth_refresh_tokens_hash_key ON oauth_refresh_tokens (token_hash);
CREATE INDEX oauth_refresh_tokens_grant_idx ON oauth_refresh_tokens (grant_id);
CREATE INDEX oauth_refresh_tokens_expiry_idx ON oauth_refresh_tokens (expires_at);

-- +goose Down

DROP TABLE oauth_refresh_tokens;
DROP TABLE oauth_access_tokens;
DROP TABLE oauth_authorization_codes;
DROP TABLE oauth_grants;
DROP TABLE oauth_authorize_requests;
DROP TABLE oauth_clients;
