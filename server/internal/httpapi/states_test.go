package httpapi_test

import (
	"net/http"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Recurrence state: paused is not finished
// ---------------------------------------------------------------------------

// TestRecurrenceStatePausedVsFinished is the distinction `active` alone could not
// make. "I turned this off" and "this ran its course" mean opposite things to a
// person: one offers to resume, the other is over.
func TestRecurrenceStatePausedVsFinished(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)

	today := time.Now().UTC().Format("2006-01-02")

	// A live series.
	live := h.do(http.MethodPost, "/v1/recurrences", u.Token, map[string]any{
		"context_id": contextID, "title": "daily", "rrule": "FREQ=DAILY",
		"starts_on": today, "timezone": "UTC",
	}).expect(http.StatusCreated).decode()

	if live["state"] != "active" {
		t.Errorf("state = %v, want active", live["state"])
	}

	if live["completed_at"] != nil {
		t.Errorf("completed_at = %v on a live series, want null", live["completed_at"])
	}

	liveID, _ := live["id"].(string)

	// Paused by a person.
	paused := h.do(http.MethodPatch, "/v1/recurrences/"+liveID, u.Token, map[string]any{
		"active": false,
	}).expect(http.StatusOK).decode()

	if paused["state"] != "paused" {
		t.Errorf("state = %v after the user turned it off, want paused", paused["state"])
	}

	if paused["completed_at"] != nil {
		t.Errorf("completed_at = %v on a paused series, want null; pausing is not "+
			"finishing", paused["completed_at"])
	}

	// A series that runs out. COUNT=1 with a long lead window is exhausted on the
	// first spawner pass, which the create triggers inline.
	finished := h.do(http.MethodPost, "/v1/recurrences", u.Token, map[string]any{
		"context_id": contextID, "title": "one and done",
		"rrule": "FREQ=DAILY;COUNT=1", "starts_on": today, "timezone": "UTC",
		"lead_days": 30,
	}).expect(http.StatusCreated).decode()

	finishedID, _ := finished["id"].(string)

	reread := h.do(http.MethodGet, "/v1/recurrences/"+finishedID, u.Token, nil).
		expect(http.StatusOK).decode()

	if reread["state"] != "finished" {
		t.Fatalf("state = %v after exhausting COUNT=1, want finished", reread["state"])
	}

	if reread["completed_at"] == nil {
		t.Error("completed_at is null on a finished series")
	}

	// Both are inactive, which is exactly why `active` was not enough.
	if reread["active"] != false || paused["active"] != false {
		t.Error("expected both to be inactive")
	}
}

func TestRecurrenceStateFilter(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)

	today := time.Now().UTC().Format("2006-01-02")

	liveID := h.do(http.MethodPost, "/v1/recurrences", u.Token, map[string]any{
		"context_id": contextID, "title": "live", "rrule": "FREQ=DAILY",
		"starts_on": today, "timezone": "UTC",
	}).expect(http.StatusCreated).id()

	pausedID := h.do(http.MethodPost, "/v1/recurrences", u.Token, map[string]any{
		"context_id": contextID, "title": "paused", "rrule": "FREQ=DAILY",
		"starts_on": today, "timezone": "UTC",
	}).expect(http.StatusCreated).id()

	h.do(http.MethodPatch, "/v1/recurrences/"+pausedID, u.Token, map[string]any{
		"active": false,
	}).expect(http.StatusOK)

	h.do(http.MethodPost, "/v1/recurrences", u.Token, map[string]any{
		"context_id": contextID, "title": "spent",
		"rrule": "FREQ=DAILY;COUNT=1", "starts_on": today, "timezone": "UTC",
		"lead_days": 30,
	}).expect(http.StatusCreated)

	for state, wantTitle := range map[string]string{
		"active":   "live",
		"paused":   "paused",
		"finished": "spent",
	} {
		items := h.do(http.MethodGet, "/v1/recurrences?state="+state, u.Token, nil).
			expect(http.StatusOK).list()

		if len(items) != 1 {
			t.Errorf("state=%s returned %d, want 1", state, len(items))

			continue
		}

		if items[0]["title"] != wantTitle {
			t.Errorf("state=%s returned %v, want %q", state, items[0]["title"], wantTitle)
		}
	}

	_ = liveID

	res := h.do(http.MethodGet, "/v1/recurrences?state=nonsense", u.Token, nil)
	res.expect(http.StatusUnprocessableEntity)

	if res.fields()["state"] == "" {
		t.Errorf("error names %v, want an entry for state", res.fields())
	}
}

// TestResumingAFinishedSeriesClearsTheMarker covers the awkward case: resuming a
// spent series is only meaningful alongside changing the rule, so the marker is
// cleared and the spawner re-decides rather than the API refusing.
func TestResumingAFinishedSeriesClearsTheMarker(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)

	today := time.Now().UTC().Format("2006-01-02")

	id := h.do(http.MethodPost, "/v1/recurrences", u.Token, map[string]any{
		"context_id": contextID, "title": "spent", "rrule": "FREQ=DAILY;COUNT=1",
		"starts_on": today, "timezone": "UTC", "lead_days": 30,
	}).expect(http.StatusCreated).id()

	if body := h.do(http.MethodGet, "/v1/recurrences/"+id, u.Token, nil).
		expect(http.StatusOK).decode(); body["state"] != "finished" {
		t.Fatalf("state = %v, want finished", body["state"])
	}

	// Resuming without changing the rule: the spawner runs inline on update, finds
	// nothing left, and retires it again. Finishing twice keeps the first
	// timestamp rather than moving it.
	resumed := h.do(http.MethodPatch, "/v1/recurrences/"+id, u.Token, map[string]any{
		"active": true,
	}).expect(http.StatusOK).decode()

	if resumed["state"] != "finished" {
		t.Errorf("state = %v after resuming a spent rule, want finished again",
			resumed["state"])
	}

	// Resuming *with* a wider rule genuinely revives it.
	revived := h.do(http.MethodPatch, "/v1/recurrences/"+id, u.Token, map[string]any{
		"active": true, "rrule": "FREQ=DAILY",
	}).expect(http.StatusOK).decode()

	if revived["state"] != "active" {
		t.Errorf("state = %v after resuming with an open-ended rule, want active",
			revived["state"])
	}

	if revived["completed_at"] != nil {
		t.Errorf("completed_at = %v on a revived series, want null", revived["completed_at"])
	}
}

// ---------------------------------------------------------------------------
// Cancelled: closed, but not done
// ---------------------------------------------------------------------------

// TestCancelledIsSeparateFromDone pins the convention: cancelling closes a task
// without claiming the work happened, and it must not be counted as progress.
func TestCancelledIsSeparateFromDone(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)

	mk := func(title string) string {
		return h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
			"title": title, "context_id": contextID, "status": "todo",
		}).expect(http.StatusCreated).id()
	}

	doneID := mk("finished it")
	cancelledID := mk("decided against it")
	openID := mk("still to do")

	h.do(http.MethodPatch, "/v1/tasks/"+doneID, u.Token, map[string]any{
		"status": "done",
	}).expect(http.StatusOK)

	cancelled := h.do(http.MethodPatch, "/v1/tasks/"+cancelledID, u.Token, map[string]any{
		"status": "cancelled",
	}).expect(http.StatusOK).decode()

	if cancelled["cancelled_at"] == nil {
		t.Error("cancelled_at is null after cancelling")
	}

	if cancelled["completed_at"] != nil {
		t.Errorf("completed_at = %v on a cancelled task; cancelling is not completing",
			cancelled["completed_at"])
	}

	brief := h.brief(u, "?timezone=UTC")

	if brief.Totals.CompletedToday != 1 {
		t.Errorf("completed_today = %d, want only the finished task",
			brief.Totals.CompletedToday)
	}

	if brief.Totals.CancelledToday != 1 {
		t.Errorf("cancelled_today = %d, want the cancelled task counted separately",
			brief.Totals.CancelledToday)
	}

	// Cancelled work is not pending, so it appears in no open bucket.
	for name, bucket := range map[string][]map[string]any{
		"overdue": brief.Overdue, "due_today": brief.DueToday,
		"planned": brief.Planned, "in_progress": brief.InProgress,
		"blocked": brief.Blocked, "inbox": brief.Inbox,
	} {
		for _, task := range bucket {
			if task["id"] == cancelledID {
				t.Errorf("the cancelled task appears in the %s bucket", name)
			}
		}
	}

	_ = openID
}

// TestCancelledTodayIsListedNotJustCounted is why it is a bucket: cancelled tasks
// appear nowhere else, so without a list there is no way to answer "what did I
// decide against today".
func TestCancelledTodayIsListedNotJustCounted(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	taskID := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "dropped", "context_id": h.firstContextID(u), "status": "todo",
	}).expect(http.StatusCreated).id()

	h.do(http.MethodPatch, "/v1/tasks/"+taskID, u.Token, map[string]any{
		"status": "cancelled",
	}).expect(http.StatusOK)

	raw := h.do(http.MethodGet, "/v1/brief?timezone=UTC", u.Token, nil).
		expect(http.StatusOK).decode()

	list, ok := raw["cancelled_today"].([]any)
	if !ok {
		t.Fatalf("cancelled_today = %v, want an array", raw["cancelled_today"])
	}

	if len(list) != 1 {
		t.Fatalf("cancelled_today has %d entries, want 1", len(list))
	}

	entry, _ := list[0].(map[string]any)
	if entry["id"] != taskID {
		t.Errorf("cancelled_today lists %v, want %v", entry["id"], taskID)
	}

	// Empty is an array, not null, like every other bucket.
	other := h.user("other@example.com")

	emptyBrief := h.do(http.MethodGet, "/v1/brief?timezone=UTC", other.Token, nil).
		expect(http.StatusOK).decode()

	if emptyBrief["cancelled_today"] == nil {
		t.Error("cancelled_today is null on an empty account, want an empty array")
	}
}

// TestMCPCancelTask covers the tool: a model needs a named verb for "not doing
// this", or it reaches for delete and destroys the record instead.
func TestMCPCancelTask(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	h.initialize(u.Token)

	created := structured(t, h.callTool(u.Token, "create_task", map[string]any{
		"title": "not going to happen",
	}))

	task, _ := created["task"].(map[string]any)
	taskID, _ := task["id"].(string)

	result := h.callTool(u.Token, "cancel_task", map[string]any{"task_id": taskID})
	out := structured(t, result)

	task, _ = out["task"].(map[string]any)
	if task["status"] != "cancelled" {
		t.Errorf("status = %v, want cancelled", task["status"])
	}

	if text := resultText(result); text == "" {
		t.Error("no summary text on the cancel result")
	}

	// It leaves the open list, without being deleted.
	open := structured(t, h.callTool(u.Token, "list_tasks", map[string]any{}))
	if count, _ := open["count"].(float64); count != 0 {
		t.Errorf("open list has %v tasks after cancelling, want 0", open["count"])
	}

	closed := structured(t, h.callTool(u.Token, "list_tasks",
		map[string]any{"include_closed": true}))

	if count, _ := closed["count"].(float64); count != 1 {
		t.Errorf("include_closed returned %v, want the cancelled task", closed["count"])
	}

	// And the brief separates it from completed work.
	brief := structured(t, h.callTool(u.Token, "daily_brief", map[string]any{}))

	totals, _ := brief["totals"].(map[string]any)
	if got, _ := totals["cancelled_today"].(float64); got != 1 {
		t.Errorf("brief cancelled_today = %v, want 1", totals["cancelled_today"])
	}

	if got, _ := totals["completed_today"].(float64); got != 0 {
		t.Errorf("brief completed_today = %v, want 0; cancelling is not progress",
			totals["completed_today"])
	}
}

// TestMCPRecurrenceStateVisible checks a model can tell a spent series from a
// paused one, so it does not offer to resume something that is over.
func TestMCPRecurrenceStateVisible(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	h.initialize(u.Token)

	contexts := structured(t, h.callTool(u.Token, "list_contexts", map[string]any{}))
	list, _ := contexts["contexts"].([]any)
	first, _ := list[0].(map[string]any)
	contextID, _ := first["id"].(string)

	today := time.Now().UTC().Format("2006-01-02")

	structured(t, h.callTool(u.Token, "create_recurrence", map[string]any{
		"title": "one and done", "context_id": contextID,
		"rrule": "FREQ=DAILY;COUNT=1", "starts_on": today,
		"timezone": "UTC", "lead_days": 30,
	}))

	out := structured(t, h.callTool(u.Token, "list_recurrences", map[string]any{}))

	items, _ := out["recurrences"].([]any)
	if len(items) != 1 {
		t.Fatalf("recurrences = %d, want 1", len(items))
	}

	entry, _ := items[0].(map[string]any)
	if entry["state"] != "finished" {
		t.Errorf("state = %v, want finished", entry["state"])
	}
}
