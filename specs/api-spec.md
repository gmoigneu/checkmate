# Checkmate — API Specification

**Status:** implemented and tested. Every statement here was derived from the Go
source, not from intent.
**Companion:** [`openapi.yaml`](openapi.yaml) is the machine-readable REST surface
— paths, schemas, enums, status codes. Generate clients from it.
**Audience:** anyone building a client (web, iOS, macOS, an MCP client, a script).

This document covers what a schema cannot state: the behavioural contracts, the
invariants worth enforcing before a request, and the two protocols (sync, MCP) that
are more than a set of endpoints. Where it and `openapi.yaml` disagree, one of them
is a bug — say so.

> **Maintenance:** `specs/` is a deliverable, not a snapshot. Any change to an
> endpoint, field, enum, invariant, cascade, scope or MCP tool updates this file and
> `openapi.yaml` in the same commit as the code.

---

## 1. Shape of the system

One Go binary, one sqlite file, the frontend to be embedded via `embed.FS`.
Single-user in practice, multi-user in the schema: every row carries a `user_id` and
every store method takes an owner and scopes its SQL by it.

```
Context ──┬── Project ──┐
          │             │
          └─────────────┴── Task ──┬── Task     (parent_id, subtasks, any depth)
                                   ├── Task     (blocked_by_id)
                                   └── Person   (delegated_to_id)
Recurrence ────────────────────────→ Task       (spawned occurrences)
Source (fixed lookup of 6)
```

Three surfaces over the same data and the same ownership rules:

| Surface | Path | For |
| --- | --- | --- |
| REST | `/v1/*` | Web, iOS, macOS |
| MCP | `/mcp` | Agents and assistants (§7) |
| OAuth | `/oauth/*`, `/.well-known/*` | Issuing credentials to MCP clients (§6) |

---

## 2. Conventions

**Ids** are UUIDv7 as text. Because v7 begins with a millisecond timestamp, `id`
ordering *is* creation ordering — which is why the default listing needs no sort
column, and why a client may safely mint an id locally if it ever needs to.

**Timestamps** are RFC3339 UTC text with milliseconds: `2026-07-25T14:03:11.482Z`.
**Calendar dates** are plain `YYYY-MM-DD` with no zone and no time. They are
validated as real dates, so `2026-02-31` is rejected rather than merely
shape-checked.

**Estimates** are whole minutes, greater than zero.

**Collections** return `{"data": [...], "next_cursor": "…"|null}`. **Single
resources** return the bare object. `DELETE` returns 204.

**Errors** are `{"error": "...", "fields": {"due_on": "..."}}`. When `fields` is
present, render each entry against its own control. A generic toast in that case
throws away the only useful information in the response.

---

## 3. The vocabulary

Enforced by database CHECK constraints. Do not invent a value.

### Task status — seven, exhaustive

| Value | Meaning | Notes |
| --- | --- | --- |
| `inbox` | Captured, not triaged | The default when no context is given |
| `todo` | Triaged, waiting to be worked | |
| `in_progress` | Being worked now | |
| `blocked` | Cannot proceed | `blocked_by_id` is **optional** — a task can be blocked by something outside Checkmate |
| `delegated` | The user is waiting on a person | **Requires `delegated_to_id`, enforced in both directions** (§4.1) |
| `done` | Finished | `completed_at` set by the server |
| `cancelled` | Abandoned | `cancelled_at` set by the server. **Not the same as `done`, and not the same as deleted** |

### Task kind — derived, never stored, never writable

Precedence is strict top to bottom, so a recurring task that is also delegated reads
as `recurring`:

| Kind | Condition |
| --- | --- |
| `recurring` | `recurrence_id` is set |
| `delegated` | `delegated_to_id` is set |
| `blocked` | `blocked_by_id` set **or** status is `blocked` |
| `long` | has at least one live child |
| `short` | none of the above |

Derived by a SQL view rather than stored, so it cannot go stale: adding a subtask
flips a task from `short` to `long` with no second write.

### Source vs capture method — two independent axes

**Source** is *where the task came from*: `self`, `email`, `slack`, `google_chat`,
`meeting`, `phone`. Six fixed rows, global, from `GET /v1/sources`.

**Capture method** is *how it entered Checkmate*: `form`, `api`, `hermes`,
`chrome_ext`, `ios_widget`, `voice`, `recurrence`. Set on create, then read-only.

They are independent: a task can originate in a Slack thread (`source: slack`) and
be captured by dictation (`capture_method: voice`). Capture method is diagnostic —
show it in a detail footer, never in a list row.

Per-client values to send: web forms and native capture sheets → `form`; iOS widget
and Control Center → `ios_widget`; dictation and Siri → `voice`; share extension →
`chrome_ext`. The MCP endpoint stamps `hermes` itself; the spawner stamps
`recurrence`.

### Others

**Project status** — `active`, `paused`, `done`, `archived`.
**Scopes** — `read`, `write`, plus `offline_access` at the authorization server only.

---

## 4. Invariants

Breaking one returns 422 with `fields`. A good client prevents the round trip.

### 4.1 Delegation needs a person, both ways

`status: delegated` requires `delegated_to_id`. You cannot set the status without a
person, **and you cannot clear the person while the status holds**. The second half
is the one clients forget.

- Choosing "waiting on" opens a person picker in the same gesture and cannot commit
  empty.
- Clearing the person offers "→ To do" rather than erroring.
- `POST /v1/tasks` accepts `delegated_to` — a person's **name** — resolving or
  creating them in one call. Create-only; on PATCH, use `delegated_to_id`.

### 4.2 A project lives in one context

A project belongs to exactly one context and has no children. A task's project must
be in the task's context.

- The project picker lists only projects of the current context.
- Changing a task's context invalidates its project: warn and clear in the same
  action, do not silently drop it.
- Moving a *project* to another context **moves all of its tasks with it**.

### 4.3 Graph edges reject cycles at any depth

`parent_id` and `blocked_by_id` are checked with a recursive walk, so `A → B → A` is
refused, not just `A → A`. Pickers should exclude the task's own subtree and blocker
chain; if the server still refuses, show it inline on the field.

### 4.4 Server-managed fields

`completed_at`, `cancelled_at`, `kind`, `rev`, `recurrence_id`, `occurrence_on` are
not writable — sending them is a **400**, not a silent no-op. Reopening a task
clears `completed_at` automatically; say so in the undo affordance.

A spawned occurrence cannot be detached from its series.

### 4.5 Not nullable

`sort_order` on a context and `lead_days` on a recurrence are `NOT NULL`. Sending
`null` is a 422 naming the field, not a 500.

### 4.6 Limits

Body 1 MiB, reachable only through `details`. Titles 500 characters. Page size 200.

---

## 5. Behavioural contracts

### 5.1 PATCH: absent ≠ null ≠ set

```
{}                        → changes nothing
{"due_on": null}          → clears the due date
{"due_on": "2026-08-01"}  → sets it
{"planed_on": "..."}      → 400, unknown field
```

Unknown fields are rejected rather than ignored, because on PATCH a typo and a
successful write are otherwise indistinguishable from the client's side.

**Design consequence:** "clear this field" and "leave this field alone" must be two
visibly different actions. Every optional field needs an explicit clear affordance —
an `×` on the chip, a "No date" row in the picker — distinct from cancelling out of
the editor.

### 5.2 Ordering and pagination

`GET /v1/tasks` takes `sort` and `order`:

| `sort` | Default direction | Notes |
| --- | --- | --- |
| `created_at` (default) | `desc` | Served by the primary key |
| `due_on`, `planned_on`, `completed_at` | `asc` | |
| `title` | `asc` | Case-insensitive, so `Apple` sorts with `apple` |
| `estimate_minutes` | `asc` | |
| `updated_at`, `status` | `asc` | |

**Missing values always sort last**, in both directions. "Sorted by due date"
opening with undated tasks would be useless whichever way the dates run.

Pagination is **keyset, not offset**, so it does not drift when rows change between
pages — with an offset, inserting a task mid-walk silently skips or repeats one.

`next_cursor` is **opaque; do not parse it**, and it is **bound to the sort that
issued it**. Changing `sort` or `order` mid-walk is a 422, because the same position
means nothing under a different ordering: restart the walk. The same cursor
mechanism is used by every other collection, keyed on id.

### 5.3 The inbox has two meanings, deliberately reconciled

"Inbox" means **awaiting triage**, which is `status = inbox`. It is *not* merely
"has no context": delegating an untriaged capture leaves it without a context while
it is plainly no longer waiting to be sorted.

- The brief's `inbox` bucket and the MCP `inbox_only` filter both use the status.
- `GET /v1/tasks?context_id=null` is the *literal* question "which tasks have no
  context", which is a different and occasionally useful one.

### 5.4 Delete cascades

Deletes are tombstones — `deleted_at` is set so the change reaches sync clients —
and each keeps the graph coherent. **There is no undelete.** Every confirmation
must state what happens to the children, because it is non-obvious in every case:

| Deleting | What happens |
| --- | --- |
| **Context** | Its projects and recurrences are deleted. **Its tasks move to the inbox** — they are *not* deleted. People lose the association. |
| **Project** | Deleted. Its tasks stay in their context and lose the grouping. |
| **Person** | Deleted. Tasks delegated to them **return to `todo`**, because the schema forbids a delegated task with no delegate. |
| **Task** | Deleted **with its whole subtree**. Anything blocked by it is unblocked and returned to `todo`. |
| **Recurrence** | Deleted and deactivated. **Occurrences already spawned are untouched** — they are real completion history. |

### 5.5 Error codes

| Code | Cause | Treatment |
| --- | --- | --- |
| 400 | Malformed JSON, unknown field, two documents | Client bug. Non-blocking error; never blame the user. |
| 401 | No credential, or unknown / revoked / expired / wrong audience | The credential is dead. Web: full-screen re-auth preserving the destination. Native: re-auth sheet, never discarding an in-flight capture. |
| 403 | Missing `read` or `write` scope, or a failed same-origin check | Read-only mode, distinct from offline. A scope failure carries `WWW-Authenticate: error="insufficient_scope"`. |
| 404 | No such row **or it belongs to someone else** | Deliberately indistinguishable, so the API never confirms an id it will not serve. Treat as "no longer exists"; a stale widget deep-link lands here. |
| 413 | Body over 1 MiB | Inline on `details`. |
| 415 | Content-Type not JSON | Client bug. |
| 422 | Validation failed | Map each `fields` key to its control. Never a generic toast when `fields` is populated. |

---

## 6. Authentication

Three credential kinds, all resolving to the same user.

| Kind | Prefix | Used by | Obtained |
| --- | --- | --- | --- |
| Session cookie | — | Web | Google OIDC (§6.1) |
| Device token | `cm_` | iOS, macOS, CLI, local agents | `POST /v1/tokens`, or the CLI |
| OAuth access token | `cmat_` | Remote MCP clients | §6.2 |

Scopes: `read` for every GET, `write` for every mutation. A session cookie carries
both.

### 6.1 Web: federated sign-in

`/auth/login/google` → Google → `/auth/callback/google` sets a session cookie.
`__Host-checkmate_session`, `HttpOnly`, `Secure`, `SameSite=Lax`; the unprefixed
name `checkmate_session` on a plain-http development server, because browsers
reject a `__Host-` cookie without `Secure`.

Two expiries: an idle timeout that slides forward on use (14 days) and an absolute
ceiling it cannot pass (90 days). An idle timeout alone never expires an actively
used session.

Cookie-authenticated **mutations** must prove same origin, via `Sec-Fetch-Site` or a
matching `Origin`. A request with neither is refused. Bearer tokens are exempt,
because they have to be attached deliberately.

**Provisioning is gated.** `CHECKMATE_ALLOWED_EMAILS` decides who may have an
account created by signing in, and **empty means nobody** — existing users can sign
in, strangers cannot create anything. Without this, a public deployment plus "sign
in with Google" hands an account to everyone on the internet who has one.

Four distinct sign-in refusals, each needing its own copy:

1. **Link expired or already used** — recoverable, retry.
2. **Address not on the allowlist** — *not* recoverable by retrying, and must not
   read as "wrong password". Explain that provisioning is gated by the server owner.
3. **Provider did not verify the email** — point at the provider.
4. **Generic failure**.

### 6.2 Native and agents: OAuth 2.1

Checkmate is a full authorization server (MCP spec revision 2025-11-25, with the
additions from the draft that becomes 2026-07-28). Google authenticates the human at
the consent step; the tokens issued are Checkmate's own and are never forwarded
upstream, because MCP forbids token passthrough.

```
/.well-known/oauth-protected-resource      RFC 9728  (also /{resource…})
/.well-known/oauth-authorization-server    RFC 8414
/oauth/authorize                           consent screen, PKCE S256 required
/oauth/token                               authorization_code, refresh_token
/oauth/revoke                              RFC 7009
/oauth/register                            RFC 7591, deprecated by the draft
```

What a client implementer needs to know:

- **PKCE S256 is mandatory.** An omitted `code_challenge_method` is an error, not
  RFC 7636's `plain` default, which OAuth 2.1 forbids.
- **Redirect URIs match exactly** — no prefixes, no wildcards. https, or loopback
  http for a native client, or a reversed-domain private-use scheme.
- **Tokens are audience-bound** (RFC 8707). Send `resource`; a token minted for
  another resource is refused.
- **`iss` is returned** on every authorization response including errors (RFC 9207).
  Validate it.
- **Refresh tokens rotate.** Replaying a rotated one **revokes the whole grant**, on
  the assumption that a replay means the token leaked and the attacker may already
  hold the successor. A client that retries a refresh badly will be logged out.
- Registration: Client ID Metadata Documents preferred, dynamic registration
  retained for current clients and capped.

`GET /v1/grants` lists connected clients; `DELETE /v1/grants/{id}` withdraws consent
and kills every token under it.

### 6.3 Issuing a device token needs a session

`POST /v1/tokens` **rejects bearer-token callers** with 403: minting a long-lived
credential from a long-lived credential would let a leaked token renew itself
indefinitely. So a native app has exactly two ways in — the OAuth flow above, or the
user pasting a token created in the web app or the CLI. The secret is shown once.

### 6.4 Self-hosted means the client does not know the address

The first screen of a native app must collect a server URL and validate it against
`/healthz` and `/auth/config`, failing legibly on: unreachable host, TLS error,
wrong service at that URL, and `providers: []` — no identity provider, so the web
sign-in cannot work and the flow should route straight to pasting a token.

---

## 7. The MCP endpoint

`POST /mcp`, Streamable HTTP, protocol revision **2025-11-25**. Responses are
`application/json` rather than SSE: nothing here streams, and the plain form is the
one a person can read with curl. Stateless — no `Mcp-Session-Id` to track.

Not in `openapi.yaml`, because it is JSON-RPC rather than REST.

### 7.1 Auth

The same store calls as a REST request, so a tool receives a user id the caller
cannot choose.

- OAuth access tokens are audience-checked against this resource.
- Device tokens are accepted: not audience-bound, but issued by the owner for this
  server and usable nowhere else, which satisfies the requirement's intent and makes
  a local client one line of config.
- **Session cookies are refused.** This is the one endpoint where a browser
  attaching a credential automatically would turn a foreign page into an
  authenticated caller.

Scope is enforced per tool. A write tool called with a read-only token answers **403
with `WWW-Authenticate: error="insufficient_scope"`** naming what is needed, which
is what drives the spec's step-up flow.

**Origin validation is an opt-in allowlist**, `CHECKMATE_MCP_ALLOWED_ORIGINS`. The
transport spec makes it a MUST, but that targets unauthenticated local servers where
a web page could drive one through the browser; here the only credential is a bearer
token no browser attaches by itself. Enforcing it strictly would break
browser-hosted clients that send their own origin. **This is the one place the
implementation knowingly reads a MUST as not applicable.**

### 7.2 Tools

Fifteen. Read tools need `read`, write tools need `write`.

| Tool | Scope | Notes |
| --- | --- | --- |
| `daily_brief` | read | The entry point for open-ended questions about the day |
| `list_tasks` | read | Filters incl. `inbox_only`; excludes done work unless asked |
| `get_task` | read | With subtasks |
| `list_contexts` | read | Context ids are per-user and cannot be guessed |
| `list_projects`, `list_people`, `list_recurrences` | read | |
| `create_task` | write | Title only is enough; no context captures to the inbox |
| `update_task` | write | `clear_*` flags null a date, since JSON cannot say "absent" in a flat schema |
| `complete_task` | write | Its own tool: finishing is the commonest single action |
| `delete_task` | write | Subtree, and unblocks dependents |
| `delegate_task` | write | Takes a **name**, creating the person if new |
| `triage_task` | write | Inbox capture → real task, moving the status too |
| `create_project`, `create_recurrence` | write | A new recurrence spawns immediately |

Ids are resolved to names in tool output (`context_name`, `delegated_to`), so a
model does not need a second call to understand what it is looking at.

**Deliberately absent:** creating or deleting contexts — they are the four stable
areas of a life and a model inventing a fifth is more likely a mistake than an
intention — and the sync feed, which is for device replication and would only flood
a model's context.

Validation failures come back as tool errors (`isError: true`) with a message naming
the field, because a model can read that and retry. Unknown tool names are JSON-RPC
protocol errors, because retrying will not help.

---

## 8. Sync

`GET /v1/sync?since=<rev>&limit=<n>`

**One-way.** Storage is sqlite with no offline write path: the server is
authoritative and there is nothing to merge.

Every mutable row carries `rev` from one global counter, stamped by a trigger. The
counter is global, so `rev` is unique across all tables and orders every change into
a single sequence. Deletes arrive as tombstones (`deleted_at` set) and **must be
applied as deletions**; a client that only ever saw live rows could never learn that
something was removed.

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

- Persist `cursor`; pass it as `since`. It never moves backwards.
- `has_more` means more changes are already waiting — poll again immediately rather
  than waiting for the next tick.
- `sources` appears only on a full sync (`since=0`), since it has no `rev`.
- Every collection is always an array, never `null`.
- `server_time` lets a client detect a badly skewed device clock and report it as a
  clock problem rather than as wrong due dates.

**Offline behaviour is read-only cache with blocked writes.** Last-synced data stays
readable; a persistent non-modal banner marks it stale with an age; **every** control
that would write is visibly disabled, including capture. Do not build a local draft
queue — it implies a flush that does not exist.

---

## 9. The daily brief

`GET /v1/brief?date=&timezone=&context_id=`

`date` defaults to today **in the caller's account timezone**, overridable per
request and echoed back. Dates are stored without a zone, so using the server's
clock would hand someone in Paris the wrong day for the first hours of it.

Eight buckets, each sorted server-side:

| Bucket | Contents | Sort |
| --- | --- | --- |
| `overdue` | `due_on < date`, open | `due_on` ASC — longest-late first |
| `due_today` | `due_on = date`, open | estimate ASC, **treating no estimate as 0, so unestimated tasks come first** |
| `planned` | `planned_on = date`, open | `due_on` ASC, undated last |
| `in_progress` | status `in_progress` | `updated_at` DESC |
| `inbox` | status `inbox` | `created_at` ASC — oldest first |
| `blocked` | status `blocked` | `updated_at` ASC |
| `waiting_on` | status `delegated`, **grouped by person** | `due_on` ASC within a person, undated first; groups in first-seen order |
| `completed_today` | done, completed on `date` | `completed_at` DESC |

"Open" means status in `todo`, `in_progress`, `blocked`, `delegated`.

Five behaviours a client must handle:

1. **Buckets overlap.** One task can be in `overdue`, `planned` and `blocked` at
   once. It must not read as three tasks.
2. **`inbox` ignores `context_id`**, because an untriaged task has no context and
   filtering would always empty it. When a context filter is active, label the count
   as global.
3. **Lists cap at 100; `totals` do not.** 143 overdue tasks means 100 rows and
   `totals.overdue = 143`. Design the "43 more" affordance.
4. **`planned_minutes` + `planned_without_estimate` are the over-commitment
   signal.** "2h10 planned, 3 tasks with no estimate" is the honest reading. Never
   present `planned_minutes` as complete while the other is non-zero.
5. **`due_on` and `planned_on` are different things.** Due is the deadline in the
   world; planned is the day the user intends to work on it. A task can be due
   Friday and planned Tuesday. Conflating them destroys the model.

---

## 10. Recurrence

A template holds an RFC 5545 `rrule`. A `FREQ` part is required, from `DAILY`,
`WEEKLY`, `MONTHLY`, `YEARLY`, `HOURLY`, `MINUTELY`, `SECONDLY`; the rest goes to a
full RRULE engine, so `INTERVAL`, `BYDAY`, `BYMONTHDAY`, `COUNT` and `UNTIL` work.

The spawner materializes each occurrence as a **real task row** carrying
`recurrence_id` and `occurrence_on`, with `due_on` set to the occurrence date. So
completion history survives — the Monday report was done thirty times — and every
list treats recurring and one-shot tasks identically.

It runs at boot, every 15 minutes, **inline when a template is created or edited**,
and from `checkmate recurrence spawn`. Idempotent, via a unique index on
`(recurrence_id, occurrence_on)`.

Properties the UI must reflect:

- **`lead_days`** — how many days *ahead of its due date* an occurrence appears.
  `lead_days: 2` on a Monday report means it shows up Saturday. The single most
  confusing field in the app; explain it with a live example.
- **Catch-up is bounded to 7 days.** Two weeks away produces the last week of missed
  occurrences, not fourteen. Older ones are counted as missed and never appear.
- **A series past `ends_on` or out of `COUNT` is deactivated, not deleted.** So
  `active: false` means paused *or* finished — the two are not distinguished, which
  is a known limitation.
- **`timezone` is per-series**, because `occurrence_on` is a plain date.
- `next_occurrence_on` and `last_spawned_on` are read-only server state.

---

## 11. Not available

No server support. A design needing one of these must be flagged, not drawn.

- **Priority, importance, flags** — no field.
- **Tags or labels** — contexts, projects and sources are the only classification.
- **A "someday" status** — the seven are exhaustive.
- **Attachments** — `reference_url` + `reference_label` is the only external pointer.
- **Comments, activity log, history** — `created_at` / `updated_at` only.
- **Notifications or push** — no infrastructure. An iOS badge must be computed
  on-device from synced data.
- **Saved filters server-side** — client-local only, so per-device and unsynced.
- **Sub-projects or nesting** — a project has one context and no children.
- **Persisted manual ordering of tasks** — only *contexts* have `sort_order`. Tasks
  have none, so drag-to-reorder is not persistable.
- **Time tracking** — only `estimate_minutes`.
- **Bulk edit endpoint** — no batch API. Bulk actions fan out to N PATCH calls;
  design the partial-failure state ("14 of 16 moved") rather than assuming
  atomicity.
- **A reverse "blocking" query** — the tasks blocked *by* a given task are not
  directly listable. Fetch and filter client-side, or add a filter server-side.
- **`contexts.color` is unvalidated free text** — no format is enforced. Agree one
  client-side and treat `null` as "use the fallback".
- **Undelete** — tombstones are visible with `include_deleted` but cannot be
  restored.

---

## 12. Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `CHECKMATE_ENV` | `development` | Picks log format; `production` requires https |
| `CHECKMATE_ADDR` | `:8080` | Listen address |
| `CHECKMATE_DB_PATH` | `checkmate.db` | sqlite file |
| `CHECKMATE_BASE_URL` | from `ADDR` | Public origin; OIDC redirects and the CSRF check derive from it |
| `CHECKMATE_AUTO_MIGRATE` | `true` | Migrate on boot |
| `CHECKMATE_SHUTDOWN_TIMEOUT` | `15s` | Drain time |
| `CHECKMATE_SECURE_COOKIES` | true outside dev | `Secure` on the session cookie |
| `CHECKMATE_SESSION_IDLE_TIMEOUT` | `336h` | Sliding expiry |
| `CHECKMATE_SESSION_MAX_LIFETIME` | `2160h` | Hard ceiling |
| `CHECKMATE_ALLOWED_EMAILS` | empty | Who may be provisioned. **Empty means nobody** |
| `CHECKMATE_GOOGLE_CLIENT_ID` / `_SECRET` | — | Enables Google sign-in |
| `CHECKMATE_DEFAULT_TIMEZONE` | `UTC` | For new accounts |
| `CHECKMATE_OAUTH_ENABLED` | `true` | The authorization server |
| `CHECKMATE_OAUTH_ALLOW_DCR` | `true` | RFC 7591 registration |
| `CHECKMATE_OAUTH_MAX_DYNAMIC_CLIENTS` | `200` | Registration cap |
| `CHECKMATE_MCP_ENABLED` | `true` | The `/mcp` endpoint |
| `CHECKMATE_MCP_ALLOWED_ORIGINS` | empty | Origin allowlist; empty accepts any |

---

## 13. Open questions

1. **`active: false` conflates paused and finished** on a recurrence. A UI wanting
   to distinguish "I paused this" from "this series ended" needs another column.
2. **No reverse blocking query** (§11). The task-detail "Blocking" list needs one, or
   accepts client-side filtering over a page.
3. **Project progress needs counting.** There is no aggregate endpoint, so
   `done/total` for a project means fetching its tasks. Over 200 tasks that is not
   answerable in one request.
4. **`contexts.color` is unvalidated.** Worth enforcing a format server-side once the
   palette is chosen.
5. **Cancelled vs deleted** are distinct in the data but there is no UI convention
   for cancelled yet.
