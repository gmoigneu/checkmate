package httpapi_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/nls/checkmate/server/internal/id"
)

func TestRoutineIsSeparatedFromClassicRecurrencesAndBriefBuckets(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)
	today := time.Now().UTC().Format("2006-01-02")

	recurrenceID := h.do(http.MethodPost, "/v1/recurrences", u.Token, map[string]any{
		"kind":       "routine",
		"context_id": contextID,
		"title":      "Plan the day",
		"day_slot":   "morning",
		"slot_order": 10,
		"rrule":      "FREQ=DAILY",
		"starts_on":  today,
	}).expect(http.StatusCreated).id()

	if classic := h.do(http.MethodGet, "/v1/recurrences", u.Token, nil).
		expect(http.StatusOK).list(); len(classic) != 0 {
		t.Fatalf("classic recurrences = %d, want routine excluded", len(classic))
	}

	routines := h.do(http.MethodGet, "/v1/recurrences?kind=routine", u.Token, nil).
		expect(http.StatusOK).list()
	if len(routines) != 1 || routines[0]["id"] != recurrenceID {
		t.Fatalf("routine list = %#v, want the created routine", routines)
	}

	tasks := h.do(http.MethodGet, "/v1/tasks?recurrence_id="+recurrenceID, u.Token, nil).
		expect(http.StatusOK).list()
	if len(tasks) != 1 {
		t.Fatalf("spawned routine tasks = %d, want 1", len(tasks))
	}

	task := tasks[0]
	for field, want := range map[string]any{
		"kind":       "routine",
		"day_slot":   "morning",
		"slot_order": float64(10),
		"due_on":     today,
		"planned_on": today,
	} {
		if got := task[field]; got != want {
			t.Errorf("%s = %#v, want %#v", field, got, want)
		}
	}

	var brief struct {
		Routine []map[string]any `json:"routine"`
		Totals  struct {
			Routine     int `json:"routine"`
			RoutineOpen int `json:"routine_open"`
			DueToday    int `json:"due_today"`
			Planned     int `json:"planned"`
			Overdue     int `json:"overdue"`
		} `json:"totals"`
	}
	h.do(http.MethodGet, "/v1/brief?timezone=UTC", u.Token, nil).
		expect(http.StatusOK).decodeInto(&brief)

	if len(brief.Routine) != 1 || brief.Totals.Routine != 1 || brief.Totals.RoutineOpen != 1 {
		t.Fatalf("routine brief = %#v, totals = %#v", brief.Routine, brief.Totals)
	}
	if brief.Totals.DueToday != 0 || brief.Totals.Planned != 0 || brief.Totals.Overdue != 0 {
		t.Errorf("routine leaked into ordinary totals: %#v", brief.Totals)
	}
}

func TestRegularTaskSlotRequiresAPlannedDate(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)

	res := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "Read the brief", "context_id": contextID, "day_slot": "morning",
	}).expect(http.StatusUnprocessableEntity)
	if res.fields()["day_slot"] == "" {
		t.Fatal("missing day_slot validation error")
	}

	today := time.Now().UTC().Format("2006-01-02")
	task := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title":      "Read the brief",
		"context_id": contextID,
		"planned_on": today,
		"day_slot":   "morning",
	}).expect(http.StatusCreated).decode()
	if task["day_slot"] != "morning" {
		t.Errorf("day_slot = %#v, want morning", task["day_slot"])
	}
}

func TestRoutineRuleIsLimitedToSelectedWeekdays(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)
	today := time.Now().UTC().Format("2006-01-02")

	res := h.do(http.MethodPost, "/v1/recurrences", u.Token, map[string]any{
		"kind":       "routine",
		"context_id": contextID,
		"title":      "Not a weekday routine",
		"day_slot":   "morning",
		"rrule":      "FREQ=MONTHLY;BYMONTHDAY=1",
		"starts_on":  today,
	}).expect(http.StatusUnprocessableEntity)
	if res.fields()["rrule"] == "" {
		t.Fatal("missing routine rrule validation error")
	}
}

func TestRoutineEditReconcilesTodaysOpenOccurrence(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)
	today := time.Now().UTC().Format("2006-01-02")

	recurrenceID := h.do(http.MethodPost, "/v1/recurrences", u.Token, map[string]any{
		"kind":       "routine",
		"context_id": contextID,
		"title":      "Old title",
		"day_slot":   "morning",
		"slot_order": 10,
		"rrule":      "FREQ=DAILY",
		"starts_on":  today,
	}).expect(http.StatusCreated).id()

	before := h.do(http.MethodGet, "/v1/tasks?recurrence_id="+recurrenceID, u.Token, nil).
		expect(http.StatusOK).list()[0]

	h.do(http.MethodPatch, "/v1/recurrences/"+recurrenceID, u.Token, map[string]any{
		"title": "New title", "day_slot": "evening", "slot_order": 30,
	}).expect(http.StatusOK)

	after := h.do(http.MethodGet, "/v1/tasks?recurrence_id="+recurrenceID, u.Token, nil).
		expect(http.StatusOK).list()
	if len(after) != 1 {
		t.Fatalf("tasks after edit = %d, want the same single occurrence", len(after))
	}
	if after[0]["id"] != before["id"] || after[0]["title"] != "New title" ||
		after[0]["day_slot"] != "evening" || after[0]["slot_order"] != float64(30) {
		t.Errorf("reconciled task = %#v", after[0])
	}
}

func TestRoutineRuleRemovingTodayExpiresTheOpenOccurrence(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)
	now := time.Now().UTC()
	today := now.Format("2006-01-02")

	recurrenceID := h.do(http.MethodPost, "/v1/recurrences", u.Token, map[string]any{
		"kind":       "routine",
		"context_id": contextID,
		"title":      "Only on another day",
		"day_slot":   "morning",
		"rrule":      "FREQ=DAILY",
		"starts_on":  today,
	}).expect(http.StatusCreated).id()

	otherDay := weekdayToken(now.AddDate(0, 0, 1).Weekday())
	h.do(http.MethodPatch, "/v1/recurrences/"+recurrenceID, u.Token, map[string]any{
		"rrule": "FREQ=WEEKLY;BYDAY=" + otherDay,
	}).expect(http.StatusOK)

	task := h.do(http.MethodGet, "/v1/tasks?recurrence_id="+recurrenceID, u.Token, nil).
		expect(http.StatusOK).list()[0]
	if task["status"] != "expired" || task["expired_at"] == nil {
		t.Errorf("removed occurrence = %#v, want expired", task)
	}
}

func TestPausingOrDeletingRoutineExpiresTodaysOpenOccurrence(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)
	today := time.Now().UTC().Format("2006-01-02")

	create := func(title string) (string, string) {
		recurrenceID := h.do(http.MethodPost, "/v1/recurrences", u.Token, map[string]any{
			"kind":       "routine",
			"context_id": contextID,
			"title":      title,
			"day_slot":   "morning",
			"rrule":      "FREQ=DAILY",
			"starts_on":  today,
		}).expect(http.StatusCreated).id()
		taskID := h.do(http.MethodGet, "/v1/tasks?recurrence_id="+recurrenceID, u.Token, nil).
			expect(http.StatusOK).list()[0]["id"].(string)

		return recurrenceID, taskID
	}

	pausedRecurrence, pausedTask := create("Pause me")
	h.do(http.MethodPatch, "/v1/recurrences/"+pausedRecurrence, u.Token, map[string]any{
		"active": false,
	}).expect(http.StatusOK)

	deletedRecurrence, deletedTask := create("Delete me")
	h.do(http.MethodDelete, "/v1/recurrences/"+deletedRecurrence, u.Token, nil).
		expect(http.StatusNoContent)

	for _, taskID := range []string{pausedTask, deletedTask} {
		task := h.do(http.MethodGet, "/v1/tasks/"+taskID, u.Token, nil).
			expect(http.StatusOK).decode()
		if task["status"] != "expired" {
			t.Errorf("task %s status = %#v, want expired", taskID, task["status"])
		}
	}
}

func TestExpiredRoutineTaskIsTerminalAndNeverOverdue(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)
	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")

	recurrenceID := h.do(http.MethodPost, "/v1/recurrences", u.Token, map[string]any{
		"kind":       "routine",
		"context_id": contextID,
		"title":      "Yesterday's routine",
		"day_slot":   "morning",
		"rrule":      "FREQ=DAILY",
		"starts_on":  today,
	}).expect(http.StatusCreated).id()
	taskID := h.do(http.MethodGet, "/v1/tasks?recurrence_id="+recurrenceID, u.Token, nil).
		expect(http.StatusOK).list()[0]["id"].(string)

	if _, err := h.store.DB().Exec(
		`UPDATE tasks SET occurrence_on = ?, due_on = ?, planned_on = ? WHERE id = ?`,
		yesterday, yesterday, yesterday, taskID,
	); err != nil {
		t.Fatalf("move occurrence to yesterday: %v", err)
	}

	expired, err := h.store.ExpireRoutineTasks(context.Background(), now)
	if err != nil {
		t.Fatalf("expire routine tasks: %v", err)
	}
	if expired != 1 {
		t.Fatalf("expired = %d, want 1", expired)
	}

	task := h.do(http.MethodGet, "/v1/tasks/"+taskID, u.Token, nil).
		expect(http.StatusOK).decode()
	if task["status"] != "expired" || task["cancelled_at"] != nil {
		t.Errorf("expired task = %#v", task)
	}

	h.do(http.MethodPatch, "/v1/tasks/"+taskID, u.Token, map[string]any{
		"status": "done",
	}).expect(http.StatusUnprocessableEntity)
	h.do(http.MethodDelete, "/v1/tasks/"+taskID, u.Token, nil).
		expect(http.StatusUnprocessableEntity)

	var todayBrief struct {
		Totals struct {
			Overdue int `json:"overdue"`
		} `json:"totals"`
	}
	h.do(http.MethodGet, "/v1/brief?date="+today+"&timezone=UTC", u.Token, nil).
		expect(http.StatusOK).decodeInto(&todayBrief)
	if todayBrief.Totals.Overdue != 0 {
		t.Errorf("overdue = %d, want expired routine excluded", todayBrief.Totals.Overdue)
	}
}

func TestBriefExpiresOnlyTheCallingUsersRoutine(t *testing.T) {
	h := newHarness(t)
	alice := h.user("alice@example.com")
	bob := h.user("bob@example.com")
	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")

	createYesterday := func(user testUser) string {
		recurrenceID := h.do(http.MethodPost, "/v1/recurrences", user.Token, map[string]any{
			"kind":       "routine",
			"context_id": h.firstContextID(user),
			"title":      "Scoped expiration",
			"day_slot":   "morning",
			"rrule":      "FREQ=DAILY",
			"starts_on":  today,
		}).expect(http.StatusCreated).id()
		taskID := h.do(http.MethodGet, "/v1/tasks?recurrence_id="+recurrenceID, user.Token, nil).
			expect(http.StatusOK).list()[0]["id"].(string)

		if _, err := h.store.DB().Exec(
			`UPDATE tasks SET occurrence_on = ?, due_on = ?, planned_on = ? WHERE id = ?`,
			yesterday, yesterday, yesterday, taskID,
		); err != nil {
			t.Fatalf("move occurrence to yesterday: %v", err)
		}

		return taskID
	}

	aliceTask := createYesterday(alice)
	bobTask := createYesterday(bob)

	h.do(http.MethodGet, "/v1/brief?date="+today+"&timezone=UTC", alice.Token, nil).
		expect(http.StatusOK)

	if status := h.do(http.MethodGet, "/v1/tasks/"+aliceTask, alice.Token, nil).
		expect(http.StatusOK).decode()["status"]; status != "expired" {
		t.Errorf("Alice task status = %#v, want expired", status)
	}
	if status := h.do(http.MethodGet, "/v1/tasks/"+bobTask, bob.Token, nil).
		expect(http.StatusOK).decode()["status"]; status != "todo" {
		t.Errorf("Bob task status = %#v, want untouched todo", status)
	}
}

func TestRoutineBriefOutcomeTotalsAreNotCapped(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)
	today := time.Now().UTC().Format("2006-01-02")

	tx, err := h.store.DB().Begin()
	if err != nil {
		t.Fatalf("begin fixtures: %v", err)
	}
	defer tx.Rollback()

	for index := range 101 {
		recurrenceID := id.New()
		if _, err := tx.Exec(`
			INSERT INTO recurrences (
				id, user_id, kind, context_id, title, day_slot, slot_order,
				rrule, timezone, starts_on, active
			) VALUES (?, ?, 'routine', ?, ?, 'morning', ?, 'FREQ=DAILY', 'UTC', ?, 1)`,
			recurrenceID, u.ID, contextID, "Routine item", index, today,
		); err != nil {
			t.Fatalf("insert recurrence %d: %v", index, err)
		}

		status := "todo"
		var expiredAt any
		var completedAt any
		switch index {
		case 99:
			status = "done"
			completedAt = time.Now().UTC().Format(time.RFC3339Nano)
		case 100:
			status = "cancelled"
			expiredAt = time.Now().UTC().Format(time.RFC3339Nano)
		}

		if _, err := tx.Exec(`
			INSERT INTO tasks (
				id, user_id, context_id, capture_method, title, status,
				due_on, planned_on, day_slot, slot_order, recurrence_id,
				occurrence_on, completed_at, expired_at
			) VALUES (?, ?, ?, 'recurrence', ?, ?, ?, ?, 'morning', ?, ?, ?, ?, ?)`,
			id.New(), u.ID, contextID, "Routine item", status, today, today,
			index, recurrenceID, today, completedAt, expiredAt,
		); err != nil {
			t.Fatalf("insert task %d: %v", index, err)
		}
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit fixtures: %v", err)
	}

	var brief struct {
		Routine []map[string]any `json:"routine"`
		Totals  struct {
			Routine        int `json:"routine"`
			RoutineOpen    int `json:"routine_open"`
			RoutineDone    int `json:"routine_done"`
			RoutineExpired int `json:"routine_expired"`
		} `json:"totals"`
	}
	h.do(http.MethodGet, "/v1/brief?date="+today+"&timezone=UTC", u.Token, nil).
		expect(http.StatusOK).decodeInto(&brief)

	if len(brief.Routine) != 100 {
		t.Errorf("routine rows = %d, want display cap 100", len(brief.Routine))
	}
	if brief.Totals.Routine != 101 || brief.Totals.RoutineOpen != 99 ||
		brief.Totals.RoutineDone != 1 || brief.Totals.RoutineExpired != 1 {
		t.Errorf("routine totals = %#v, want exact uncapped outcomes", brief.Totals)
	}
}

func weekdayToken(day time.Weekday) string {
	return [...]string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}[day]
}
