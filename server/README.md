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
internal/model           domain types shared by store and HTTP
internal/store           all SQL; every method takes a user id and scopes by it
internal/account         user / context seeding, API token issuing
internal/login           federated sign-in (OIDC client)
internal/oauth           OAuth 2.1 authorization server, CIMD fetching
internal/recurrence      RRULE evaluation and the occurrence spawner
internal/mcpserver       the MCP endpoint: tool surface and auth bridge
internal/httpapi         router, middleware, handlers, consent screen
internal/patch           absent / null / set JSON field for PATCH
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

`recurrences` holds the template with an RFC 5545 `rrule` (`FREQ=WEEKLY;BYDAY=MO`),
evaluated by `teambition/rrule-go` rather than a hand-rolled subset. The spawner
materializes each occurrence as a real `tasks` row carrying `recurrence_id` and
`occurrence_on`, with `due_on` set to the occurrence date so it lands in the daily
brief. Completion history therefore survives — you can see the Monday report was
done thirty times — and every list query treats recurring and one-shot tasks
identically.

It runs at boot, every 15 minutes, inline when a template is created or edited
(otherwise a new daily recurrence shows nothing for a quarter of an hour), and on
demand:

```sh
checkmate recurrence spawn
```

Four properties worth knowing:

- **Idempotent.** A unique index on `(recurrence_id, occurrence_on)` means an
  extra pass creates nothing, so the ticker, the boot run, the inline call and a
  cron job can all overlap safely.
- **Bounded catch-up.** `CatchUpDays` is 7. Coming back from two weeks away gives
  you the last week of missed occurrences, not fourteen stale standups — and not
  nothing, which would silently lose a weekly report that came due while the
  server was down. Occurrences older than the window are counted as `missed`.
- **Anchored at `starts_on`.** `DTSTART` comes from the template, so a weekly rule
  keeps its weekday instead of drifting to whenever the spawner first ran.
- **Timezone-correct.** Occurrence dates are computed in the template's own zone,
  because `occurrence_on` is a plain date and the same instant is a different day
  either side of the date line.

A series that reaches `ends_on`, or exhausts a `COUNT=`, is deactivated rather
than deleted: the tasks it already spawned are real history and a deleted template
would orphan them. One unparseable rule is logged and skipped without stopping the
other templates.

A retired series is also stamped with `completed_at`, which is what separates
**finished** from **paused**. `active` alone conflated them, and they mean opposite
things to a person: one offers to resume, the other is over. The API exposes a derived
`state` of `active` / `paused` / `finished` and a `?state=` filter. Resuming a
finished series only does something if the rule changes too — the spawner runs inline
on update, so a spent rule is retired again on the same request.

The spawner is the one component that steps outside the per-user store convention
— it walks every account — so each row it writes takes its `user_id` from the
template. There is a test for exactly that.

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

`GET /v1/sync?since=<rev>&limit=<n>` returns creates, updates and deletes in one
pass:

```json
{
  "cursor": 412,
  "has_more": false,
  "changes": { "contexts": [], "projects": [], "people": [],
               "recurrences": [], "tasks": [] },
  "sources": [],
  "server_time": "2026-07-25T10:17:49.090Z"
}
```

Persist `cursor`, replay the rows onto the local copy, and treat any row with
`deleted_at` as a deletion. `sources` is sent only on a full sync (`since=0`),
since it has no `rev`. Every collection is always an array, never `null`.

**The cursor arithmetic is the subtle part.** `rev` comes from one global counter,
so it is unique across all tables and orders every change into a single sequence.
Each table is queried for `limit+1` rows to detect truncation; when any table is
truncated the returned cursor is pulled back to the lowest safe point across all
of them and every table is filtered to it. Without that, a table with many
changes would have its tail skipped — the client would advance past rows it never
received and never ask again. `TestSyncPaginationLosesNothing` pins this.

### Daily brief

`GET /v1/brief?date=&timezone=&context_id=` assembles the morning read: overdue
(oldest first), due today, planned today, in progress, inbox, blocked, and
`waiting_on` grouped by person rather than by task, since that is how a follow-up
actually gets made. `completed_today` is there so the day shows progress and not
only debt.

`totals` includes `planned_minutes` and `planned_without_estimate`, so an
over-committed day is visible before it starts and the estimate is not read as
more complete than it is. Lists are capped at 100 but the totals are not.

`date` defaults to today **in the caller's timezone**, taken from their account
and overridable per request. Dates are stored without a zone, so using the
server's clock would hand someone in Paris the wrong day for the first hours of
it.

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
| `CHECKMATE_OAUTH_ENABLED` | `true` | The OAuth 2.1 authorization server for MCP clients |
| `CHECKMATE_OAUTH_ALLOW_DCR` | `true` | RFC 7591 dynamic registration (deprecated by the MCP draft) |
| `CHECKMATE_OAUTH_MAX_DYNAMIC_CLIENTS` | `200` | Cap on open registration |
| `CHECKMATE_MCP_ENABLED` | `true` | Serve the MCP endpoint at `/mcp` |
| `CHECKMATE_MCP_ALLOWED_ORIGINS` | empty | Restrict the `Origin` header on `/mcp`; empty accepts any |

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

## OAuth 2.1 for remote MCP clients

Checkmate is both the OAuth **authorization server** and the **resource server**.
Google authenticates the human at the consent step; the tokens issued are
Checkmate's own and are never forwarded upstream — MCP forbids token passthrough,
which is what turns a resource server into a confused deputy.

Built against the MCP authorization spec, stable revision **2025-11-25**, with the
additions from the draft that becomes 2026-07-28.

```
GET  /.well-known/oauth-authorization-server        RFC 8414
GET  /.well-known/oauth-protected-resource          RFC 9728 (also /{resource...})
GET  /oauth/authorize                               consent screen
POST /oauth/authorize                               the decision
POST /oauth/token                                   authorization_code, refresh_token
POST /oauth/revoke                                  RFC 7009
POST /oauth/register                                RFC 7591 (deprecated by the draft)
GET  /v1/grants, DELETE /v1/grants/{id}             review and withdraw consent
```

### What the spec forced

| Requirement | Why it matters here |
| --- | --- |
| `code_challenge_methods_supported` advertised | Clients are told to **refuse** a server that omits it, so leaving it out makes the server unusable |
| PKCE S256 mandatory | `plain` is refused, and an absent `code_challenge_method` is an error rather than RFC 7636's default of `plain`, which OAuth 2.1 forbids |
| RFC 8707 audience binding | Tokens carry an audience and the resource server rejects one minted for anything else |
| RFC 9207 `iss` | Emitted on every authorization response including errors, and advertised. The draft is expected to raise this to a MUST |
| Refresh rotation | Required for public clients. Replaying a rotated token revokes the **whole grant**, because a replay means it leaked and the attacker may hold the successor |
| Exact redirect URI matching | No prefixes, no wildcards |

Scopes are `read` and `write`, the same vocabulary as device tokens, so one
enforcement path covers both. `offline_access` is advertised by the authorization
server but deliberately absent from the resource's `scopes_supported`.

### Client registration

Both mechanisms, because the ecosystem is mid-migration:

- **Client ID Metadata Documents** — `client_id` is an https URL serving a JSON
  document. Preferred by the draft spec. The cached row *is* that document.
- **Dynamic registration** (RFC 7591) — deprecated by the draft, still what
  current MCP clients use. Capped by `CHECKMATE_OAUTH_MAX_DYNAMIC_CLIENTS`.

CIMD means **fetching a URL a stranger supplied**, which is an SSRF primitive by
construction. The fetcher allowlists globally-routable addresses rather than
blocklisting bad ones, and checks in `DialContext` against the address actually
dialled — not the hostname — which closes the DNS-rebinding window between a
check and a later connect. It also refuses redirects, requires https, and caps
body size and time.

### The consent screen is server-rendered

Deliberately not handed to the frontend. It is a security surface: the only place
a user can tell a real client from one impersonating it, and it has to work before
any frontend loads. It shows the redirect host prominently and warns on
loopback-only clients, because a metadata document proves who *published* it, not
who is listening on the port. `client_name` comes from a stranger, so it goes
through `html/template` escaping.

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

GET    /v1/me · PATCH /v1/me          name and timezone; email is not editable
POST   /v1/tasks/{id}/restore         undo a delete, subtree included
POST   /v1/people/{id}/merge          fold a duplicate delegate into another
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
(max 200), `cursor`, plus `sort` and `order`.

`?context_id=null` is the inbox. `?project_id=null` and `?parent_id=null` work
the same way.

### Sorting

`sort` accepts `created_at` (default), `updated_at`, `due_on`, `planned_on`,
`title`, `estimate_minutes`, `completed_at` and `status`; `order` is `asc` or
`desc`. The direction defaults to `desc` for `created_at` — newest first — and
`asc` for everything else, so the soonest date leads.

Rows with no value for the sort column always come last, in both directions:
"sorted by due date" opening with undated tasks would be useless either way.

Pagination is keyset rather than offset, so it does not drift when rows change
between pages — an offset would silently skip or repeat a task. `next_cursor` is
opaque and **bound to the sort that issued it**: reusing one under a different
`sort` or `order` is a 422, because the same position means nothing under a
different ordering. `TestSortedPaginationLosesNothing` walks every sort at a page
size of two and asserts the paged sequence matches a single unpaginated read.

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
| Task | Tombstoned with its whole subtree; anything blocked by it is unblocked and returned to `todo`. **Restorable** |
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

## The MCP endpoint

`POST /mcp` speaks the Model Context Protocol over Streamable HTTP, protocol
revision **2025-11-25**. The protocol layer is the official
[Go SDK](https://github.com/modelcontextprotocol/go-sdk); what is hand-written is
the tool surface and how it maps onto the ownership and scope model the rest of
the server already enforces.

```sh
curl -s localhost:8080/mcp \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | jq
```

Responses are `application/json` rather than an SSE stream. Clients must support
both and nothing here streams — every tool is a handful of sqlite queries, so
there are no progress notifications to interleave. The plain form is also the one
a person can read with curl, which matters for a server you operate alone.

The session is stateless: each request carries its own bearer token and the server
holds no per-session state, so clients need not track an `Mcp-Session-Id`.

### Tools

| Tool | Scope | Notes |
| --- | --- | --- |
| `daily_brief` | read | The morning overview; the entry point for open-ended questions |
| `list_tasks` | read | Filters for status, kind, context, project, dates, free text |
| `get_task` | read | One task plus its subtasks |
| `list_contexts` | read | Context ids are per-user and cannot be guessed |
| `list_projects` / `list_people` / `list_recurrences` | read | |
| `create_task` | write | Title is the only requirement; no context means capture to the inbox |
| `update_task` | write | Omitted fields are left alone; `clear_*` flags null a date |
| `complete_task` | write | Its own tool because finishing is the commonest single action |
| `delete_task` | write | Tombstones the subtree and unblocks dependents |
| `delegate_task` | write | Takes a person's *name*, creating them if new |
| `triage_task` | write | Turns an inbox capture into a real task |
| `create_project` / `create_recurrence` | write | A new recurrence spawns its occurrences immediately |

Deliberately **not** exposed: creating or deleting contexts (four stable areas of
your life; a model inventing a fifth is far more likely a mistake than an
intention) and the sync feed (that is for device replication and would only flood
a model's context).

Every task an MCP client creates is stamped `capture_method: hermes`, so you can
always tell which of your tasks an assistant put there.

### Auth on the MCP endpoint

Authentication runs through the SDK's bearer middleware with a verifier that calls
the same store functions as a REST request, so a tool receives a user id the
caller could not choose.

- **OAuth access tokens** are audience-checked against this resource, which MCP
  requires of a resource server.
- **Device tokens** are also accepted. They are not audience-bound, but they are
  issued by the account owner for this server and cannot be presented anywhere
  else, which satisfies the requirement's intent — and it makes pointing a local
  client at Checkmate a one-line config.
- **Session cookies are refused.** Browsers attach cookies automatically, and this
  is the one endpoint where that would turn a cross-site page into an
  authenticated caller.

Scope is enforced per tool. A write tool called with a read-only token answers
**403 with `WWW-Authenticate: error="insufficient_scope"`** naming the scope
needed, which is what drives the spec's step-up flow — the client can obtain a
wider token and retry instead of treating it as a dead end. Doing that means
peeking at the JSON-RPC body before the SDK reads it; the alternative, a tool-level
error, would tell the model it failed but not tell the client how to fix it.

### Origin validation: a deliberate deviation

The transport spec says a server **MUST** validate `Origin`. That requirement
targets local servers with no authentication, where a web page could drive one
through the user's browser. It does not apply here: the only accepted credential
is a bearer token, which no browser attaches by itself, and cookies are refused.
Rejecting a foreign `Origin` outright would break legitimate browser-hosted MCP
clients that send their own.

So the check is an opt-in allowlist — `CHECKMATE_MCP_ALLOWED_ORIGINS` empty means
any origin, set makes it strict. When it does reject, it answers 403 with an
id-less JSON-RPC error as the spec prescribes. **This is the one place the
implementation knowingly reads a MUST as not applicable**; set the allowlist if
you disagree.

## Not built yet

- **Resources and prompts.** Only the `tools` capability is declared. Prompts
  ("run my weekly review") would be a natural next addition.
- **Server-initiated messages.** `GET /mcp` for a server→client SSE stream is not
  offered, because nothing here has anything to push.

- **Apple sign-in.** The OIDC layer is provider-agnostic; only Google is wired.
- **The Tanstack frontend**, to be embedded via `embed.FS`.

## A sqlite footgun that bit twice

`ON CONFLICT` against a **partial** unique index must repeat the index predicate
in the conflict target:

```sql
ON CONFLICT (recurrence_id, occurrence_on)
  WHERE recurrence_id IS NOT NULL AND occurrence_on IS NOT NULL
DO NOTHING
```

Omitting the `WHERE` does not degrade to a plain insert — it matches no constraint
at all and the statement fails outright. Several indexes here are partial (to make
soft deletes free a slug, or to exclude nulls), so any new upsert against one needs
the predicate copied across. This is what made the first spawner run create zero
tasks while reporting every template as failed.
