package httpapi_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// syncPage is one decoded /v1/sync response.
type syncPage struct {
	Cursor  int64 `json:"cursor"`
	HasMore bool  `json:"has_more"`
	Changes struct {
		Contexts    []map[string]any `json:"contexts"`
		Projects    []map[string]any `json:"projects"`
		People      []map[string]any `json:"people"`
		Recurrences []map[string]any `json:"recurrences"`
		Tasks       []map[string]any `json:"tasks"`
	} `json:"changes"`
	Sources    []map[string]any `json:"sources"`
	ServerTime string           `json:"server_time"`
}

func (h *harness) sync(u testUser, query string) syncPage {
	h.t.Helper()

	var page syncPage

	h.do(http.MethodGet, "/v1/sync"+query, u.Token, nil).
		expect(http.StatusOK).decodeInto(&page)

	return page
}

func TestSyncFullThenIncremental(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	// A full sync from zero carries the seeded contexts and the static source
	// lookup, which a fresh client needs before it can render anything.
	full := h.sync(u, "?since=0")

	if len(full.Changes.Contexts) != 4 {
		t.Errorf("full sync returned %d contexts, want 4", len(full.Changes.Contexts))
	}

	if len(full.Sources) != 6 {
		t.Errorf("full sync returned %d sources, want the 6 seeded", len(full.Sources))
	}

	if full.Cursor == 0 {
		t.Error("cursor is still 0 after a full sync")
	}

	if full.ServerTime == "" {
		t.Error("no server_time in the response")
	}

	// Nothing has changed, so the next call is empty and the cursor holds.
	empty := h.sync(u, fmt.Sprintf("?since=%d", full.Cursor))

	if len(empty.Changes.Tasks) != 0 || len(empty.Changes.Contexts) != 0 {
		t.Errorf("an idle sync returned changes: %+v", empty.Changes)
	}

	if empty.Cursor != full.Cursor {
		t.Errorf("cursor moved from %d to %d with no changes", full.Cursor, empty.Cursor)
	}

	// Sources are only sent on a full sync; they have no rev to compare.
	if len(empty.Sources) != 0 {
		t.Errorf("an incremental sync resent %d sources", len(empty.Sources))
	}

	// Empty collections must be [] and not null, or a client iterating them
	// crashes on a quiet sync.
	raw := h.do(http.MethodGet, "/v1/sync?since=0", u.Token, nil).
		expect(http.StatusOK).decode()

	changes, ok := raw["changes"].(map[string]any)
	if !ok {
		t.Fatalf("changes is %T, want an object", raw["changes"])
	}

	for _, collection := range []string{"contexts", "projects", "people", "recurrences", "tasks"} {
		if changes[collection] == nil {
			t.Errorf("changes.%s is null, want an empty array", collection)
		}
	}

	// One new task shows up, and nothing else does.
	taskID := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "a new task", "context_id": h.firstContextID(u),
	}).expect(http.StatusCreated).id()

	delta := h.sync(u, fmt.Sprintf("?since=%d", empty.Cursor))

	if len(delta.Changes.Tasks) != 1 {
		t.Fatalf("delta returned %d tasks, want 1", len(delta.Changes.Tasks))
	}

	if delta.Changes.Tasks[0]["id"] != taskID {
		t.Errorf("delta task id = %v, want %v", delta.Changes.Tasks[0]["id"], taskID)
	}

	if len(delta.Changes.Contexts) != 0 {
		t.Errorf("delta resent %d unchanged contexts", len(delta.Changes.Contexts))
	}
}

// TestSyncCarriesTombstones is why deletes are soft: a client that only ever saw
// live rows could never learn that something was removed.
func TestSyncCarriesTombstones(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	taskID := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "will be deleted", "context_id": h.firstContextID(u),
	}).expect(http.StatusCreated).id()

	afterCreate := h.sync(u, "?since=0").Cursor

	h.do(http.MethodDelete, "/v1/tasks/"+taskID, u.Token, nil).expect(http.StatusNoContent)

	delta := h.sync(u, fmt.Sprintf("?since=%d", afterCreate))

	if len(delta.Changes.Tasks) != 1 {
		t.Fatalf("delta returned %d tasks after a delete, want 1 tombstone", len(delta.Changes.Tasks))
	}

	if delta.Changes.Tasks[0]["deleted_at"] == nil {
		t.Error("the tombstone has no deleted_at, so a client cannot tell it was deleted")
	}
}

func TestSyncIsScopedToOwner(t *testing.T) {
	h := newHarness(t)
	alice := h.user("alice@example.com")
	bob := h.user("bob@example.com")

	h.do(http.MethodPost, "/v1/tasks", alice.Token, map[string]any{
		"title": "alice's secret", "context_id": h.firstContextID(alice),
	}).expect(http.StatusCreated)

	page := h.sync(bob, "?since=0")

	for _, task := range page.Changes.Tasks {
		if task["title"] == "alice's secret" {
			t.Fatal("bob's sync feed contains alice's task")
		}
	}

	// Bob still gets his own four contexts, so the feed is scoped rather than
	// broken.
	if len(page.Changes.Contexts) != 4 {
		t.Errorf("bob sees %d contexts, want his own 4", len(page.Changes.Contexts))
	}
}

// TestSyncPaginationLosesNothing is the property the cursor arithmetic exists
// for. The rev counter is global, so a naive per-table limit would let a table
// with many changes have its tail skipped: the client would advance past rows it
// never received.
func TestSyncPaginationLosesNothing(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)

	const total = 25

	created := map[string]bool{}

	for i := range total {
		id := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
			"title": fmt.Sprintf("task %d", i), "context_id": contextID,
		}).expect(http.StatusCreated).id()

		created[id] = true
	}

	// Also churn some people, so more than one table has pending changes and the
	// cursor has to be pulled back to the safe minimum.
	for i := range 5 {
		h.do(http.MethodPost, "/v1/people", u.Token, map[string]any{
			"name": fmt.Sprintf("Person %d", i),
		}).expect(http.StatusCreated)
	}

	seen := map[string]bool{}
	cursor := int64(0)

	for range 40 { // generous bound: a paging bug should fail, not spin
		page := h.sync(u, fmt.Sprintf("?since=%d&limit=3", cursor))

		for _, task := range page.Changes.Tasks {
			id, _ := task["id"].(string)

			if seen[id] {
				t.Errorf("task %s was delivered twice", id)
			}

			seen[id] = true
		}

		if page.Cursor < cursor {
			t.Fatalf("cursor went backwards: %d then %d", cursor, page.Cursor)
		}

		if !page.HasMore {
			cursor = page.Cursor

			break
		}

		if page.Cursor == cursor {
			t.Fatalf("cursor stuck at %d with has_more set; this would loop forever", cursor)
		}

		cursor = page.Cursor
	}

	if len(seen) != total {
		t.Errorf("paging delivered %d of %d tasks; the rest would never be requested again",
			len(seen), total)
	}

	for id := range created {
		if !seen[id] {
			t.Errorf("task %s was never delivered", id)
		}
	}
}

func TestSyncRejectsBadCursors(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	for field, query := range map[string]string{
		"since": "?since=-1",
		"limit": "?limit=0",
	} {
		res := h.do(http.MethodGet, "/v1/sync"+query, u.Token, nil)
		res.expect(http.StatusUnprocessableEntity)

		if res.fields()[field] == "" {
			t.Errorf("%s: error names %v, want an entry for %s", query, res.fields(), field)
		}
	}

	// A cursor beyond anything issued is not an error; it simply has no changes.
	page := h.sync(u, "?since=999999")
	if len(page.Changes.Tasks) != 0 {
		t.Error("a future cursor returned changes")
	}
}

func TestSyncSeesUpdatesNotJustCreates(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	taskID := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "before", "context_id": h.firstContextID(u),
	}).expect(http.StatusCreated).id()

	cursor := h.sync(u, "?since=0").Cursor

	h.do(http.MethodPatch, "/v1/tasks/"+taskID, u.Token, map[string]any{
		"title": "after",
	}).expect(http.StatusOK)

	delta := h.sync(u, fmt.Sprintf("?since=%d", cursor))

	if len(delta.Changes.Tasks) != 1 {
		t.Fatalf("delta returned %d tasks after an update, want 1", len(delta.Changes.Tasks))
	}

	if delta.Changes.Tasks[0]["title"] != "after" {
		t.Errorf("delta carries title %v, want the updated value", delta.Changes.Tasks[0]["title"])
	}
}

// ---------------------------------------------------------------------------
// Recurrence spawning through the API
// ---------------------------------------------------------------------------

// TestCreatingARecurrenceSpawnsImmediately covers the inline spawn: without it,
// creating a daily recurrence shows nothing until the scheduler ticks, which
// reads as the feature being broken.
func TestCreatingARecurrenceSpawnsImmediately(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	today := time.Now().UTC().Format("2006-01-02")

	recurrenceID := h.do(http.MethodPost, "/v1/recurrences", u.Token, map[string]any{
		"context_id": h.firstContextID(u),
		"title":      "Daily standup",
		"rrule":      "FREQ=DAILY",
		"starts_on":  today,
		"timezone":   "UTC",
	}).expect(http.StatusCreated).id()

	// The occurrence is queryable in the very next request.
	items := h.do(http.MethodGet, "/v1/tasks?recurrence_id="+recurrenceID, u.Token, nil).
		expect(http.StatusOK).list()

	if len(items) != 1 {
		t.Fatalf("tasks for the new recurrence = %d, want 1 spawned immediately", len(items))
	}

	task := items[0]

	if task["kind"] != "recurring" {
		t.Errorf("kind = %v, want recurring", task["kind"])
	}

	if task["capture_method"] != "recurrence" {
		t.Errorf("capture_method = %v, want recurrence", task["capture_method"])
	}

	if task["occurrence_on"] != today {
		t.Errorf("occurrence_on = %v, want today (%s)", task["occurrence_on"], today)
	}

	if task["due_on"] != today {
		t.Errorf("due_on = %v, want today so it lands in the brief", task["due_on"])
	}

	// And it shows up in today's brief as due.
	brief := h.brief(u, "?timezone=UTC")
	if brief.Totals.DueToday != 1 {
		t.Errorf("brief due_today = %d, want the spawned occurrence", brief.Totals.DueToday)
	}
}

// TestSpawnedOccurrencesAreOwnedByTheTemplateOwner checks the one place the
// spawner steps outside the per-user store convention: it walks every account, so
// the rows it writes must take their user_id from the template.
func TestSpawnedOccurrencesAreOwnedByTheTemplateOwner(t *testing.T) {
	h := newHarness(t)
	alice := h.user("alice@example.com")
	bob := h.user("bob@example.com")

	today := time.Now().UTC().Format("2006-01-02")

	h.do(http.MethodPost, "/v1/recurrences", alice.Token, map[string]any{
		"context_id": h.firstContextID(alice),
		"title":      "Alice's standup",
		"rrule":      "FREQ=DAILY",
		"starts_on":  today,
		"timezone":   "UTC",
	}).expect(http.StatusCreated)

	if items := h.do(http.MethodGet, "/v1/tasks", bob.Token, nil).
		expect(http.StatusOK).list(); len(items) != 0 {
		t.Errorf("bob sees %d tasks spawned from alice's template, want 0", len(items))
	}

	if items := h.do(http.MethodGet, "/v1/tasks", alice.Token, nil).
		expect(http.StatusOK).list(); len(items) != 1 {
		t.Errorf("alice sees %d of her own spawned tasks, want 1", len(items))
	}
}

func TestDeactivatingARecurrenceStopsSpawning(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	today := time.Now().UTC().Format("2006-01-02")

	recurrenceID := h.do(http.MethodPost, "/v1/recurrences", u.Token, map[string]any{
		"context_id": h.firstContextID(u),
		"title":      "Pausable",
		"rrule":      "FREQ=DAILY",
		"starts_on":  today,
		"timezone":   "UTC",
		"active":     false,
	}).expect(http.StatusCreated).id()

	if items := h.do(http.MethodGet, "/v1/tasks?recurrence_id="+recurrenceID, u.Token, nil).
		expect(http.StatusOK).list(); len(items) != 0 {
		t.Errorf("an inactive template spawned %d tasks, want 0", len(items))
	}

	// Activating it makes the occurrence appear.
	h.do(http.MethodPatch, "/v1/recurrences/"+recurrenceID, u.Token, map[string]any{
		"active": true,
	}).expect(http.StatusOK)

	if items := h.do(http.MethodGet, "/v1/tasks?recurrence_id="+recurrenceID, u.Token, nil).
		expect(http.StatusOK).list(); len(items) != 1 {
		t.Errorf("after activating, tasks = %d, want 1", len(items))
	}
}

// ---------------------------------------------------------------------------
// Daily brief
// ---------------------------------------------------------------------------

type briefResponse struct {
	Date           string           `json:"date"`
	Timezone       string           `json:"timezone"`
	Overdue        []map[string]any `json:"overdue"`
	DueToday       []map[string]any `json:"due_today"`
	Planned        []map[string]any `json:"planned"`
	InProgress     []map[string]any `json:"in_progress"`
	Inbox          []map[string]any `json:"inbox"`
	Blocked        []map[string]any `json:"blocked"`
	CompletedToday []map[string]any `json:"completed_today"`
	CancelledToday []map[string]any `json:"cancelled_today"`
	WaitingOn      []struct {
		PersonID   string           `json:"person_id"`
		PersonName string           `json:"person_name"`
		Tasks      []map[string]any `json:"tasks"`
	} `json:"waiting_on"`
	Totals struct {
		Overdue                int `json:"overdue"`
		DueToday               int `json:"due_today"`
		Planned                int `json:"planned"`
		Inbox                  int `json:"inbox"`
		Blocked                int `json:"blocked"`
		WaitingOn              int `json:"waiting_on"`
		InProgress             int `json:"in_progress"`
		CompletedToday         int `json:"completed_today"`
		CancelledToday         int `json:"cancelled_today"`
		PlannedMinutes         int `json:"planned_minutes"`
		PlannedWithoutEstimate int `json:"planned_without_estimate"`
	} `json:"totals"`
}

func (h *harness) brief(u testUser, query string) briefResponse {
	h.t.Helper()

	var out briefResponse

	h.do(http.MethodGet, "/v1/brief"+query, u.Token, nil).
		expect(http.StatusOK).decodeInto(&out)

	return out
}

func TestBriefBuckets(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)

	today := time.Now().UTC().Format("2006-01-02")
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")

	mk := func(body map[string]any) string {
		body["context_id"] = contextID

		return h.do(http.MethodPost, "/v1/tasks", u.Token, body).
			expect(http.StatusCreated).id()
	}

	mk(map[string]any{"title": "late", "due_on": yesterday, "status": "todo"})
	mk(map[string]any{"title": "due today", "due_on": today, "status": "todo"})
	mk(map[string]any{"title": "due tomorrow", "due_on": tomorrow, "status": "todo"})
	mk(map[string]any{
		"title": "planned today", "planned_on": today,
		"status": "todo", "estimate_minutes": 45,
	})
	mk(map[string]any{"title": "planned, unestimated", "planned_on": today, "status": "todo"})
	mk(map[string]any{"title": "working on it", "status": "in_progress"})

	// An untriaged capture, which has no context by definition.
	h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "untriaged", "capture_method": "voice",
	}).expect(http.StatusCreated)

	blocker := mk(map[string]any{"title": "the blocker", "status": "todo"})
	blocked := mk(map[string]any{"title": "waiting on the blocker", "status": "todo"})

	h.do(http.MethodPatch, "/v1/tasks/"+blocked, u.Token, map[string]any{
		"blocked_by_id": blocker, "status": "blocked",
	}).expect(http.StatusOK)

	delegated := mk(map[string]any{"title": "chase Marc", "status": "todo"})
	h.do(http.MethodPatch, "/v1/tasks/"+delegated, u.Token, map[string]any{
		"delegated_to": nil,
	}).expect(http.StatusBadRequest) // delegated_to is a create-only convenience

	personID := h.do(http.MethodPost, "/v1/people", u.Token, map[string]any{
		"name": "Marc",
	}).expect(http.StatusCreated).id()

	h.do(http.MethodPatch, "/v1/tasks/"+delegated, u.Token, map[string]any{
		"delegated_to_id": personID, "status": "delegated",
	}).expect(http.StatusOK)

	finished := mk(map[string]any{"title": "already done", "status": "todo"})
	h.do(http.MethodPatch, "/v1/tasks/"+finished, u.Token, map[string]any{
		"status": "done",
	}).expect(http.StatusOK)

	brief := h.brief(u, "?timezone=UTC")

	if brief.Date != today {
		t.Errorf("date = %q, want today (%q)", brief.Date, today)
	}

	checks := map[string]struct{ got, want int }{
		"overdue":          {brief.Totals.Overdue, 1},
		"due today":        {brief.Totals.DueToday, 1},
		"planned":          {brief.Totals.Planned, 2},
		"in progress":      {brief.Totals.InProgress, 1},
		"inbox":            {brief.Totals.Inbox, 1},
		"blocked":          {brief.Totals.Blocked, 1},
		"waiting on":       {brief.Totals.WaitingOn, 1},
		"completed today":  {brief.Totals.CompletedToday, 1},
		"planned minutes":  {brief.Totals.PlannedMinutes, 45},
		"planned unknowns": {brief.Totals.PlannedWithoutEstimate, 1},
	}

	for label, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", label, c.got, c.want)
		}
	}

	if len(brief.Overdue) != 1 || brief.Overdue[0]["title"] != "late" {
		t.Errorf("overdue = %v, want the one late task", brief.Overdue)
	}

	// A task due tomorrow belongs in neither the overdue nor the due-today list.
	for _, task := range append(brief.Overdue, brief.DueToday...) {
		if task["title"] == "due tomorrow" {
			t.Error("a task due tomorrow leaked into today's brief")
		}
	}

	if len(brief.WaitingOn) != 1 {
		t.Fatalf("waiting_on = %v, want one group", brief.WaitingOn)
	}

	if brief.WaitingOn[0].PersonName != "Marc" {
		t.Errorf("waiting_on person = %q, want Marc", brief.WaitingOn[0].PersonName)
	}
}

// TestBriefUsesTheCallersTimezone matters because dates are stored without a
// zone: the server's own clock would give someone in Paris the wrong day for the
// first hours of it.
func TestBriefUsesTheCallersTimezone(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	utcToday := time.Now().UTC().Format("2006-01-02")

	// Kiritimati is UTC+14, so its date is ahead of UTC for part of every day.
	brief := h.brief(u, "?timezone=Pacific/Kiritimati")

	if brief.Timezone != "Pacific/Kiritimati" {
		t.Errorf("timezone = %q, want it echoed back", brief.Timezone)
	}

	kiritimati, err := time.LoadLocation("Pacific/Kiritimati")
	if err != nil {
		t.Skip("timezone database unavailable")
	}

	wantDate := time.Now().In(kiritimati).Format("2006-01-02")
	if brief.Date != wantDate {
		t.Errorf("date = %q, want %q (the caller's today, not the server's %q)",
			brief.Date, wantDate, utcToday)
	}
}

func TestBriefExplicitDateAndContext(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	contexts := h.do(http.MethodGet, "/v1/contexts", u.Token, nil).expect(http.StatusOK).list()
	first, _ := contexts[0]["id"].(string)
	second, _ := contexts[1]["id"].(string)

	const day = "2026-08-15"

	h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "in the first context", "context_id": first, "planned_on": day, "status": "todo",
	}).expect(http.StatusCreated)

	h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "in the second context", "context_id": second, "planned_on": day, "status": "todo",
	}).expect(http.StatusCreated)

	all := h.brief(u, "?date="+day+"&timezone=UTC")
	if all.Totals.Planned != 2 {
		t.Errorf("planned = %d across all contexts, want 2", all.Totals.Planned)
	}

	scoped := h.brief(u, "?date="+day+"&timezone=UTC&context_id="+first)
	if scoped.Totals.Planned != 1 {
		t.Errorf("planned = %d for one context, want 1", scoped.Totals.Planned)
	}

	if len(scoped.Planned) != 1 || scoped.Planned[0]["title"] != "in the first context" {
		t.Errorf("scoped brief returned %v", scoped.Planned)
	}
}

func TestBriefRejectsBadParameters(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	for field, query := range map[string]string{
		"timezone": "?timezone=Mars/Olympus",
		"date":     "?date=2026-02-31",
	} {
		res := h.do(http.MethodGet, "/v1/brief"+query, u.Token, nil)
		res.expect(http.StatusUnprocessableEntity)

		if res.fields()[field] == "" {
			t.Errorf("%s: error names %v, want an entry for %s", query, res.fields(), field)
		}
	}
}

// TestBriefTotalsAreNotCappedByTheList pins a fix: the totals used to be
// len() of the returned slice, which was itself capped. Someone with three
// hundred overdue tasks would have been told they had a hundred, and the number
// in a brief is the part people act on.
func TestBriefTotalsAreNotCappedByTheList(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)

	// Comfortably past the 100-item display cap.
	const overdueCount = 105

	for i := range overdueCount {
		h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
			"title": fmt.Sprintf("overdue %d", i), "context_id": contextID,
			"due_on": "2020-01-01", "status": "todo",
			"planned_on": time.Now().UTC().Format("2006-01-02"),
			// Every other task carries an estimate, so the summed minutes and
			// the unestimated count both have to come from the whole set.
			"estimate_minutes": 10,
		}).expect(http.StatusCreated)
	}

	h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "planned, no estimate", "context_id": contextID,
		"planned_on": time.Now().UTC().Format("2006-01-02"), "status": "todo",
	}).expect(http.StatusCreated)

	brief := h.brief(u, "?timezone=UTC")

	if brief.Totals.Overdue != overdueCount {
		t.Errorf("totals.overdue = %d, want %d", brief.Totals.Overdue, overdueCount)
	}

	if len(brief.Overdue) != 100 {
		t.Errorf("overdue list = %d items, want it capped at 100", len(brief.Overdue))
	}

	if want := overdueCount * 10; brief.Totals.PlannedMinutes != want {
		t.Errorf("planned_minutes = %d, want %d summed over the whole bucket",
			brief.Totals.PlannedMinutes, want)
	}

	if brief.Totals.PlannedWithoutEstimate != 1 {
		t.Errorf("planned_without_estimate = %d, want 1", brief.Totals.PlannedWithoutEstimate)
	}
}

func TestBriefIsScopedToOwner(t *testing.T) {
	h := newHarness(t)
	alice := h.user("alice@example.com")
	bob := h.user("bob@example.com")

	h.do(http.MethodPost, "/v1/tasks", alice.Token, map[string]any{
		"title": "alice's overdue thing", "context_id": h.firstContextID(alice),
		"due_on": "2020-01-01", "status": "todo",
	}).expect(http.StatusCreated)

	brief := h.brief(bob, "?timezone=UTC")

	if brief.Totals.Overdue != 0 {
		t.Errorf("bob's brief shows %d overdue tasks, want 0", brief.Totals.Overdue)
	}
}

func TestBriefEmptyAccountReturnsEmptyLists(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	// JSON nulls where a client expects an array are a common source of client
	// crashes, so every bucket is an empty array instead.
	res := h.do(http.MethodGet, "/v1/brief?timezone=UTC", u.Token, nil).
		expect(http.StatusOK).decode()

	for _, field := range []string{
		"overdue", "due_today", "planned", "in_progress", "inbox", "blocked",
		"waiting_on", "completed_today",
	} {
		if res[field] == nil {
			t.Errorf("%s is null, want an empty array", field)
		}
	}
}
