# Checkmate server

Go API for Checkmate, backed by sqlite. The Tanstack frontend will later be
embedded into this same binary via `embed.FS`, so a deployment is one file.

## Quick start

```sh
make build
./bin/checkmate user create -email you@example.com -name "Your Name" -timezone Europe/Paris -token "macOS"
make run
curl -s localhost:8080/healthz
```

`user create` seeds the four contexts from the brief (Upsun, Personal, Gaal,
Arkea) and, with `-token`, prints an API token once. Only the token's SHA-256 is
stored, so copy it there and then.

## Layout

```
cmd/checkmate            entrypoint: serve, migrate, user, token
internal/config          CHECKMATE_* environment configuration
internal/database        sqlite connection, embedded goose migrations
internal/database/migrations
internal/account         user / context seeding, API token issuing
internal/httpapi         router, middleware, handlers
internal/id              UUIDv7 generation
```

## Stack

| Choice | Why |
| --- | --- |
| `net/http` ServeMux | Method + pattern routing is in the stdlib since 1.22; no framework needed |
| `modernc.org/sqlite` | Pure Go, so the binary is CGO-free and cross-compiles to Linux trivially |
| `pressly/goose` | Plain SQL migrations, embedded in the binary via `embed.FS` |
| `log/slog` | Text logs in development, JSON in production |

## Data model

Four axes, kept deliberately separate:

- **context** — Upsun / Personal / Gaal / Arkea. The bucket a task belongs to.
- **source** — where the task came from: self, email, slack, google_chat,
  meeting, phone. A shared lookup, not per-context, because the same names
  repeat under every context.
- **capture_method** — how it entered Checkmate: form, api, hermes, chrome_ext,
  ios_widget, voice, recurrence. Independent of source: a task can originate in
  a Slack thread but be captured from the Chrome extension.
- **project** — optional grouping inside one context.

Tables: `users`, `api_tokens`, `sources`, `contexts`, `projects`, `people`,
`recurrences`, `tasks`.

### Task kind is derived, not stored

The four task types in the brief all fall out of columns that already exist, so
there is no `type` column to go stale when a subtask is added:

| Kind | Condition |
| --- | --- |
| `recurring` | `recurrence_id IS NOT NULL` |
| `delegated` | `delegated_to_id IS NOT NULL` |
| `blocked` | `blocked_by_id IS NOT NULL OR status = 'blocked'` |
| `long` | has at least one live child via `parent_id` |
| `short` | none of the above |

Read them off the `tasks_with_kind` view. Precedence is top to bottom: a
recurring task that is also delegated reads as recurring.

### Recurrence

`recurrences` holds the template with an RFC 5545 `rrule` (`FREQ=WEEKLY;BYDAY=MO`).
The spawner materializes each occurrence as a real `tasks` row carrying
`recurrence_id` and `occurrence_on`. Completion history therefore survives, and
every list query treats recurring and one-shot tasks identically. A unique index
on `(recurrence_id, occurrence_on)` makes spawning idempotent, so the scheduler
can run as often as it likes.

### Inbox

`tasks.context_id` is nullable, and `status` defaults to `inbox`. Quick capture
from a widget or a voice note should never be blocked on choosing a context;
triage assigns one later.

### Sync

Storage is sqlite with no offline mode, so the server is the single source of
truth and there is no merge problem. Clients still need cheap deltas:

- every mutable row carries `rev`, stamped from one global counter (`change_seq`)
  by an insert/update trigger pair
- deletes are tombstones (`deleted_at`), so they appear in the delta like any
  other change

A client polls for rows with `rev > last_seen_rev` and gets creates, updates and
deletes in one pass.

## Conventions

- ids are UUIDv7 as TEXT: time-sortable, and clients can mint them locally,
  which makes create idempotent
- timestamps are RFC3339 UTC text (`2026-07-25T14:03:11.482Z`)
- calendar dates (`due_on`, `planned_on`, `occurrence_on`) are plain
  `YYYY-MM-DD` with no timezone, guarded by a `GLOB` CHECK
- estimates are stored in whole minutes (`estimate_minutes`)

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `CHECKMATE_ENV` | `development` | `development` or `production`; picks log format |
| `CHECKMATE_ADDR` | `:8080` | Listen address |
| `CHECKMATE_DB_PATH` | `checkmate.db` | sqlite file; parent directory is created |
| `CHECKMATE_AUTO_MIGRATE` | `true` | Apply pending migrations on boot |
| `CHECKMATE_SHUTDOWN_TIMEOUT` | `15s` | Drain time for in-flight requests |
| `CHECKMATE_BASE_URL` | derived from `ADDR` | Public origin; OIDC redirect URIs and the CSRF origin check are built from it. Must be https in production |
| `CHECKMATE_SECURE_COOKIES` | true outside dev | `Secure` flag on the session cookie |
| `CHECKMATE_SESSION_IDLE_TIMEOUT` | `336h` (14d) | Sliding session expiry |
| `CHECKMATE_SESSION_MAX_LIFETIME` | `2160h` (90d) | Hard session ceiling |
| `CHECKMATE_ALLOWED_EMAILS` | empty | Who may be provisioned by sign-in. Empty means nobody |
| `CHECKMATE_GOOGLE_CLIENT_ID` | — | Enables Google sign-in when set together with the secret |
| `CHECKMATE_GOOGLE_CLIENT_SECRET` | — | |
| `CHECKMATE_DEFAULT_TIMEZONE` | `UTC` | Zone for newly provisioned accounts |

### Setting up Google sign-in

Register an OAuth client in the Google Cloud Console using the redirect URI that
is printed at boot — `{CHECKMATE_BASE_URL}/auth/callback/google` — then set the
client id and secret. Provider discovery runs at startup, so a wrong issuer fails
the boot rather than someone's first sign-in attempt.

## Authentication

Two credential kinds reach the API, and both resolve to the same `users` row, so
everything below the middleware is identical:

| Credential | Used by | Notes |
| --- | --- | --- |
| Bearer token | iOS, macOS, Linux, Chrome extension, hermes | Opaque, 256-bit, SHA-256 at rest, individually revocable, optional expiry |
| Session cookie | The web UI | `__Host-` prefixed, `HttpOnly`, `Secure`, `SameSite=Lax` |

Google authenticates the human; Checkmate issues its own session. Checkmate is
the authorization server for its own data, Google is only the identity backend.

```
GET  /auth/config              which providers are configured (public)
GET  /auth/login/{provider}    redirect to the provider
GET  /auth/callback/{provider} finish sign-in, set the cookie
POST /v1/logout[?everywhere=true]
GET  /v1/me
GET  /v1/tokens
POST /v1/tokens                session-only; returns the secret once
DELETE /v1/tokens/{id}
```

### Provisioning is gated by default

`CHECKMATE_ALLOWED_EMAILS` decides who may have an account created for them by a
federated sign-in. **Empty means nobody**: existing users can still sign in, but
no new account is created. Without that, a public deployment plus "sign in with
Google" hands an account to everyone on the internet who has one. Entries are
addresses, or `@domain.com` to admit a whole domain.

```sh
CHECKMATE_ALLOWED_EMAILS="you@example.com,@yourcompany.com"
```

`checkmate user create` remains the bootstrap path and ignores the allowlist.

### Session lifetime

Two expiries, on purpose. `expires_at` is an idle timeout that slides forward on
use; `absolute_expires_at` is a ceiling it can never slide past. An idle timeout
alone lets an actively used session live forever.

### Federated identities link on `sub`, not email

`oidc_identities` is keyed on `(provider, subject)`. An email address can be
renamed, and on some providers reassigned to a different person, so matching on
it alone would eventually hand one person somebody else's account. Email is used
only to attach a provider to a pre-existing account, and only when the provider
reports it verified.

### CSRF

Cookies are attached by the browser automatically, so a cookie-authenticated
mutation must prove it came from our own origin. `SameSite=Lax` blocks the attack
for unsafe methods already; the middleware adds a second lock, preferring
`Sec-Fetch-Site` (which a page cannot forge) and falling back to `Origin`. A
cookie-authenticated write carrying neither is refused rather than trusted.

Bearer tokens are exempt: they have to be attached deliberately, so a foreign
page cannot cause one to be sent.

### Issuing a token needs a session

`POST /v1/tokens` rejects bearer-token callers. Minting a long-lived credential
from a long-lived credential would let a leaked token renew itself indefinitely;
requiring a browser session means stealing one token does not grant the ability
to issue more. The CLI stays available for headless setup.

## API

All `/v1` routes need a bearer token or a session cookie. `/healthz` and the
`/auth/*` routes do not.

```
GET    /v1/sources
GET    /v1/{contexts,projects,people,recurrences,tasks}
POST   /v1/{contexts,projects,people,recurrences,tasks}
GET    /v1/{...}/{id}
PATCH  /v1/{...}/{id}
DELETE /v1/{...}/{id}
```

Collections return `{"data": [...], "next_cursor": "<id>|null"}`. Single
resources return the object. `DELETE` returns 204.

### Ownership

Every store method takes a user id as its first argument and scopes its SQL by
it, so there is no code path that reads or writes a row without naming the
owner. Handlers get that id from the auth middleware and cannot choose it.

Two consequences worth knowing:

- Another user's row is reported as **404, not 403**. The API never confirms
  that an id it will not serve actually exists.
- Ownership is checked on the ids *inside* a body too, not just the row being
  addressed. Attaching your task to someone else's `context_id`, `project_id`,
  `parent_id`, `blocked_by_id` or `delegated_to_id` is a 422 naming that field.

### Status codes

| Code | Meaning |
| --- | --- |
| 400 | Malformed JSON, unknown field, or more than one JSON document |
| 401 | Missing, unknown, revoked or expired token |
| 403 | Token lacks the `read` (GET) or `write` (mutation) scope |
| 404 | No such row, or it belongs to someone else |
| 413 | Body over 1 MiB |
| 415 | Content-Type is not JSON |
| 422 | Validation failed; see the `fields` object |

Errors are `{"error": "...", "fields": {"due_on": "must be a YYYY-MM-DD date"}}`.

### PATCH semantics

Absent, null and set are three different things. `{}` changes nothing,
`{"due_on": null}` clears the due date, `{"due_on": "2026-08-01"}` sets it. An
unknown field is a 400 rather than a silent no-op, because on PATCH a typo and a
successful write look identical from the client side.

`recurrence_id`, `occurrence_on`, `kind` and `rev` are not writable.

### Task filters

`GET /v1/tasks` accepts `status`, `kind` (both repeatable or comma-separated),
`context_id`, `project_id`, `parent_id`, `delegated_to_id`, `recurrence_id`,
`planned_on`, `planned_before`, `planned_after`, `due_on`, `due_before`,
`due_after`, `q` (title and details), `top_level`, `include_deleted`, `limit`
(max 200), `cursor`.

`?context_id=null` is the inbox. `?project_id=null` and `?parent_id=null` work
the same way. Listings are ordered newest first by id; `sort_order` on contexts
is a display hint for clients.

### Convenience

`POST /v1/tasks` accepts `delegated_to` with a person's name instead of
`delegated_to_id`, resolving or creating that person, so "delegate this to Marc"
is one request even the first time Marc is mentioned.

### Cascades

Deletes are tombstones so they reach sync clients, and each one leaves the
graph coherent rather than dangling:

| Deleting | Effect |
| --- | --- |
| Context | Its projects and recurrences are tombstoned; its **tasks move to the inbox** rather than being deleted |
| Project | Tombstoned; its tasks stay in their context and lose the grouping |
| Person | Tombstoned; tasks delegated to them return to `todo` (the schema forbids a delegated task with no delegate) |
| Task | Tombstoned with its whole subtree; anything blocked by it is unblocked and returned to `todo` |
| Recurrence | Tombstoned and deactivated; occurrences already spawned are left alone, since they are real history |

Moving a project to another context moves its tasks with it.

### Invariants enforced

- A project must live in the same context as the task referencing it
- `status: delegated` requires `delegated_to_id`, in both directions
- `completed_at` / `cancelled_at` are maintained by the server, and cleared when
  a task is reopened
- `parent_id` and `blocked_by_id` reject cycles at any depth, not just self-reference
- Dates are real calendar dates: `2026-02-31` is a 422, not just a shape check

## Migrations: a hazard worth knowing

sqlite cannot `ALTER` a column's default or nullability, so changing one means
rebuilding the table. **With foreign keys enabled, `DROP TABLE` performs an
implicit `DELETE` that fires `ON DELETE CASCADE` into every child table**, and
`PRAGMA foreign_keys` is a no-op inside a transaction, so a migration cannot turn
it off. Rebuilding a parent table like `users` would silently wipe every context,
project, person, recurrence and task — and the migration would still report
success.

`TestMigrationsPreserveExistingData` seeds data at schema version 1 and migrates
to head to catch exactly this. Rebuilding a *leaf* table (as migration 00002 does
to `api_tokens`) is safe, because nothing references it.

## Not built yet

`/v1/sync` (the `rev` cursor endpoint), the recurrence spawner, the daily brief,
and the OAuth 2.1 authorization-server endpoints that remote MCP clients need:
`/oauth/authorize`, `/oauth/token`, dynamic client registration, and
protected-resource metadata. Those sit on top of the sessions built here — the
authorize endpoint needs a signed-in human to consent.
