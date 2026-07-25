# Checkmate — Design Specification

**For:** Claude Design
**From:** Checkmate product owner
**Date:** 2026-07-25
**Backend status:** Go + sqlite API is built, tested and stable. No frontend exists yet. Everything in this document must be buildable against the API described in §3.

---

## 0. How to use this document

1. Read §1–§4 first. §3 is the hard part: it lists the things the backend can and cannot do. **A design that needs something in §3.9 ("Not available") cannot ship.** If a screen you want requires it, say so in your response rather than designing around it silently.
2. §4 asks you to propose the visual direction. That is genuinely open — do not treat the ASCII wireframes in §7–§10 as visual comps. They encode *information hierarchy and element presence only*. Position, weight, colour, radius, spacing and shape are yours.
3. §5 (tokens/components), §7–§10 (screens), §11 (motion), §12 (copy) are the deliverable surface.
4. §14 lists open engineering questions. Flag any others you hit.

Screen IDs are stable references (`W3`, `I7`, …). Use them in filenames and comments.

---

## 1. What Checkmate is

A **single-user** personal task manager for someone who runs four parallel lives at once and keeps losing tasks in the gaps between them. It is self-hosted: one Go binary, one sqlite file, the web frontend embedded in the binary.

The four lives are **contexts**: Upsun (employer), Personal, Gaal (side company), Arkea (client). Tasks arrive from Slack, email, meetings, phone calls, and from the user's own head, in all four contexts, all day. The app's job is:

1. **Capture in under three seconds**, from anywhere, without forcing a decision about which context it belongs to.
2. **Triage later**, deliberately, in one place.
3. **Every morning, answer one question**: what does today actually look like — including what I am waiting on someone else for, and whether I have over-committed the day.

Not a team tool. No sharing, no assignees-who-log-in, no comments, no notifications from other humans. "Delegated to Marc" means *the user is waiting on Marc*; Marc has no account and never sees Checkmate.

**The emotional target:** opening Checkmate should feel like reading a well-set page, not like opening a ticket tracker. Four contexts of chaos should arrive as one calm surface. The user chose **"calm native — Things 3 / Apple Reminders"** as the tonal north star: generous whitespace, soft per-context colour, gesture-forward, reads as a personal app rather than a work tool. That is a constraint on *tone*, not on your specific visual language.

---

## 2. Platforms

### In scope

| ID | Platform | Notes |
|---|---|---|
| `W` | **Web, desktop browser** | React + TanStack, served from the Go binary at the same origin as the API. ≥1024px. Primary "do real work" surface. |
| `MW` | **Web, mobile browser** | Same SPA, responsive. ≤767px. Must be genuinely usable, not a courtesy. |
| `I` | **iPhone, native** | SwiftUI. iOS 18+. Plus widgets, Lock Screen, Control Center, App Intents/Siri, share extension. |
| `M` | **macOS, native** | SwiftUI. macOS 15+. Full three-pane window app. |
| `S` | **Server-rendered pages** | The OAuth consent screen and OAuth error page. Already implemented in Go `html/template`; you are restyling them. |

### Explicitly out of scope

iPad (no split-view layouts), Apple Watch, Chrome extension, Android, tvOS, a marketing site, email templates.

### Deliberate non-goals for macOS

The user chose **full window app only**. No menu bar extra, no persistent status item, no popover. See `M6` for the one exception being proposed (a global-hotkey capture panel), which is flagged for confirmation.

---

## 3. Backend physics — the non-negotiable part

Everything below is *implemented and shipped*. Design against it exactly.

### 3.1 Entities

```
Context   ──┬── Project ──┐
            │             │
            └─────────────┴── Task ──┬── Task (parent_id, subtasks, any depth)
                                     ├── Task (blocked_by_id)
                                     └── Person (delegated_to_id)
Recurrence ──────────────────────────→ Task (spawned occurrences)
Source (fixed lookup of 6)
```

**Context** — `id, name, slug, color?, sort_order, archived_at?, created_at, updated_at, rev`
**Project** — `id, context_id (required), name, description?, status, …`
**Person** — `id, name, email?, context_id?, notes?, …`
**Recurrence** — `id, context_id (required), project_id?, source?, title, details?, rrule, timezone, estimate_minutes?, delegated_to_id?, lead_days, starts_on, ends_on?, next_occurrence_on?, last_spawned_on?, active, …`
**Task** — `id, context_id?, project_id?, parent_id?, recurrence_id?, occurrence_on?, source?, capture_method, title, details?, status, priority?, due_on?, planned_on?, estimate_minutes?, delegated_to_id?, blocked_by_id?, reference_url?, reference_label?, kind (derived), completed_at?, cancelled_at?, …`

`?` = nullable. Every entity also carries `deleted_at?` (tombstone) and `rev` (sync counter).

### 3.2 The exact vocabulary

These are enforced by database CHECK constraints. **Do not invent a value, a status, or a field.**

**Task status** (7, exhaustive):

| Value | User-facing label | Meaning |
|---|---|---|
| `inbox` | *(implied by location)* | Captured, not yet triaged. The default for every new task. |
| `todo` | To do | Triaged, waiting to be worked. |
| `in_progress` | In progress | Actively being worked right now. |
| `blocked` | Blocked | Cannot proceed. `blocked_by_id` is **optional** — a task can be blocked by something outside Checkmate. |
| `delegated` | Waiting on *{name}* | **Requires `delegated_to_id`.** Enforced both ways: you cannot set this status without a person, and you cannot clear the person while the status holds. |
| `done` | Done | `completed_at` set by the server. |
| `cancelled` | Cancelled | `cancelled_at` set by the server. Not the same as deleted. |

**Task priority** (optional): `urgent` · `high` · `medium` · `low`. It is an
importance rank, separate from status, deadline, and planned date. Null means the
task has not been prioritized.

**Task kind** — derived by the server, never writable, never stored. Precedence is strict top-to-bottom; a recurring task that is also delegated reads as `recurring`:

| Value | Condition | Suggested badge |
|---|---|---|
| `recurring` | `recurrence_id` is set | Repeats {human rule} |
| `delegated` | `delegated_to_id` is set | → {name} |
| `blocked` | `blocked_by_id` set **or** `status = blocked` | Blocked |
| `long` | has ≥1 live child task | {done}/{total} subtasks |
| `short` | none of the above | *(no badge)* |

**Source** — where the task came from. Fixed lookup, 6 rows, from `GET /v1/sources`:
`self` Self · `email` Email · `slack` Slack · `google_chat` Google Chat · `meeting` Meeting · `phone` Phone

**Capture method** — how it entered Checkmate. Independent axis from source (a task can originate in a Slack thread but be captured by voice). Set by the client on create, then read-only:
`form` · `api` · `hermes` · `chrome_ext` · `ios_widget` · `voice` · `recurrence`

Per-client value to send: web forms → `form`; iOS/macOS capture sheet → `form`; iOS widget & Control Center → `ios_widget`; dictation/Siri → `voice`; share extension → `chrome_ext`. This is diagnostic metadata — surface it only in a task's detail footer, never in a list row.

**Project status** — `active` · `paused` · `done` · `archived`

**Token / OAuth scopes** — `read` · `write` (+ `offline_access` at the authorization server only).

### 3.3 Endpoints

```
GET    /healthz                                  public
GET    /auth/config                              public → {"providers": ["google"]}
GET    /auth/login/{provider}?redirect_to=       302 to provider
GET    /auth/callback/{provider}                 302, sets session cookie

GET    /v1/me                                    {user_id, email, name, timezone, auth_via, scopes}
POST   /v1/logout[?everywhere=true]              204
GET    /v1/sources

GET    /v1/brief?date=&timezone=&context_id=
GET    /v1/sync?since=&limit=

GET|POST         /v1/{contexts,projects,people,recurrences,tasks}
GET|PATCH|DELETE /v1/{...}/{id}

GET    /v1/tokens
POST   /v1/tokens                                session-cookie only; returns the secret ONCE
DELETE /v1/tokens/{id}

GET    /v1/grants                                connected OAuth clients
DELETE /v1/grants/{id}

GET    /.well-known/oauth-authorization-server
GET    /.well-known/oauth-protected-resource
GET|POST /oauth/authorize                        the consent screen (S1)
POST   /oauth/token · /oauth/revoke · /oauth/register
```

Collections return `{"data": [...], "next_cursor": "<id>" | null}`. Single resources return the bare object. `DELETE` → 204.

### 3.4 The daily brief — `GET /v1/brief`

The single most important endpoint. Params: `date` (YYYY-MM-DD, defaults to today **in the user's account timezone**), `timezone` (IANA override), `context_id` (optional filter).

Returns eight lists plus totals:

| Bucket | Contents | Server sort |
|---|---|---|
| `overdue` | `due_on < date`, status in (todo, in_progress, blocked, delegated) | `due_on` ASC — longest-late first |
| `due_today` | `due_on = date`, same statuses | `estimate_minutes` ASC, **treating no-estimate as 0 — so unestimated tasks sort to the top of this bucket, not the bottom** |
| `planned` | `planned_on = date`, same statuses | `due_on` ASC, undated last |
| `in_progress` | `status = in_progress` | `updated_at` DESC |
| `inbox` | `status = inbox` | `created_at` ASC — oldest first |
| `blocked` | `status = blocked` | `updated_at` ASC |
| `waiting_on` | `status = delegated`, **grouped by person** → `[{person_id, person_name, tasks[]}]` | `due_on` ASC within each person; **undated items come first**, not last. Person groups are in first-seen order, not sorted by name or staleness |
| `completed_today` | `status = done`, completed on `date` | `completed_at` DESC |

`totals` — `overdue, due_today, planned, inbox, blocked, waiting_on, in_progress, completed_today, planned_minutes, planned_without_estimate`.

**Five behaviours you must design for:**

1. **Buckets overlap.** One task can appear in `overdue`, `planned` and `blocked` simultaneously. The design must not read as "three separate tasks". Either de-duplicate visually (a task appears in its highest-priority bucket with markers for the others) or make repetition obviously intentional. **Make a recommendation and justify it.**
2. **`inbox` ignores `context_id`.** An untriaged task has no context, so filtering would always empty the bucket. When a context filter is active, the inbox count is still the *global* count. Label it so this is not read as a bug.
3. **Lists are capped at 100; totals are not.** A user with 143 overdue tasks gets 100 rows and `totals.overdue = 143`. Design the "43 more" affordance.
4. **`planned_minutes` + `planned_without_estimate` are the over-commitment signal.** "2h10 planned, 3 tasks with no estimate" is the honest reading. Never render `planned_minutes` as if it were the complete picture when `planned_without_estimate > 0`.
5. **`due_on` vs `planned_on` are different things.** Due = the deadline that exists in the world. Planned = the day the user *intends* to do it. A task can be due Friday and planned for Tuesday. The design must keep them visually distinct; conflating them destroys the whole model.

### 3.5 Task filters — `GET /v1/tasks`

`status` · `kind` · `priority` (all repeatable or comma-separated) · `context_id` · `project_id` · `parent_id` · `delegated_to_id` · `recurrence_id` · `planned_on` · `planned_before` · `planned_after` · `due_on` · `due_before` · `due_after` · `q` (searches title + details) · `top_level` · `include_deleted` · `limit` (default 50, **max 200**) · `cursor`

`?context_id=null` **is the inbox**. `?project_id=null` and `?parent_id=null` work the same way. `?parent_id=null` and `?top_level=true` are the same question.

> **RESOLVED 2026-07-25 — `sort` and `order` now exist.** This section previously
> recorded a hard constraint; it no longer applies.
>
> With no explicit sort, tasks are ordered `urgent`, `high`, `medium`, `low`,
> unprioritized, then newest first inside each rank. `sort` accepts `priority`,
> `created_at`, `updated_at`, `due_on`, `planned_on`, `title`,
> `estimate_minutes`, `completed_at`, `status`; `order` is `asc`/`desc`.
> Direction defaults to `desc` for `created_at` and `asc` for everything else.
> **Rows with no value for the sort column always sort last, in both directions.**
>
> Pagination under a sort is keyset rather than offset, so it does not drift when
> rows change between pages. `next_cursor` is opaque and **bound to the sort that
> issued it** — reusing one under a different `sort`/`order` is a 422, so a UI that
> lets the user change sort must restart the walk rather than reuse the cursor.
>
> Date-ordered views of any size are therefore buildable. `Upcoming` (`W10`) and
> large project lists no longer need an ordering disclosure.

### 3.6 Sync — `GET /v1/sync?since=<rev>`

Every mutable row carries `rev` from one global counter. Clients persist a cursor, poll, and replay. Deletes arrive as tombstones (`deleted_at` set) and must be applied as deletions. `sources` is sent only on a full sync (`since=0`). Response also carries `server_time`, so a badly skewed device clock can be detected and reported as a clock problem rather than as wrong due dates.

**There is no offline write path.** The server is authoritative; sync is one-way, server → client. The user chose: **read-only cache + blocked writes.** Concretely:

- Last-synced data stays fully readable with no network.
- A persistent, non-modal banner marks the data stale, with the age ("Last updated 14 minutes ago").
- **Every** control that would write is visibly disabled — checkboxes, status pickers, capture buttons, drag handles, swipe actions — with a single consistent explanation on interaction attempt.
- Capture is disabled too. Do **not** design a local draft queue; it implies a flush that does not exist. If you believe a draft-only-never-sent buffer is worth it, propose it separately as a phase-2 item with its own UI for "3 unsent captures".

### 3.7 Invariants the UI must enforce *before* the request

Breaking these returns 422 with a `fields` object. Good design prevents the round trip.

| Invariant | UI consequence |
|---|---|
| `status = delegated` requires `delegated_to_id` | Choosing "Waiting on" opens a person picker in the same gesture; it cannot be committed empty. Clearing the person must offer "→ To do" rather than erroring. |
| A project must live in the **same context** as the task referencing it | The project picker only lists projects of the task's current context. Changing a task's context invalidates its project — warn and clear in the same action, do not silently drop it. |
| `parent_id` and `blocked_by_id` reject cycles **at any depth** | The parent/blocker picker must exclude the task's own subtree and blocker chain. If the server still 422s, show it inline on the field. |
| `estimate_minutes > 0`, whole minutes only | Estimate input is minutes. Display as `45m`, `1h30`. Reject 0 and fractions in the control. |
| Dates are real calendar dates | `2026-02-31` is a 422, not just a shape error. Use a real date picker. |
| `completed_at` / `cancelled_at` are server-managed | Never editable. Reopening a task clears them automatically — say so in the undo affordance. |
| `recurrence_id`, `occurrence_on`, `kind`, `rev` are not writable | A spawned occurrence cannot be detached from its series. Its detail screen links to the series; it does not offer to edit the recurrence inline. |
| Body limit 1 MiB | Only reachable via `details`. Soft-warn at ~500KB in a details field. |

### 3.8 PATCH semantics — absent ≠ null ≠ set

`{}` changes nothing. `{"due_on": null}` **clears** the due date. `{"due_on": "2026-08-01"}` sets it. An unknown field is a **400**, not a silent no-op.

This matters for design: **"clear this field" and "leave this field alone" must be two visibly different actions.** Every optional field needs an explicit clear affordance (an × on the chip, a "No date" row in the picker) that is distinct from cancelling out of the editor.

### 3.9 Not available — do not design these

There is no server support for any of the following. If a screen needs one, it must be flagged rather than drawn:

- **Flags or stars separate from priority** — no additional field exists.
- **Tags or labels** — none. Contexts, projects and sources are the only classification axes.
- **A "Someday" / backlog status** — the 7 statuses in §3.2 are exhaustive. There is nowhere to park something with no date and no intent beyond `todo`.
- **Attachments, images, files** — none. `reference_url` + `reference_label` is the only external pointer.
- **Comments, activity log, history, audit trail** — none. `created_at` / `updated_at` only.
- **Notifications, reminders, push, badges driven by the server** — no push infrastructure. Any iOS badge or reminder must be computed locally on-device from synced data. Say so where you use one.
- **Saved filters / custom smart lists, server-side** — buildable client-side in localStorage / `UserDefaults` only, and therefore per-device and not synced. Design accordingly or leave out.
- **Sub-projects, project nesting, portfolios** — a project belongs to exactly one context and has no children.
- **Sorting or reordering by drag within a list, persisted** — tasks have no `sort_order`. Only *contexts* have one. Drag-to-reorder tasks is not persistable.
- **Time tracking, actual-vs-estimate** — only `estimate_minutes` exists.
- **Multi-select bulk edit endpoint** — no batch API. Bulk actions must fan out to N PATCH calls; design the partial-failure state ("14 of 16 moved") rather than assuming atomicity.
- **`contexts.color` format is unvalidated free text.** Specify the exact format you want (e.g. 7-char hex) and the exact palette; flag it for engineering to enforce.

### 3.10 Error codes → UI

| Code | Cause | Required treatment |
|---|---|---|
| 400 | Malformed JSON, unknown field | Client bug. Generic non-blocking error toast + "Report" affordance. Never blame the user. |
| 401 | Missing / unknown / revoked / expired credential | **Session or token is dead.** Web: full-screen re-auth, preserving intended destination. Native: re-auth sheet; never silently discard an in-flight capture. |
| 403 | Credential lacks `read` (GET) or `write` (mutation) scope | Read-only mode. Every write control disabled with "This device has read-only access." Distinct from offline. |
| 404 | No such row — **or it belongs to someone else** | Treat as "no longer exists". Deep links must handle it (e.g. a task deleted on another device while a widget still points at it). |
| 413 | Body over 1 MiB | Inline on the details field. |
| 415 | Content-Type not JSON | Client bug. |
| 422 | Validation failed | `{"error": "...", "fields": {"due_on": "must be a YYYY-MM-DD date"}}` — **map each key to its field and render inline.** Never show a generic toast when `fields` is populated. |

### 3.11 Delete cascades — the confirmation copy must be exact

Deletes are tombstones, and each keeps the graph coherent. The consequence is non-obvious in every case, so **every delete confirmation must state what happens to the children**, using these facts:

| Deleting | What actually happens |
|---|---|
| **Context** | Its projects and recurrences are deleted. **Its tasks are moved to the Inbox** — they are *not* deleted. |
| **Project** | Deleted. Its tasks stay in their context and simply lose the grouping. |
| **Person** | Deleted. Tasks delegated to them **return to `todo`** (the schema forbids a delegated task with no delegate). |
| **Task** | Deleted **with its entire subtree**. Anything that was blocked by it is unblocked and returned to `todo`. |
| **Recurrence** | Deleted and deactivated. **Occurrences already spawned are left alone** — they are real completion history. |
| **Moving a project to another context** | Its tasks move with it. |

### 3.12 Recurrence

The template holds an RFC 5545 `rrule` string, e.g. `FREQ=WEEKLY;BYDAY=MO`. Validation requires a `FREQ` part from `DAILY, WEEKLY, MONTHLY, YEARLY, HOURLY, MINUTELY, SECONDLY`; the rest of the rule is passed to a full RRULE engine, so `INTERVAL`, `BYDAY`, `BYMONTHDAY`, `COUNT` and `UNTIL` all work.

The spawner materialises each occurrence as a **real task row** with `recurrence_id` + `occurrence_on`, and `due_on` set to the occurrence date. So completion history survives ("the Monday report was done thirty times") and every list treats recurring and one-shot tasks identically.

Properties the UI must reflect:
- **`lead_days`** — how many days *ahead of its due date* an occurrence appears. `lead_days: 2` on a Monday report means it shows up Saturday. This is the single most confusing field in the app; the editor must explain it in plain language with a live example.
- **Catch-up is bounded to 7 days.** Coming back from two weeks away produces the last week of missed occurrences, not fourteen. Older ones are counted as *missed* and never appear. The recurrence detail screen should be able to say so.
- **A series that reaches `ends_on` or exhausts `COUNT=` is deactivated, not deleted.** `active: false` is a distinct, designable state.
- **`timezone` is per-series**, because `occurrence_on` is a plain date and the same instant is a different day either side of the date line.
- `next_occurrence_on` and `last_spawned_on` are read-only server state — excellent material for the series detail screen.

### 3.13 Authentication

Two credential kinds, both resolving to the same user:

| Credential | Used by | How it is obtained |
|---|---|---|
| **Session cookie** | Web (`W`, `MW`) | Google OIDC → `/auth/login/google` → callback sets a `__Host-` cookie. Idle timeout 14d, hard ceiling 90d. |
| **Bearer token** | iOS (`I`), macOS (`M`) | See below. |

**`POST /v1/tokens` rejects bearer-token callers** — a token cannot mint another token. So the native apps have exactly two paths in:

- **Primary: OAuth 2.1 + PKCE.** The server is a full authorization server. Native app opens `/oauth/authorize` in `ASWebAuthenticationSession`, user consents on `S1`, code is exchanged at `/oauth/token`, refresh token rotates. Scopes `read write offline_access`. Redirect URIs are matched **exactly** — no prefixes, no wildcards. *(Depends on engineering registering the native clients; see §14.)*
- **Fallback: paste a device token.** User signs into the web app, Settings → Devices → New token, and the secret is shown **once**. Design a paste path for headless/broken cases.

**Critical: Checkmate is self-hosted, so the native apps do not know the server address.** The first screen of `I1` and `M1` must collect a server URL, validate it against `/healthz` and `/auth/config`, and fail legibly on: unreachable host, TLS error, wrong service at that URL, `providers: []` (no identity provider configured → bearer-token-only, so route straight to the paste path).

**Sign-in refusal reasons** are distinct and each needs its own copy — see `W1b`:
- Link expired or already used → "This sign-in link has expired or was already used. Please start again."
- Address not on the allowlist → "This address does not have a Checkmate account." *(Provisioning is gated by an env allowlist. This is the most likely first-run failure. It must not read as "wrong password".)*
- Provider did not verify the email.
- Generic failure.

---

## 4. Visual direction — open brief

**Deliverable: 2–3 complete visual directions applied to the same three screens** (`W3` Brief, `W8` Task detail, `I3` iPhone Brief), light and dark, so they can be compared on identical content. Then one is chosen and you apply it everywhere.

### Constraints every direction must satisfy

1. **Tonally calm and personal.** Things 3 / Apple Reminders territory, not Jira. Whitespace over density. This app is read at 8am with coffee.
2. **Light and dark are both first-class**, not one derived from the other. macOS and iOS follow the system; web follows `prefers-color-scheme` with a manual override.
3. **Four contexts need four distinguishable colours** that (a) survive being reduced to a 8px dot, (b) work on both light and dark surfaces, (c) are distinguishable with deuteranopia and protanopia, and (d) never carry meaning alone — always paired with the context name or an icon.
4. **A separate, non-competing semantic set** for overdue / blocked / done / delegated. Overdue must be attention-getting without turning the morning brief into an alarm panel. This is the hardest colour problem in the app: a user with 12 overdue tasks should feel informed, not accused.
5. **Numbers are content.** Counts, `2h10`, `4 of 9`, dates and estimates appear constantly. Specify how numerals are set — tabular figures for anything in a column, always.
6. **Density scales.** The same task row must work at web-desktop comfortable, mobile-web touch, and a 3-row widget. Specify a compact / regular / comfortable set rather than one row design.
7. **Native-appropriate, not uniform.** The iOS and macOS apps should feel native (system materials, system controls, native navigation idioms) while sharing the web app's identity. Explicitly document what is shared (colour, type ratios, iconography, row anatomy) and what is platform-native (chrome, controls, navigation, motion).
8. **Accessibility floor:** WCAG 2.2 AA contrast for all text and meaningful non-text; Dynamic Type to XXL on iOS without layout breakage; full keyboard operability on web and macOS; `prefers-reduced-motion` and Reduce Motion honoured.

### Brand

The name is **Checkmate**. There is no existing logo, wordmark, or palette — propose them. The chess association is available but not mandatory, and a literal chess-piece logo is probably too cute for a tool that is opened while stressed. Deliver: wordmark, app icon (iOS + macOS, all required sizes), favicon, and a single-glyph mark for the widget and menu bar.

---

## 5. Design tokens & component inventory

### 5.1 Tokens

Deliver as a JSON/YAML token file plus a rendered reference sheet. Semantic names, not literals — `color.text.secondary`, never `grey.600`, in component specs.

**Required token groups:**

| Group | Must contain |
|---|---|
| Colour — surface | `page`, `raised`, `sunken`, `overlay`, `scrim` |
| Colour — text | `primary`, `secondary`, `tertiary`, `disabled`, `inverse`, `link` |
| Colour — border | `subtle`, `default`, `strong`, `focus` |
| Colour — accent | `default`, `hover`, `active`, `subtle-bg`, `on-accent` |
| Colour — semantic | `overdue`, `due-today`, `blocked`, `delegated`, `in-progress`, `done`, `cancelled`, `warning`, `danger` — each with `fg`, `bg`, `border` |
| Colour — context | 4 hues × (`dot`, `bg-subtle`, `fg-on-subtle`, `border`), plus a defined fallback for a 5th+ user-created context and for `color: null` |
| Type | Family stack (web + iOS/macOS system equivalents), scale (≥7 steps), weights, line-heights, letter-spacing; a tabular-numeral variant; a monospace face for tokens/IDs/RRULE |
| Space | 4px-based scale, ≥8 steps |
| Radius | ≥4 steps + `full` |
| Shadow / elevation | ≥3 levels, defined separately for light and dark (dark cannot use the same shadows) |
| Motion | Durations (`instant`/`fast`/`base`/`slow`), easings (`standard`/`decelerate`/`accelerate`/`spring`), and the reduced-motion substitution for each |
| Z-index | Named layers: content, sticky, dropdown, sheet, modal, toast, tooltip |
| Icon | Sizes; source (recommend SF Symbols on Apple platforms + a matched web set); stroke weight |

### 5.2 Component inventory

Every component needs: anatomy diagram, all variants, all interaction states (`rest, hover, focus-visible, active, selected, disabled, loading, error, read-only`), light + dark, and its own redlines.

**Core**
1. **Task row** — *the most important component in the app.* Variants: brief-bucket, list, subtask, search result, widget-compact, triage-card. Anatomy must accommodate, and gracefully drop in this display order: completion control, title (1–2 line clamp), priority badge, context dot+name, project name, due date, planned date, estimate, kind badge, delegate name, blocker, source, subtask progress, reference link icon, recurring glyph, selection state, drag affordance. **Specify exactly which elements drop at each density and width, and in what order.**
2. **Completion control** — checkbox/circle. States: open, hover, pressed, completing (the animation), done, cancelled, disabled, read-only. Must also expose "cancel" as distinct from "complete" — cancelled is not done.
3. **Status control** — the 7-value picker. `delegated` must chain into person selection.
4. **Context chip / dot** — dot-only, dot+label, filter-pill, and picker-row forms.
5. **Date chip** — must visually distinguish **due** from **planned** at a glance, plus overdue emphasis, "today"/"tomorrow"/weekday relative labels, and a clear (×) affordance.
6. **Estimate chip** — `15m`, `1h30`, `no estimate`.
7. **Kind badge** — 4 rendered kinds (`short` has none).
8. **Person chip** — name, optional avatar/initials (no avatar images exist — initials only), context association.
9. **Count badge** — sidebar and tab-bar counts, including the zero and >99 cases.
10. **Progress meter** — subtask progress, project progress, and the brief's "4 of 9 done". Also the `planned_minutes` capacity read.
11. **Reference link chip** — favicon-less, `reference_label` with `reference_url` host fallback.

**Input**
12. **Token-parsing capture field** (see §6) — the highest-risk component. Needs: typed text, live-resolving chips, ambiguity/unresolved states, autocomplete popovers for `#context`, `@person`, `!source`, keyboard-only operation, and a spec for how a chip is un-tokenized back to plain text.
13. Text field, textarea (auto-growing, for `details`), date picker, minute-duration input, searchable single-select (context/project/person/task), segmented control, toggle, RRULE builder (see `W13`).

**Structure**
14. Sidebar (with expandable context sections, `W2`), tab bar (`I2`/`MW`), toolbar, three-pane split (`M1`), detail panel, sheet/modal, popover, command palette (`W9`), dropdown menu, context menu, empty state, skeleton loader, banner (offline/stale/read-only), toast (with undo), confirmation dialog (with cascade copy, §3.11), section header with count, keyboard-shortcut hint, tooltip, avatar-initials, sync-status indicator.

---

## 6. The capture system

Capture is the feature the app lives or dies by. The user chose **inline token parsing, resolved client-side**.

### 6.1 Grammar

A single text field. Everything is optional except the title. Parsing happens live as the user types, and **the parse is always previewable and always reversible before saving**.

| Token | Resolves to | Examples |
|---|---|---|
| `#word` | `context_id` | `#upsun`, `#perso` (prefix-matched against context names and slugs) |
| `/word` | `project_id` | `/q3launch` — **only offered once a context is known**, per §3.7 |
| `@name` | `delegated_to_id` + `status = delegated` | `@marc` — matches an existing person, or **creates one** (the API's `delegated_to` field does this in a single call) |
| `!word` | `source` | `!slack`, `!email`, `!meeting` (6 fixed values) |
| bare duration | `estimate_minutes` | `30m`, `1h`, `1h30`, `90m` |
| natural date | `due_on` | `today`, `tomorrow`, `mon`, `next tuesday`, `26 jul`, `2026-08-01`, `in 3 days`, `eom` |
| `>` + date | `planned_on` (as opposed to due) | `>tomorrow` = plan for tomorrow |
| `^title` or a picker | `parent_id` | Prefer a picker; specify whichever you choose |

### 6.2 Hard requirements

1. **Nothing is silently swallowed.** Every token that becomes a field must render as a visible chip, and the raw text must be recoverable. A task titled "Ship the 30m demo" must not lose "30m" from its title without the user seeing it happen — and must be correctable in one keystroke.
2. **Ambiguity is shown, never guessed.** `#p` matching both Personal and a future "Platform" context shows a disambiguation list. `mon` on a Monday must state which Monday it resolved to.
3. **`due` vs `planned` must be learnable.** The plain form (`tomorrow`) sets `due_on`. Design the affordance that teaches `>` for planned, and consider whether a two-field expanded mode is a better default than a syntax the user has to remember. **Make a recommendation.**
4. **Keyboard-complete.** The whole flow — open, type, disambiguate, save, save-and-open-detail — must be possible without a pointer. Specify every key.
5. **Escape hatch.** `⌘↵` (or an explicit "More" affordance) opens the full form with everything the parser found pre-filled. Nobody should have to fight the syntax.
6. **Zero-friction default.** With no tokens at all, `↵` saves a title-only task to the Inbox with `context_id: null`, `status: inbox`. **This is the most common path and must be the fastest.**
7. **Voice.** iOS dictation feeds the same parser. Spoken "hashtag upsun" will not appear as `#upsun`, so specify how a dictated capture is reviewed before saving — and note that spoken text is far more likely to have its title mangled by the parser. **Recommend whether voice capture should parse at all, or always save title-only to the Inbox.**

### 6.3 Where capture is reachable

| Surface | Entry point |
|---|---|
| `W` web desktop | `⌘K` / `C` from anywhere; a persistent field at the top of the Inbox; per-list inline "+ Add" that pre-fills the list's own filters |
| `MW` mobile web | Floating action button; sticky field on Inbox |
| `I` iPhone | In-app `+`; Home Screen widget tap; Lock Screen widget; Control Center button; Action Button; Siri; share sheet |
| `M` macOS | Toolbar `+`; `⌘N`; global hotkey panel (`M6`, to confirm) |

---

## 7. Web — desktop (`W`)

Breakpoints: `≥1440` wide · `1024–1439` standard · `768–1023` compact (sidebar collapses to icons) · `≤767` → mobile (`MW`, §8).

### W1 · Sign-in

**Route** `/signin` · **Data** `GET /auth/config` · **Auth** none

Full-screen, centred. Wordmark, one-line product statement, one button per configured provider (today: "Continue with Google"), footer with version and a link to the self-hosting docs.

**States**
- *Loading* — probing `/auth/config`; button area is a skeleton, not a jumping layout.
- *Ready* — one or more provider buttons.
- *No provider configured* (`providers: []`) — no button. Copy: this server has no identity provider set up; the web app cannot sign in; use a device token via the CLI. Include the exact env vars to set (`CHECKMATE_GOOGLE_CLIENT_ID` / `_SECRET`).
- *Server unreachable* — `/auth/config` failed. "Cannot reach the Checkmate server" + retry.
- *Redirecting* — after the click, before the provider takes over. Blocking, non-cancellable, ~200–2000ms.
- *Already signed in* — never rendered; redirect to `/`.

**Interactions** — `redirect_to` must be preserved through the whole round trip, so a 401 on `/tasks/abc123` returns the user to `/tasks/abc123`, not to the Brief.

### W1b · Sign-in refused

Same layout as `W1` with an inline message block above the buttons. Four distinct variants — copy is prescribed in §12.2 because the wording is load-bearing:

1. **Link expired / replayed** — recoverable, primary action "Try again".
2. **Not on the allowlist** — *not* recoverable by retrying. Must not look like a wrong-password error. Explain that provisioning is gated and what the account owner must change.
3. **Email not verified by the provider** — explain, point at the provider.
4. **Generic failure** — retry + "if this persists, check the server logs".

### W2 · App shell

Persistent across every authenticated route.

```
┌────────────────┬──────────────────────────────────────────────────────┐
│  Checkmate     │  ⌘K                              ⟳ synced   ◯ Guillaume│
│  ─────────────  ├──────────────────────────────────────────────────────┤
│  ☀ Brief     9 │                                                      │
│  ✉ Inbox     4 │                                                      │
│  ─────────────  │                                                      │
│  ▾ UPSUN     5 │                  ← route content →                   │
│      Q3 launch │                                                      │
│      Platform  │                                                      │
│  ▸ PERSONAL  2 │                                                      │
│  ▸ GAAL      1 │                                                      │
│  ▸ ARKEA     1 │                                                      │
│  ─────────────  │                                                      │
│  ⏳ Waiting   3 │                                                      │
│  ⟳ Repeating   │                                                      │
│  ⛌ Blocked   2 │                                                      │
│  ─────────────  │                                                      │
│  + New context │                                                      │
└────────────────┴──────────────────────────────────────────────────────┘
```

**Sidebar structure** (the chosen navigation model — contexts are *sections*, not a filter chip):

| Group | Items | Count shown |
|---|---|---|
| Top | **Brief**, **Inbox** | Brief = actionable total for today (`overdue + due_today + planned`, de-duplicated); Inbox = `totals.inbox` |
| Contexts | One expandable row per context, ordered by `sort_order`. Expands to that context's **projects** (status `active` and `paused`; `done`/`archived` behind a "Show N completed") | Open tasks in that context |
| Cross-cutting | **Waiting on**, **Repeating**, **Blocked** | `totals.waiting_on`, active series count, `totals.blocked` |
| Footer | + New context · Settings · account menu | — |

**Interactions**
- Click a context row → `W5` (that context's overview). Click the disclosure → expand/collapse without navigating. **These must be separate hit targets.**
- Expansion state persists per context in localStorage.
- Contexts are reorderable by drag (`sort_order` is real and writable). Projects and tasks are **not** (§3.9).
- Drag a task onto a context row → move (PATCH `context_id`); onto a project row → move to that project and its context. Dropping onto a project of a *different* context must state that both change.
- Right-click a context → Rename, Change colour, Archive, Delete (with §3.11 cascade copy).
- Collapsed sidebar (≤1023px, or manual toggle): icon rail with counts as dots; contexts become a single flyout.
- Archived contexts are hidden; reachable from Settings.

**States**
- *Loading* — skeleton rows; counts appear last, never as flashing zeroes.
- *No contexts* — only possible if the user deleted all four. Show a create prompt in the sidebar body.
- *Stale/offline* — the sync indicator becomes the stale banner's anchor; counts get a "as of HH:MM" tooltip.
- *Read-only credential (403)* — "+ New context" and all drag targets disabled.

**Sync indicator** — four states: synced (with relative timestamp on hover), syncing, stale (age + manual retry), failed (error + retry). Never a spinner that runs forever.

### W3 · Daily Brief — the home screen

**Route** `/` · **Data** `GET /v1/brief?date=&context_id=` · **Refresh** on focus, on sync tick, and at local midnight (the date rolls over — handle it, do not leave yesterday on screen)

```
┌──────────────────────────────────────────────────────────────────────┐
│  Saturday 25 July 2026            ‹ Today ›        All contexts ▾    │
│                                                                      │
│   3 overdue · 5 due today · 6 planned · 2h10 (3 without estimate)    │
│   ▓▓▓▓▓▓▓▓░░░░░░░░░░  4 of 13 done today                            │
│                                                                      │
│  OVERDUE · 3 ──────────────────────────────────────────────────────  │
│   ○  Send the Arkea invoice          due 21 Jul (4d)  ● Arkea        │
│   ○  Reply to Marc about pricing     due 23 Jul (2d)  ● Gaal   → Marc│
│   ○  Renew the domain                due 24 Jul (1d)  ● Personal     │
│                                                                      │
│  DUE TODAY · 5 ────────────────────────────────────────────────────  │
│   ○  Standup notes                   15m   ● Upsun    ⟳ every weekday│
│   ○  Review PR #412                  30m   ● Upsun    ↗ github.com   │
│   …                                                                  │
│                                                                      │
│  PLANNED TODAY · 6 · 2h10 ─────────────────────── 3 without estimate │
│   ○  Draft the Q3 deck               1h    ● Upsun   /Q3 launch      │
│   …                                                                  │
│                                                                      │
│  IN PROGRESS · 2 ──────────────────────────────────────────────────  │
│  WAITING ON · 3 ───────────────────────────────────────────────────  │
│   Marc · 2      ○ Pricing sign-off       ○ Invoice approval          │
│   Sophie · 1    ○ Access to the staging env                          │
│                                                                      │
│  BLOCKED · 2 ──────────────────────────────────────────────────────  │
│  INBOX · 4 ─────────────────────────────────────── Triage all →      │
│  DONE TODAY · 4 ─────────────────────────────────────── collapsed ▸  │
└──────────────────────────────────────────────────────────────────────┘
```

**Header**
- Full date, in the account timezone. If the requested `timezone` differs from the account's, say so explicitly.
- Date stepper — ‹ / › / "Today". Past dates are historical (read the day that was); future dates are a forecast. **A future brief has an empty `completed_today` and a meaningless `in_progress` — handle both rather than showing empty sections.**
- Context filter — "All contexts" + one entry per context. **When a context is selected, the Inbox section must be labelled as global** (§3.4.2), e.g. "INBOX · 4 · all contexts".

**Summary strip** — the over-commitment read. `planned_minutes` rendered as `2h10`, always accompanied by `planned_without_estimate` when non-zero. The progress meter is `completed_today / (completed_today + open actionable)`. Define exactly what "open actionable" means and label it so the denominator is not mysterious.

**Sections** — in the fixed order above. Each has: a title, the real total from `totals` (not the row count), an optional right-side affordance, and collapse state persisted per section.

**Section-specific rules**
- *Overdue* — server-sorted oldest-first. Show days-late explicitly ("4d"), because "21 Jul" alone does not convey lateness on a scan.
- *Due today* — server-sorted by estimate ascending, with unestimated tasks first (§3.4). Either keep that order and make "no estimate" legible at the top, or re-sort client-side and label it. **Pick one and say which.**
- *Planned today* — the capacity section. This is where `2h10` belongs.
- *Waiting on* — **grouped by person, and the person is the primary unit.** A follow-up is made per person, not per task. Each person group needs a one-gesture "Follow up with Marc" affordance; specify what it does (recommendation: opens capture pre-filled with `@marc` and today's date, since there is no email integration).
- *Inbox* — a preview plus "Triage all →" into `W4`. Do not make the brief a second triage surface.
- *Done today* — collapsed by default, expandable. The day's evidence of progress.

**Overlap handling (§3.4.1)** — a task in `overdue` + `planned` + `blocked` must not read as three tasks. **Recommend and justify:** either canonical placement (highest-priority bucket) with inline markers for the others, or full repetition with an explicit visual link between instances.

**Capping (§3.4.3)** — when `totals.X > 100`: "Showing 100 of 143 · See all" → `W7` pre-filtered.

**Interactions**
- Click a row → `W8` detail panel (slides over the right ~40%; the brief stays readable behind it).
- Completion control → PATCH `status: done`; the row animates out of its bucket; toast with **Undo** for 8s (PATCH `status: todo`, which clears `completed_at`); the summary strip and sidebar counts update optimistically and reconcile.
- Hover a row → a compact action set: complete, plan for today/tomorrow, set due date, delegate, open. Also available via right-click context menu and via keyboard.
- `J`/`K` or ↑/↓ move a row cursor across the whole brief, crossing section boundaries. `X` selects. `Space` previews. `↵` opens.
- Multi-select (shift-click, `X`) → bulk bar. **Bulk = N PATCH calls, no batch endpoint (§3.9): design the partial-failure result** ("14 of 16 moved · 2 failed · Retry").
- Drag a row between sections — **only where it maps to a real field change.** Dragging into *Planned today* sets `planned_on` = the brief's date. Dragging into *Overdue* means nothing and must not be a drop target. Enumerate exactly which sections accept drops.

**States**
- *Loading* — skeleton with section headers present (they are known) and rows shimmering. Do not render zero counts during load.
- *Perfect empty day* — nothing overdue, nothing due, nothing planned, inbox clear. This is a **reward state** and deserves real design attention, not "No tasks". It is the state the whole product is aiming at.
- *Partial empty* — an individual section with 0 items collapses to a single quiet line, or hides entirely. **Specify which sections hide when empty and which persist** (recommendation: Overdue, Blocked, Waiting on and Done today hide; Due today, Planned and Inbox persist with an empty affordance, because their emptiness is information).
- *Error* — brief request failed. Retry, plus the cached last-good brief with a stale marker if one exists.
- *Stale/offline* — banner, all controls disabled per §3.6.
- *Read-only (403)* — banner, completion controls visibly non-interactive.
- *Future date* — no `completed_today` section, `in_progress` hidden or labelled "as of now".
- *Past date* — read-only historical framing; do not offer "plan for today".
- *First run* (fresh account: 4 contexts seeded, 0 tasks) — an onboarding brief that teaches capture. Specify it.

### W4 · Inbox & triage

**Route** `/inbox` · **Data** `GET /v1/tasks?context_id=null&status=inbox` (also `status=inbox` with a context set, which is legal, if rare)

Two modes:

**List mode** (default) — sticky capture field at the top, then the untriaged tasks, oldest first (matching the brief's ordering). Each row exposes fast triage controls inline: context, due, planned, estimate, delegate, project.

**Focus mode** ("Triage all →") — one task at a time, full-width card, keyboard-driven:

```
┌──────────────────────────────────────────────────────────────────────┐
│  Triage · 2 of 4                                          Esc  Done  │
│                                                                      │
│    Call the accountant about the Q2 filing                           │
│    captured 2 days ago · from Slack · via iOS widget                 │
│                                                                      │
│    Context     [1 Upsun] [2 Personal] [3 Gaal] [4 Arkea]             │
│    When        [T today] [M tomorrow] [W this week] [D pick a date]   │
│    Estimate    [15m] [30m] [1h] [2h] [custom]                        │
│                                                                      │
│    ↵ To do      D delegate…      B blocked      ⌫ delete             │
│                                    S skip       ⌘↵ open full detail  │
└──────────────────────────────────────────────────────────────────────┘
```

**Interactions**
- Every choice is a single keypress; digits pick a context, letters pick dates and statuses. **Specify the complete key map, with no collisions against the global shortcut set.**
- `↵` commits and advances. Committing sends one PATCH with everything chosen (`context_id`, `due_on`/`planned_on`, `estimate_minutes`, `status`).
- `S` skips — leaves it in the inbox and moves on. Skipped items are not re-shown in this pass; the counter reflects that.
- `D` opens the person picker inline; choosing a person sets `status: delegated` + `delegated_to_id` in one PATCH (§3.7).
- Undo is per-item and must reach at least the last 5 decisions, because triage is fast and mistakes are made at speed.
- Progress is explicit ("2 of 4"). A completion state at the end — the second reward moment in the app.

**States** — *empty inbox* (a genuine win state, distinct from `W3`'s empty day); *loading*; *item deleted elsewhere mid-pass* (404 → skip forward with a quiet note); *offline* (focus mode must be unreachable, not broken); *read-only*.

### W5 · Context overview

**Route** `/c/{slug}` · **Data** `GET /v1/brief?context_id=` + `GET /v1/projects?context_id=` + `GET /v1/tasks?context_id=`

The header of one life. Context name and colour; a mini summary (overdue / due today / planned minutes for this context only); projects with progress; then that context's open tasks grouped by project with an "No project" group.

**Elements** — context header (with inline rename, colour, archive, delete); mini-brief strip; project cards or rows with `done/total` progress computed client-side; task list grouped by project; "+ New project"; "+ New task" pre-filled with this context.

**States** — loading; *no projects* (tasks still listed flat); *no tasks* (empty prompt); *archived context* (read-only framing with an Unarchive action); *context deleted elsewhere* (404 → redirect to Brief with a note); offline; read-only.

**Note** — project progress requires counting each project's tasks. With `limit` 200 and no aggregate endpoint, specify how you get the numbers, and design the "progress unavailable for large projects" fallback rather than showing a wrong ratio.

### W6 · Project detail

**Route** `/p/{id}` · **Data** `GET /v1/projects/{id}` + `GET /v1/tasks?project_id={id}&top_level=true`

Project name, description, status (`active`/`paused`/`done`/`archived`), owning context, progress. Then top-level tasks with expandable subtrees.

**Interactions** — inline edit name/description; status change (each of the four states needs a distinct visual treatment, and `done`/`archived` should read as closed); **move to another context — must warn that all the project's tasks move with it** (§3.11); delete with the exact cascade copy; add task pre-filled with project + context.

**States** — loading; empty; paused (visually de-emphasised, still editable); done/archived (read-only-ish framing); 404; offline; read-only.

### W7 · Task list (filtered view)

**Route** `/tasks?<filters>` · **Data** `GET /v1/tasks` with any of the §3.5 filters. Also the target of "See all" from the brief, and of the sidebar's Waiting on / Blocked / cross-cutting entries.

A filter bar that exposes the real API filters and nothing else — status (multi), kind (multi), context, project, delegate, due range, planned range, text query, top-level-only, include-deleted.

```
┌──────────────────────────────────────────────────────────────────────┐
│  Tasks    [status: to do, in progress ×] [context: Upsun ×] [+ filter]│
│           142 tasks · sorted by due date ⚠           Group: none ▾    │
├──────────────────────────────────────────────────────────────────────┤
│  ○  Draft the Q3 deck        due 28 Jul  1h    ● Upsun  /Q3 launch    │
│  ○  Review PR #412           due 25 Jul  30m   ● Upsun                │
│  …                                                                    │
│  ────────────────────────────────────────────────────────────────────│
│  Showing 200 of 142+ · Load more                                      │
└──────────────────────────────────────────────────────────────────────┘
```

**Ordering** follows §3.5:
- Default ordering is **priority first**, honestly labelled: urgent, high, medium,
  low, then unprioritized, newest first within each rank.
- Other supported sorts are server-side and keyset-paginated. Changing sort starts
  a fresh walk because cursors are bound to their ordering.

**Interactions** — filters are URL state (shareable, back-button-correct); grouping by context / project / status / due-date is client-side; multi-select and bulk actions with partial-failure reporting; `include_deleted` reveals tombstoned rows in a distinctly dead treatment (they cannot be restored — there is no undelete endpoint; say so).

**States** — loading; empty-for-these-filters (with "clear filters", distinct from a genuinely empty app); over-200 truncation; invalid filter combination (422 → map to the offending control); offline; read-only.

### W8 · Task detail

**Route** `/t/{id}`, rendered as a right-side panel over the current view, and as a full page on direct navigation · **Data** `GET /v1/tasks/{id}` + parent, children, blocker, blocked-set, person, project, recurrence

```
┌───────────────────────────────────────────────┐
│  ✕                                    ⋯       │
│                                               │
│  ○  Send the Arkea invoice                    │
│                                               │
│  Status      Blocked ▾                        │
│  Blocked by  ○ Get the PO number from Sophie →│
│  Context     ● Arkea ▾                        │
│  Project     — ▾                              │
│  Due         21 Jul 2026 · 4 days ago  ×      │
│  Planned     — +                              │
│  Estimate    30m  ×                           │
│  Delegated   — +                              │
│  Source      Email ▾                          │
│  Reference   ↗ Invoice thread            ×    │
│                                               │
│  Details                                      │
│  ┌───────────────────────────────────────────┐│
│  │ Needs the PO number before it can be      ││
│  │ raised in the portal.                     ││
│  └───────────────────────────────────────────┘│
│                                               │
│  Subtasks · 1 of 3                     + Add  │
│   ☑ Draft the line items                      │
│   ○ Get the PO number                         │
│   ○ Raise it in the portal                    │
│                                               │
│  Blocking · 1                                 │
│   ○ Chase payment                             │
│                                               │
│  ─────────────────────────────────────────────│
│  Recurring · every weekday · next 27 Jul  →   │
│  Captured 21 Jul via iOS widget · updated 2h  │
└───────────────────────────────────────────────┘
```

**Field-by-field requirements**
- **Title** — inline-editable, multi-line, blank not allowed (the DB enforces non-empty after trim).
- **Status** — the 7-value picker. Selecting `delegated` chains to the person picker and cannot be committed without one. Selecting `done`/`cancelled` shows the resulting timestamp read-only.
- **Priority** — optional 4-value picker (`urgent`, `high`, `medium`, `low`) with
  an explicit "No priority" clear action. Show a compact, text-labelled badge in
  rows; do not communicate the rank by colour alone.
- **Blocked by** — searchable task picker, **excluding this task's own blocker chain and subtree** (cycles are rejected at any depth). Optional even when status is `blocked`.
- **Context** — changing it **invalidates the project** (§3.7). One action, two consequences, stated in the confirm: "Move to Personal? This task will leave the project *Q3 launch*."
- **Project** — filtered to the current context's projects. Disabled with an explanation when no context is set.
- **Due** and **Planned** — two visually distinct rows, both individually clearable (× → PATCH `null`, §3.8).
- **Estimate** — minutes; quick presets plus custom; clearable.
- **Delegated to** — person picker with create-on-the-fly. Clearing it while status is `delegated` must offer "→ To do" rather than failing.
- **Source** — the 6 fixed values, plus none.
- **Reference** — URL + label pair. Show the label, fall back to the host.
- **Details** — auto-growing plain textarea. **Plain text only** — no rich text, no markdown rendering unless you specify it and engineering agrees on storage. Soft-warn near the 1 MiB body limit.
- **Subtasks** — a list with inline add, inline complete, and reorder that is *not* persisted (§3.9 — so do not offer drag reordering at all). Progress `done/total`. Each links to its own detail.
- **Blocking** — the reverse edge: tasks whose `blocked_by_id` is this task. Read-only list. **Requires a query the API does not expose directly** — see §14.2.
- **Recurrence footer** — when `recurrence_id` is set: the human-readable rule, `occurrence_on`, next occurrence, and a link to the series (`W13b`). **The series cannot be edited from here** and this task cannot be detached from it (§3.7).
- **Metadata footer** — `capture_method`, `created_at`, `updated_at`. Quiet, small, always present.

**Interactions** — every field saves on commit as its own PATCH (per-field, so a failure is attributable to one field); optimistic with rollback on error; 422 `fields` map to the exact control; `⋯` menu holds Duplicate *(client-side: create a new task from the same values — no server duplicate endpoint)*, Convert to subtask, Copy link, Delete; `Esc` closes the panel; `⌘⌫` deletes with confirmation.

**States** — loading (skeleton keeping the field labels, so the layout does not jump); *saving* (per-field, subtle); *field error* (inline, from `fields`); *404* ("This task no longer exists" with a return path — reachable from a stale widget or a link); *done* (the whole panel reads as closed, with Reopen); *cancelled* (distinct from done); *deleted-elsewhere while open*; *offline* (fully read-only); *read-only credential*; *tombstoned* (only via `include_deleted`).

### W9 · Command palette / quick capture

**Trigger** `⌘K` from anywhere, or `C` when no field is focused.

One field, two modes that the user should not have to think about: **type to capture** (the §6 grammar, live chips beneath) and **type to find** (`>` prefix or fuzzy-matching a command/navigation target). Specify the disambiguation precisely — the failure mode where someone types a task title and gets navigation instead is fatal to trust.

```
┌────────────────────────────────────────────────────────────┐
│  Call Marc about the invoice tomorrow 30m #upsun @marc      │
├────────────────────────────────────────────────────────────┤
│  NEW TASK                                                  │
│   Call Marc about the invoice                              │
│   ⏰ due Tue 26 Jul   ⏱ 30m   ● Upsun   → Marc (new person) │
│                                                            │
│   tomorrow → due date · 30m → estimate                     │
│   #upsun → context   · @marc → delegate, creates "Marc"    │
├────────────────────────────────────────────────────────────┤
│  ↵ Save to Upsun     ⌘↵ Open full form     Esc Cancel      │
└────────────────────────────────────────────────────────────┘
```

**Requirements** — every recognised token echoed as a chip *and* explained in the reasoning line; `@marc` creating a new person must say so before saving; unresolved tokens (`#xyz` matching nothing) shown as unresolved and **left in the title** rather than dropped; `↵` saves and toasts with Undo; `⌘↵` opens `W8` in create mode pre-filled; recent captures reachable for correction.

**States** — empty (shows syntax hints, and this is where the grammar is taught); typing/parsing; ambiguous (disambiguation list); command mode; searching (results grouped: tasks, projects, people, navigation); no results; saving; save failed (**the typed text must never be lost** — this is the highest-stakes error state in the app); offline (disabled with an explanation, text preserved).

### W10 · Upcoming

**Route** `/upcoming` · **Data** `GET /v1/tasks?due_after=&due_before=` (+ the same for `planned_*`)

Date-grouped forward view: Today, Tomorrow, this week by day, then by week, then "Later", then "No date". Toggle between grouping by **due** and by **planned** — they are different questions ("what is owed" vs "what I have committed to doing").

**Depends on §14.1** for correct ordering beyond 200 tasks. Design it with the disclosure from `W7` until then.

**States** — loading; empty range; a day with nothing (show the date with an empty affordance — a clear day is information); over-200; offline; read-only.

### W11 · Waiting on

**Route** `/waiting` · **Data** `GET /v1/tasks?status=delegated` grouped by person client-side, or the brief's `waiting_on`

**Person-first, not task-first.** One card or section per person: name, count, oldest item's age, then their tasks with due dates. A per-person "Follow up" action (spec what it does — there is no email integration; recommend creating a task `@person` due today).

Sort people by staleness (oldest un-actioned first) — this is the "who have I not chased" view. Note that the brief returns person groups in first-seen order (§3.4), so this sort is client-side.

**States** — loading; nothing delegated (empty win state); a person with everything overdue (needs emphasis); offline; read-only.

### W12 · People

**Route** `/people` · **Data** `GET /v1/people`

List with name, email, associated context, open-delegated count. Detail/edit for name, email, context (nullable = appears everywhere), notes. Delete with the exact cascade copy: **their delegated tasks return to "To do"** (§3.11).

Note that `POST /v1/tasks` with `delegated_to` auto-creates people, so this list accumulates entries created by fast capture — including typos. A merge action would be valuable and **does not exist server-side**; flag it (§14.3) rather than designing it as if it works.

**States** — loading; empty; a person with 0 open tasks; delete confirmation; offline; read-only.

### W13 · Repeating tasks (series list)

**Route** `/repeating` · **Data** `GET /v1/recurrences`

Rows: title, human-readable rule ("Every weekday", "Every 2 weeks on Monday", "The 1st of each month"), context, next occurrence, active/inactive. Inactive series (ended or `COUNT` exhausted) in a separate, quieter group — they are history, not errors.

### W13b · Series detail & editor

The **RRULE builder** is the hardest form in the app. It must produce a valid `FREQ=…` string and it must never let the user build something that means nothing.

```
┌────────────────────────────────────────────────────────────┐
│  Weekly report to Arkea                                    │
│                                                            │
│  Repeats     [Daily] [Weekly] [Monthly] [Yearly] [Custom]  │
│  Every       [1 ▾]  week(s)  on  M T W T F S S             │
│  Starting    27 Jul 2026                                   │
│  Ends        ( ) Never  ( ) On date  ( ) After [12] times   │
│  Timezone    Europe/Paris ▾                                │
│                                                            │
│  Show up     [2 ▾] days before it's due                    │
│              → due Monday, appears Saturday                │
│                                                            │
│  Each task   Context ● Arkea   Project —   Estimate 45m    │
│              Delegate —        Source Meeting              │
│                                                            │
│  Next 5:  Mon 27 Jul · Mon 3 Aug · Mon 10 Aug · …          │
│  Active   ●━━  (turning this off stops new occurrences)    │
│                                                            │
│  Last spawned 20 Jul · 14 occurrences created              │
└────────────────────────────────────────────────────────────┘
```

**Requirements**
- A live **"Next 5 occurrences"** preview, computed client-side from the rule. This is the only way a user can verify what they built.
- **`lead_days` explained in plain language with a live example** — "Due Monday, appears in your list Saturday." This field is the most confusing in the data model.
- Timezone is per-series and matters (§3.12); default to the account timezone and explain why it exists.
- **Active toggle** — off stops future spawning, keeps existing occurrences. Not the same as delete.
- Read-only server state: `next_occurrence_on`, `last_spawned_on`, occurrence count.
- Custom mode exposes the raw RRULE string with validation feedback (the server requires a `FREQ` part and rejects an unparseable rule field-wise).
- Delete confirmation: **the tasks it already spawned are kept** (§3.11).
- Note in the design that catch-up is capped at 7 days (§3.12), so the series detail can honestly explain a gap in history rather than looking broken.

**States** — create (empty); editing; invalid rule (inline on the rule); ended (`ends_on` passed → inactive, read-only-ish with a Reactivate path that requires a new `ends_on`); paused (`active: false`); never-spawned; 404; offline; read-only.

### W14 · Search

Full-page results for `q` (title + details only — **not** people, projects or details of related entities; the server searches two columns). Grouped results, filters, empty state that says exactly what was searched. Keyboard-navigable. Also surfaced inside `W9`.

### W15 · Settings

Sectioned, left-nav within the settings route.

| `W15a` **Account** | Name, email, timezone (read from `/v1/me`; **note that no endpoint updates the user record — flag §14.4**), sign out, sign out everywhere (`?everywhere=true`). |
| `W15b` **Contexts** | Reorderable list (`sort_order` is real), colour picker (specify the exact allowed format — §3.9), rename, archive/unarchive, delete with cascade copy, create. Archived contexts live here. |
| `W15c` **People** | Entry point to `W12`. |
| `W15d` **Repeating** | Entry point to `W13`. |
| `W15e` **Devices & tokens** | `GET /v1/tokens`. Each: name, scopes, created, last used, expiry, revoked. Create → name + scopes (`read`, `write`) + optional expiry. **The secret is shown exactly once** — design that moment carefully: copy button, explicit "you will not see this again" acknowledgement, and a confirm-before-dismiss. Revoke with confirmation. Show "last used" prominently so unused tokens can be pruned. Note: token creation requires a session, so this page is web-only by construction. |
| `W15f` **Connected apps** | `GET /v1/grants` — OAuth clients the user has authorised (MCP clients, the native apps). Each: client name, client URI, scopes, audience, granted date. Disconnect (`DELETE /v1/grants/{id}`) with a warning that the client will need re-authorisation. **`client_name` comes from a document a stranger controls — treat it as untrusted text and design for hostile values: 200 characters, RTL overrides, homoglyphs, an empty string.** |
| `W15g` **Appearance** | Light / dark / system. Density. Reduce motion (in addition to the OS setting). |
| `W15h` **About** | Version from `/healthz`, sqlite path/health, sync cursor and last sync, a "Force full resync" action, and a link to the API docs. |

**States for each** — loading, empty, error, offline, read-only, plus the destructive confirmations.

### W16 · Global states

| ID | State | Requirement |
|---|---|---|
| `W16a` | **Stale / offline banner** | Persistent, non-modal, states the age. Every write control disabled. One consistent explanation on attempted interaction. |
| `W16b` | **Read-only credential (403)** | Distinct from offline — the data is *live*, the user simply cannot write. Different copy, same disabling. |
| `W16c` | **Session expired (401)** | Full-screen re-auth preserving destination and any in-flight input. |
| `W16d` | **Sync error** | The indicator's failed state + a retry, without blocking reading. |
| `W16e` | **Clock skew** | `server_time` differs from the device clock by > 5 min → warn, because it silently corrupts "today". |
| `W16f` | **Route not found** | 404 page. |
| `W16g` | **Error boundary** | Unhandled client crash: apologise, offer reload, do not lose unsaved input if avoidable. |
| `W16h` | **Server down** | `/healthz` failing or unreachable → distinguish "server unreachable" from "server degraded, database unreachable" (`healthz` reports the latter explicitly). |
| `W16i` | **First run** | Fresh account, four seeded contexts, zero tasks. A guided path to first capture. |

---

## 8. Web — mobile browser (`MW`)

The same SPA at ≤767px. Not a separate app, but the layout is genuinely different.

**Navigation** — sidebar becomes a bottom tab bar: **Brief · Inbox · Contexts · Search**, plus a centre capture button. Contexts opens a full-screen list drilling into `W5`.

**Screens to spec at mobile width**

| ID | Screen | Adaptation |
|---|---|---|
| `MW1` | Sign-in | Single column, large touch targets. |
| `MW2` | Brief | Sections stack; summary strip becomes a horizontally scrollable stat row or a compact two-line block; date stepper is a swipe *and* buttons. |
| `MW3` | Inbox | Sticky capture at top; triage focus mode becomes a full-screen card deck with swipe gestures. |
| `MW4` | Task list | Filters collapse into a sheet; the row drops to 2 lines with a defined element priority. |
| `MW5` | Task detail | Full-screen route, not a panel. Fields become tappable rows opening sheets. |
| `MW6` | Capture | Full-screen sheet; the chip preview must survive the on-screen keyboard — spec the layout at 320pt height available. |
| `MW7` | Context / project | Full-screen. |
| `MW8` | Settings | Full-screen list drilling into subpages. |

**Mobile-specific requirements**
- Swipe actions on a task row: complete (leading), and a defined trailing set. Specify exactly, and how they differ from the native iOS app so the two do not contradict each other.
- Every hover-only affordance in `W` needs a touch equivalent — enumerate them.
- Safe areas, notch, home indicator, and the iOS Safari URL bar's collapse behaviour.
- No drag-and-drop on touch; every drag interaction in `W` needs a menu-based alternative.
- Nothing depends on a keyboard shortcut.

---

## 9. iPhone — native (`I`)

SwiftUI, iOS 18+. Bearer token in the Keychain. Local sqlite/SwiftData cache fed by `/v1/sync`.

### I1 · Onboarding

Four steps, each its own screen:

1. **Welcome** — what Checkmate is, in two lines.
2. **Server address** — a text field. Checkmate is self-hosted; the app cannot guess. Validate against `/healthz` then `/auth/config`. Failure states, each with distinct copy: unreachable, TLS/certificate failure, not-a-Checkmate-server, server degraded (database unreachable), `providers: []` (→ route to step 3b).
3. **Sign in** — `ASWebAuthenticationSession` against `/oauth/authorize` with PKCE, scopes `read write offline_access`. The user sees the server-rendered consent screen (`S1`) inside the sheet. Handle: user cancelled, consent denied, network failure mid-flow, and the token exchange failing.
   **3b. Paste a device token** — the fallback. Instructions for creating one in the web app, a paste field, validation against `/v1/me`, and a clear reason for when this path exists.
4. **First sync** — progress, honest about duration, then straight into the Brief. Offer the widget and Siri setup here or defer to a post-first-use prompt.

### I2 · Tab structure

Five tabs: **Brief · Inbox · Contexts · Waiting · Search**, with a prominent capture entry. Recommend the capture placement (centre tab vs. floating button vs. toolbar) and justify against one-handed reach.

Tab badges: Inbox = `totals.inbox`, Waiting = `totals.waiting_on`. **Computed locally from cached data — there is no push (§3.9)**; state where they can be stale.

### I3 · Brief

The `W3` content, natively. Sections as SwiftUI list sections with sticky headers; summary strip pinned; context filter in the navigation bar; date stepper as a swipe plus buttons.

**iOS-specific**
- Pull to refresh.
- Swipe leading = complete, with haptic. Swipe trailing = a defined set (recommend: Plan today, Due date, Delegate, More).
- Long-press → context menu with a task preview.
- Dynamic Type to XXL: specify how a task row reflows at large sizes, where the layout switches from horizontal to vertical, and what drops.
- Scroll-to-top and section jumping for a long brief.

**States** — the full `W3` set, plus: first launch with no cache, background-refresh-stale on foreground, Low Power Mode (specify whether background sync is suppressed and how that surfaces), and iOS-level "no network" versus "server unreachable" as different messages.

### I4 · Inbox & triage

List with sticky capture. Triage as a **card deck**: swipe right = To do, left = delete (with undo), up = pick a context, down = skip. Gestures must be discoverable — spec the first-run coach and the always-visible button equivalents. Haptics on each commit. Progress and a completion state.

### I5 · Task list · I6 · Task detail

`W7` / `W8` as native screens. Detail fields become rows that open sheets or inline editors. Specify each field's editing affordance individually. The `⋯` menu becomes a native menu. `Delete` is destructive-styled with the §3.11 confirmation.

### I7 · Capture sheet

The most-used screen in the app. Opens over anything, focuses the field immediately, keyboard up.

**Requirements**
- The §6 chip preview must remain visible above the keyboard on the smallest supported device.
- A keyboard accessory row for the common tokens (context, today, tomorrow, estimate, delegate) so the syntax is optional.
- Dictation: microphone in the field. **Recommend whether dictated text is parsed or saved title-only to the Inbox** (§6.2.7), and design the review step either way.
- Save → dismiss + haptic + a brief confirmation naming where it went ("Added to Upsun" / "Added to Inbox").
- **Save failure must never lose the text.** Design the retry that keeps the sheet alive.
- Offline: disabled with an explanation, text preserved in the sheet.

### I8 · Widgets

| ID | Widget | Content |
|---|---|---|
| `I8a` | **Small** | The three numbers that matter: overdue, due today, planned minutes. Tap → Brief. Design the zero state so a clear day looks like a reward, not an empty box. |
| `I8b` | **Medium** | Today's 3–4 highest-priority rows (overdue first, then due today) + counts. Tap a row → that task. Tap the header → Brief. |
| `I8c` | **Large** | Mini brief: counts, 6–8 rows across overdue/due/planned with section markers, waiting-on count. |
| `I8d` | **Lock Screen** (circular, rectangular, inline) | Circular: overdue count as a gauge. Rectangular: next task + count. Inline: one line of text. |
| `I8e` | **Control Center + Action Button** | A single control that opens capture directly. |

**Widget requirements** — refresh budget is limited: state the target cadence and what happens between refreshes (a timestamp, not stale-looking-live data). Deep links must handle a task that no longer exists (§3.10, 404). Light and dark, all tint modes, `.accented` rendering. No network at render time — read from the shared cache.

### I9 · App Intents & Siri

- **Add a task** — parameterised intent (title, optional context, optional due). Spoken and Shortcuts-driven. Phrases: "Add a task to Checkmate", "Remind me in Checkmate to…".
- **What's on today** — reads back the brief's headline counts.
- **Complete a task** — with disambiguation when the title match is not unique.
- Shortcuts app tiles for the above, so the user can build their own automations.
- Design the confirmation UI for each intent, including the spoken-response text, and the failure cases (not signed in, offline, ambiguous match).

### I10 · Share extension

From Safari / Mail / Slack. Receives a URL and/or text.

- Title pre-filled from the page title or the selection, editable.
- **`reference_url` set to the shared URL, `reference_label` to the page title** — this is exactly what those fields exist for.
- Context picker, optional due date, and a Save that works inside the extension's constrained environment.
- `capture_method: chrome_ext`.
- States: no network (the extension cannot queue — say so plainly), not signed in, save failed, success.

### I11 · Settings

Server address (with a sign-out-and-change path), account, sync status and a force-resync, appearance, widget/Siri setup pointers, about, and sign out (which must clear the Keychain token and the local cache). Device-token management is **not** available here (`POST /v1/tokens` needs a session) — link to the web app and explain why.

### I12 · iOS global states

Offline (§3.6), read-only credential, token revoked (401 → re-auth without losing local state), first launch, first sync, background refresh, low power mode, VoiceOver (full traversal of the brief, meaningful labels for every chip and count), Dynamic Type at XXL, Reduce Motion, Increase Contrast, and the iOS 18 dark/tinted/clear icon variants.

---

## 10. macOS — native (`M`)

SwiftUI, macOS 15+. Full window app, **no menu bar extra**.

### M1 · Onboarding

Same four steps as `I1`, in a window. `ASWebAuthenticationSession` equivalent for the OAuth flow. Token in the Keychain.

### M2 · Window structure

Three panes:

```
┌──────────────┬────────────────────────────┬─────────────────────────┐
│ Sidebar      │ List                       │ Detail                  │
│              │                            │                         │
│ ☀ Brief    9 │ OVERDUE · 3                │  ○ Send the Arkea…      │
│ ✉ Inbox    4 │  ○ Send the Arkea invoice  │                         │
│ ──────────── │  ○ Reply to Marc           │  Status    Blocked ▾    │
│ ▾ UPSUN    5 │  ○ Renew the domain        │  Context   ● Arkea      │
│   Q3 launch  │ DUE TODAY · 5              │  Due       21 Jul       │
│   Platform   │  ○ Standup notes      15m  │  …                      │
│ ▸ PERSONAL 2 │  ○ Review PR #412     30m  │                         │
│ ▸ GAAL     1 │  …                         │  Subtasks · 1 of 3      │
│ ▸ ARKEA    1 │                            │                         │
│ ──────────── │                            │                         │
│ ⏳ Waiting  3 │                            │                         │
│ ⟳ Repeating  │                            │                         │
└──────────────┴────────────────────────────┴─────────────────────────┘
```

- Sidebar identical in structure to `W2`, as a native source list with system materials.
- Middle pane is the current view's list; on Brief it is the sectioned brief.
- Detail pane is `W8`, always present rather than sliding over. Collapsible.
- Native toolbar: capture, filter, view options, sync status. Window state (size, pane widths, selection) restored on relaunch.

### M3 · Screens

`M3a` Brief · `M3b` Inbox & triage · `M3c` Task list · `M3d` Task detail · `M3e` Context overview · `M3f` Project detail · `M3g` Waiting on · `M3h` People · `M3i` Repeating + editor · `M3j` Search — each the `W` equivalent, rendered natively. Do not redesign the information architecture; do redesign the chrome, controls and interaction idioms to be genuinely macOS.

### M4 · Menus & keyboard

A complete menu bar (the app's own menus, not a status item): **Checkmate · File · Edit · Task · View · Window · Help**. Every action in the app must be reachable from a menu, and the menu is where discoverability lives.

Deliver the **full keyboard map**, coordinated with `W`'s web shortcuts so muscle memory transfers where the platform allows. At minimum: new task, quick capture, complete, delete, next/previous, open detail, toggle sidebar, toggle detail, jump to Brief/Inbox/each context, search, refresh, and the triage keys.

### M5 · Settings window

Native multi-tab preferences: General, Account & server, Appearance, Sync, Shortcuts (including the global hotkey if `M6` is approved), About. Same content as `W15` minus token creation.

### M6 · Global capture panel — *proposed, needs confirmation*

A floating, focused capture window on a user-set global hotkey (default `⌥Space`), dismissing on `Esc` or blur. **This is not a menu bar extra** — no status item, no popover, no persistent presence — but it does put the app on a global hotkey, which goes slightly beyond "full window app only". **Design it and mark it clearly as pending approval.** If rejected, capture is toolbar + `⌘N` only.

### M7 · macOS global states

Offline, read-only, 401 re-auth, multiple windows, full screen, Stage Manager, window restoration, VoiceOver, Increase Contrast, Reduce Transparency, Reduce Motion, dark mode, and the app icon at all sizes.

---

## 11. Server-rendered pages (`S`)

Already implemented in Go `html/template` with inline CSS. You are restyling them. **Constraints: inline CSS only, no external requests, no JavaScript, a strict CSP (`default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'`), and they must work before any frontend loads.**

### S1 · OAuth consent screen

`GET /oauth/authorize`. The single most security-sensitive screen in the product: it is the only place a user can tell a legitimate client from one impersonating it.

**Must display** — client name; client URI (optional); **the redirect host, prominently** (for a loopback client this is the *only* signal the user has about what is actually receiving the code); how the client was identified (a published metadata document at a URL, or registration with this server); the resource being accessed; a plain-language line per requested scope; the signed-in user's email; Authorize and Cancel.

**Must handle**
- **A loopback-client warning**, more prominent than the rest of the page: Checkmate cannot verify which program is listening on a local port, so only continue if the user just started the connection themselves.
- **A reconnecting client** — different subtitle when a grant already exists.
- **Hostile input.** `client_name`, `client_uri` and `logo_uri` come from a document a stranger publishes. Design for: a 300-character name, RTL override characters, homoglyph attacks ("Сlaude" with a Cyrillic С), an empty name, a name containing markup, and a `logo_uri` pointing at something enormous or absent. **State how each is handled** — recommend not rendering remote logos at all, since the CSP forbids the request anyway.
- Cancel must be as easy to hit as Authorize, and must never be the visually dominant choice by accident in either direction.

**Scope copy** (currently in the code, refine if you can improve it):
- `read` — "See your tasks, projects, contexts and the people you delegate to"
- `write` — "Create, change and delete your tasks, projects and contexts"
- `offline_access` — "Stay connected without asking you again (it can refresh its own access until you disconnect it)"

### S2 · OAuth error page

Error description, the machine error code, and the reassurance that nothing was shared. No retry link (the client must restart the flow). Design the variants: invalid client, invalid redirect URI, PKCE failure, expired request, user denied.

---

## 12. Interaction & motion

### 12.1 Motion

Specify duration, easing, and the `prefers-reduced-motion` / Reduce Motion substitution for each:

| Interaction | Notes |
|---|---|
| **Task completion** | The signature animation. Fires dozens of times a day, so it must be satisfying at the first repetition and invisible at the hundredth. Specify the control's transition, the row's departure, and the count decrementing. |
| **Undo** | The row returning. Must clearly be a reversal, not a new arrival. |
| Row insertion (new capture) | Where does it land, and how is it made findable. |
| Section collapse / expand | |
| Detail panel in / out (web) | |
| Sheet presentation (mobile, macOS) | |
| Command palette in / out | Must feel instant — this gates capture speed. |
| Capture chip resolution | The moment typed text becomes a chip: the most delightful opportunity in the app, and the easiest to make annoying. |
| Triage card commit | Per direction. |
| Skeleton → content | No layout jump. |
| Optimistic → confirmed | And optimistic → **rolled back**, which must be legible without being alarming. |
| Count changes | Sidebar and tab badges. |
| Toast in / out | |
| Sync indicator | |
| Page / route transitions | |

**Rule:** no animation may delay an interaction's perceived completion. Optimistic updates are instant; the animation decorates the result.

### 12.2 Gestures

| Platform | Required |
|---|---|
| iOS | Swipe leading/trailing on rows (specify every action per list type), long-press context menu with preview, pull to refresh, triage card swipes in four directions, edge-swipe back, sheet drag-to-dismiss, and where drag-and-drop is offered (given that reordering is not persistable — §3.9) |
| Mobile web | Swipe on rows only. Everything else needs a tap path. |
| macOS | Trackpad two-finger navigation, right-click everywhere, drag-and-drop into the sidebar |

### 12.3 Keyboard

Deliver one table covering web and macOS, marking each shortcut as global / list-focused / detail-focused / triage-mode, and flagging collisions with browser and OS reserved keys. Include a discoverable shortcut reference (`?`).

### 12.4 Haptics (iOS)

Specify the exact type for: task completed, task uncompleted, triage commit, triage skip, capture saved, delete confirmed, error, and the end of a triage pass. Restraint matters more than coverage.

---

## 13. Copy

### 13.1 Voice

Plain, specific, unhurried. Never cheerful about someone's overdue work. Never scolding. Numbers stated exactly. No exclamation marks in error copy.

### 13.2 Copy to write, exactly

1. **Every empty state** — and they are not interchangeable. "You have nothing due today" (good), "Your inbox is clear" (a win), "Nothing matches these filters" (a dead end with an exit), "No tasks yet" (first run, needs teaching), "This context has no projects" (neutral). Enumerate all of them per screen.
2. **All four sign-in refusals** (`W1b`) — the allowlist one especially. It is the most likely first-run failure and must not read as "wrong password".
3. **Every delete confirmation**, with the exact cascade consequence from §3.11. Six of them.
4. **The context-change warning** — "This task will leave the project *Q3 launch*."
5. **Offline, read-only, session-expired and clock-skew banners** — four distinct explanations.
6. **`lead_days`** in plain language, with a worked example.
7. **The token-shown-once moment** — the strongest "you will not see this again" copy in the app.
8. **The 422 field messages** — the server sends them ("must be a YYYY-MM-DD date"). Decide whether to show the server's string or map to friendlier client copy, and specify the mapping if so.
9. **Scope descriptions** for `S1` and `W15f`.
10. **The reward states** — the empty day and the finished triage pass. These carry the product's personality; everything else should get out of the way.

---

## 14. Open engineering questions

Raised by this spec. Design around them and flag where a screen depends on one.

**Resolved since this document was written (2026-07-25).** Eight of the original ten
are closed; the design should assume the resolved behaviour.

1. ~~No sort parameter~~ → **`sort` and `order` implemented.** See the box in §3.5.
   Date-ordered views of any size are buildable; the ordering disclosure in `W7` and
   `W10` is no longer needed.
2. ~~No reverse blocked-by lookup~~ → **`?blocked_by_id=` implemented.** `W8`'s
   Blocking section is directly buildable. `?blocked_by_id=null` lists unblocked
   tasks.
3. ~~No person-merge~~ → **`POST /v1/people/{id}/merge` with `{"into": id}`
   implemented.** Repoints tasks and recurrences, tombstones the duplicate, returns
   `tasks_moved` so the UI can say "3 tasks moved to Marc". Design a merge affordance
   in `W12`. Not reversible — confirm before calling. *Still open: no aliases, so the
   next delegate-by-name can recreate the duplicate.*
4. ~~No endpoint updates the user record~~ → **`PATCH /v1/me` implemented** for
   `name` and `timezone`. Email stays read-only: it is the federated-identity join
   key. `W15a` is buildable. Note that the profile is **not** in the sync feed, so
   re-read `/v1/me` on foreground.
5. **No aggregate counts — and now deliberately so.** Every syncing client already
   holds the full dataset, so sidebar counts and project progress are a local
   computation, not a request. Design them that way. Only a non-syncing client would
   need an endpoint, and `/v1/brief` covers those numbers.
6. ~~`contexts.color` unvalidated~~ → **`#rrggbb` is now enforced** at the API edge.
   Three-digit shorthand and eight-digit alpha are rejected, not normalised. `null`
   means "use the fallback". **The palette itself is still yours to specify.**
7. **Native OAuth client registration — decided: dynamic registration.** The apps
   self-register at first launch and persist the `client_id`. Design consequence: a
   reinstall means a new `client_id`, so **consent is asked again** — `I1`/`M1` should
   not present re-consent as an error. Redirect URI is the app's choice; a
   reversed-domain scheme (`com.moigneu.checkmate:/oauth/callback`) is more reliable
   in `ASWebAuthenticationSession` than loopback.
8. **No push — confirmed, local-only.** Badges and reminders are computed on-device
   from synced data. Say so wherever one is used, and note the limit: nothing fires
   while the app has not synced recently.
9. ~~`/mcp` does not exist~~ → **implemented**, Streamable HTTP with 15 tools.
   `W15f` lists grants for clients that can now actually call something.
10. **No undelete — resolved for tasks only.** `POST /v1/tasks/{id}/restore` brings
    back a task and exactly the subtree deleted with it, so an undo affordance can
    outlive the optimistic window. Contexts, projects, people and recurrences have no
    restore, so **their** confirmations must be firm and must state the cascade.

**Still open, and needing design input rather than engineering:**

11. ~~`active: false` conflates paused and finished~~ → **resolved.** A recurrence
    now carries a derived `state` of `active`, `paused` or `finished`, filterable with
    `?state=`. `W13` can separate the two lists; `W13b` should present `finished` as
    over rather than offering a resume that does nothing. Note the edge: resuming a
    finished series is accepted but only has an effect if the rule changes too, so
    the affordance is "resume and edit the rule", not "resume".

    Series that were already inactive read as `paused`, because nothing recorded why
    they stopped — the forgiving default.

12. ~~`cancelled` has no UI convention~~ → **resolved, and it needs three states in
    the design, not two.** `done` means the work happened; `cancelled` means it was
    decided against; deleted means the record should not exist. All three are
    distinct in the data and now in the API:

    - The brief has a `cancelled_today` bucket alongside `completed_today`, counted
      separately in `totals`. **Cancelled is not progress** — do not fold it into a
      "closed today" number or a completion meter.
    - Cancelled tasks appear in no open bucket, so `cancelled_today` is the only
      place they surface. Design it as part of the collapsed "today's evidence"
      section, distinct from Done.
    - The completion control (§5.2 component 2) already had to expose cancel as
      distinct from complete. It now has a real destination.
    - MCP gained a `cancel_task` tool, so an assistant can decline work without
      deleting it.

**Still open and needing design input:**

13. **Nothing distinguishes a paused series a person will resume from one they have
    abandoned.** `paused` covers both. Say whether that is worth an archive state.

---

## 15. Deliverables

### Phase 1 — direction

1. 2–3 visual directions on `W3`, `W8`, `I3`, light + dark (§4).
2. A written rationale per direction, including the two hard calls: the overdue colour treatment (§4.4) and the brief-bucket overlap model (§3.4.1).

### Phase 2 — system

3. Token file + reference sheet (§5.1).
4. Full component library with every state (§5.2), task row first.
5. The capture component in every state (§6, `W9`).

### Phase 3 — screens

6. Web desktop: `W1`–`W16`, every state.
7. Mobile web: `MW1`–`MW8`.
8. iPhone: `I1`–`I12`, including all widgets.
9. macOS: `M1`–`M7`.
10. Server-rendered: `S1`, `S2`.

### Phase 4 — behaviour

11. Motion spec (§12.1).
12. Gesture and keyboard maps (§12.2–12.3), haptics (§12.4).
13. Complete copy deck (§13.2).
14. Accessibility annotations: focus order, VoiceOver labels for every chip and count, Dynamic Type reflow, contrast verification.
15. Brand: wordmark, app icons (iOS incl. dark/tinted, macOS), favicon, single-glyph mark.

### Priority if scope must be cut

`W3` (Brief) → `W9`/`I7` (Capture) → `W8` (Task detail) → `W4` (Triage) → `I3` (iPhone Brief) → `I8` (Widgets) → `W2` (Shell) → everything else. The Brief and Capture are the product; the rest is support.
