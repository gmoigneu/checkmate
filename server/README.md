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

## Not built yet

Resource handlers (`/v1/tasks`, `/v1/contexts`, `/v1/projects`, `/v1/people`,
`/v1/recurrences`, `/v1/sync`), bearer-token authentication middleware, the
recurrence spawner, the daily brief endpoint, and the MCP surface.
