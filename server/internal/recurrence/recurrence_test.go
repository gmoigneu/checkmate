package recurrence

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/nls/checkmate/server/internal/account"
	"github.com/nls/checkmate/server/internal/database"
	"github.com/nls/checkmate/server/internal/patch"
	"github.com/nls/checkmate/server/internal/store"
)

// fixedNow is the instant every test pretends it is running at: a Saturday, so a
// weekly Monday rule has an unambiguous next occurrence.
var fixedNow = time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)

type fixture struct {
	t       *testing.T
	store   *store.Store
	spawner *Spawner
	userID  string
	ctxID   string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	ctx := context.Background()

	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}

	t.Cleanup(func() { db.Close() })

	if err := database.Migrate(ctx, db, nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	st := store.New(db)

	user, err := account.CreateUser(ctx, db, "you@example.com", "You", "UTC")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	createdContext, err := st.CreateContext(ctx, user.ID, store.ContextCreate{Name: "Recurrence test"})
	if err != nil {
		t.Fatalf("create fixture context: %v", err)
	}

	spawner := New(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	spawner.now = func() time.Time { return fixedNow }

	return &fixture{t: t, store: st, spawner: spawner, userID: user.ID, ctxID: createdContext.ID}
}

// recurrenceOpts are the knobs a test varies.
type recurrenceOpts struct {
	RRule     string
	StartsOn  string
	EndsOn    string
	Timezone  string
	LeadDays  int64
	Delegated *string
}

func (f *fixture) createRecurrence(o recurrenceOpts) string {
	f.t.Helper()

	if o.Timezone == "" {
		o.Timezone = "UTC"
	}

	var leadDays = o.LeadDays

	created, err := f.store.CreateRecurrence(context.Background(), f.userID, store.RecurrenceCreate{
		ContextID:     f.ctxID,
		Title:         "Recurring thing",
		RRule:         o.RRule,
		Timezone:      o.Timezone,
		StartsOn:      o.StartsOn,
		LeadDays:      &leadDays,
		DelegatedToID: o.Delegated,
	})
	if err != nil {
		f.t.Fatalf("create recurrence: %v", err)
	}

	if o.EndsOn != "" {
		if _, err := f.store.DB().Exec(
			`UPDATE recurrences SET ends_on = ? WHERE id = ?`, o.EndsOn, created.ID,
		); err != nil {
			f.t.Fatalf("set ends_on: %v", err)
		}
	}

	return created.ID
}

// occurrences lists the dates spawned for a series, in order.
func (f *fixture) occurrences(recurrenceID string) []string {
	f.t.Helper()

	rows, err := f.store.DB().Query(
		`SELECT occurrence_on FROM tasks
		 WHERE recurrence_id = ? AND deleted_at IS NULL ORDER BY occurrence_on`,
		recurrenceID)
	if err != nil {
		f.t.Fatalf("list occurrences: %v", err)
	}
	defer rows.Close()

	var out []string

	for rows.Next() {
		var date string
		if err := rows.Scan(&date); err != nil {
			f.t.Fatalf("scan occurrence: %v", err)
		}

		out = append(out, date)
	}

	return out
}

func (f *fixture) run() Result {
	f.t.Helper()

	result, err := f.spawner.Run(context.Background())
	if err != nil {
		f.t.Fatalf("spawner run: %v", err)
	}

	return result
}

// state reads back the template's cursor and active flag.
func (f *fixture) state(recurrenceID string) (nextOn string, lastSpawned string, active bool) {
	f.t.Helper()

	var (
		next, last sql.NullString
		activeInt  int
	)

	err := f.store.DB().QueryRow(
		`SELECT next_occurrence_on, last_spawned_on, active FROM recurrences WHERE id = ?`,
		recurrenceID,
	).Scan(&next, &last, &activeInt)
	if err != nil {
		f.t.Fatalf("read recurrence state: %v", err)
	}

	return next.String, last.String, activeInt != 0
}

// ---------------------------------------------------------------------------

func TestSpawnsDailyWithinTheLeadWindow(t *testing.T) {
	f := newFixture(t)

	// Starting today, three days of lead time: today plus the next three.
	id := f.createRecurrence(recurrenceOpts{
		RRule: "FREQ=DAILY", StartsOn: "2026-07-25", LeadDays: 3,
	})

	result := f.run()

	if result.Created != 4 {
		t.Errorf("created = %d, want 4 (today plus three days of lead)", result.Created)
	}

	want := []string{"2026-07-25", "2026-07-26", "2026-07-27", "2026-07-28"}
	got := f.occurrences(id)

	if len(got) != len(want) {
		t.Fatalf("occurrences = %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("occurrence[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// The cursor is parked on the first date beyond the window, so the next pass
	// picks up exactly where this one stopped.
	nextOn, lastSpawned, active := f.state(id)

	if nextOn != "2026-07-29" {
		t.Errorf("next_occurrence_on = %q, want 2026-07-29", nextOn)
	}

	if lastSpawned != "2026-07-28" {
		t.Errorf("last_spawned_on = %q, want 2026-07-28", lastSpawned)
	}

	if !active {
		t.Error("an open-ended daily series was deactivated")
	}
}

func TestSpawnsOnlyTodayWithNoLeadTime(t *testing.T) {
	f := newFixture(t)

	id := f.createRecurrence(recurrenceOpts{RRule: "FREQ=DAILY", StartsOn: "2026-07-25"})

	if result := f.run(); result.Created != 1 {
		t.Errorf("created = %d, want 1 with no lead time", result.Created)
	}

	if got := f.occurrences(id); len(got) != 1 || got[0] != "2026-07-25" {
		t.Errorf("occurrences = %v, want just today", got)
	}
}

// TestSpawningIsIdempotent is the property that lets the spawner run on a short
// tick, at boot, and from cron at the same time.
func TestSpawningIsIdempotent(t *testing.T) {
	f := newFixture(t)

	id := f.createRecurrence(recurrenceOpts{
		RRule: "FREQ=DAILY", StartsOn: "2026-07-25", LeadDays: 2,
	})

	first := f.run()
	if first.Created != 3 {
		t.Fatalf("first pass created %d, want 3", first.Created)
	}

	before := f.occurrences(id)

	// Three more passes at the same instant must change nothing.
	for i := range 3 {
		result := f.run()

		if result.Created != 0 {
			t.Errorf("pass %d created %d rows, want 0", i+2, result.Created)
		}
	}

	after := f.occurrences(id)

	if len(after) != len(before) {
		t.Errorf("occurrences grew from %d to %d across repeated passes", len(before), len(after))
	}
}

// TestCatchUpIsBounded covers the outage case: a series that has been due for a
// month must not produce a month of tasks.
func TestCatchUpIsBounded(t *testing.T) {
	f := newFixture(t)

	// Started 30 days ago and never spawned.
	id := f.createRecurrence(recurrenceOpts{
		RRule: "FREQ=DAILY", StartsOn: "2026-06-25",
	})

	result := f.run()

	got := f.occurrences(id)

	// Only the catch-up window plus today, not all 31 days.
	if len(got) > CatchUpDays+1 {
		t.Errorf("created %d occurrences (%v), want at most %d; a long outage should not "+
			"flood the list", len(got), got, CatchUpDays+1)
	}

	if result.Missed == 0 {
		t.Error("nothing reported as missed, but occurrences older than the window were skipped")
	}

	// Nothing older than the window survived.
	earliest := fixedNow.AddDate(0, 0, -CatchUpDays).Format(database.DateOnly)

	for _, date := range got {
		if date < earliest {
			t.Errorf("occurrence %q predates the catch-up window starting %q", date, earliest)
		}
	}

	// And today is definitely there: a bounded catch-up must not skip the present.
	if len(got) == 0 || got[len(got)-1] != "2026-07-25" {
		t.Errorf("occurrences = %v, want today's included", got)
	}
}

func TestWeeklyRuleLandsOnTheRightWeekday(t *testing.T) {
	f := newFixture(t)

	// 2026-07-25 is a Saturday; the next Monday is the 27th.
	id := f.createRecurrence(recurrenceOpts{
		RRule: "FREQ=WEEKLY;BYDAY=MO", StartsOn: "2026-07-01", LeadDays: 14,
	})

	f.run()

	got := f.occurrences(id)
	if len(got) == 0 {
		t.Fatal("no occurrences spawned for a weekly rule")
	}

	for _, date := range got {
		parsed, err := time.Parse(database.DateOnly, date)
		if err != nil {
			t.Fatalf("parse %q: %v", date, err)
		}

		if parsed.Weekday() != time.Monday {
			t.Errorf("occurrence %q is a %s, want Monday", date, parsed.Weekday())
		}
	}

	// The rule is anchored at starts_on, not at whenever the spawner first ran,
	// so a Saturday run still produces Mondays.
	if got[0] != "2026-07-20" && got[0] != "2026-07-27" {
		t.Errorf("first occurrence = %q, want a Monday inside the window", got[0])
	}
}

func TestSeriesEndsAtEndsOn(t *testing.T) {
	f := newFixture(t)

	id := f.createRecurrence(recurrenceOpts{
		RRule: "FREQ=DAILY", StartsOn: "2026-07-25", EndsOn: "2026-07-27", LeadDays: 30,
	})

	result := f.run()

	got := f.occurrences(id)
	if len(got) != 3 {
		t.Errorf("occurrences = %v, want the three days up to ends_on", got)
	}

	for _, date := range got {
		if date > "2026-07-27" {
			t.Errorf("occurrence %q is past ends_on", date)
		}
	}

	// A finished series is deactivated rather than deleted, so its spawned tasks
	// keep their template reference and stay real history.
	_, _, active := f.state(id)
	if active {
		t.Error("a series past its end date is still active")
	}

	if result.Completed != 1 {
		t.Errorf("completed = %d, want 1", result.Completed)
	}

	// It is no longer picked up, and nothing more appears.
	if second := f.run(); second.Templates != 0 {
		t.Errorf("a completed series was queried again (%d templates)", second.Templates)
	}
}

func TestCountLimitedRuleStops(t *testing.T) {
	f := newFixture(t)

	id := f.createRecurrence(recurrenceOpts{
		RRule: "FREQ=DAILY;COUNT=3", StartsOn: "2026-07-25", LeadDays: 30,
	})

	f.run()

	if got := f.occurrences(id); len(got) != 3 {
		t.Errorf("occurrences = %v, want exactly 3 from COUNT=3", got)
	}

	if _, _, active := f.state(id); active {
		t.Error("a COUNT-limited series is still active after producing every occurrence")
	}
}

// TestTimezoneDecidesTheDate matters because occurrence_on is a plain date: the
// same instant is a different day either side of the date line.
func TestTimezoneDecidesTheDate(t *testing.T) {
	f := newFixture(t)

	// fixedNow is 2026-07-25 12:00 UTC. In Kiritimati (UTC+14) that is already
	// the 26th; in Honolulu (UTC-10) it is still the 25th.
	ahead := f.createRecurrence(recurrenceOpts{
		RRule: "FREQ=DAILY", StartsOn: "2026-07-01", Timezone: "Pacific/Kiritimati",
	})

	behind := f.createRecurrence(recurrenceOpts{
		RRule: "FREQ=DAILY", StartsOn: "2026-07-01", Timezone: "Pacific/Honolulu",
	})

	f.run()

	aheadDates := f.occurrences(ahead)
	behindDates := f.occurrences(behind)

	if len(aheadDates) == 0 || len(behindDates) == 0 {
		t.Fatalf("expected occurrences for both zones, got %v and %v", aheadDates, behindDates)
	}

	latestAhead := aheadDates[len(aheadDates)-1]
	latestBehind := behindDates[len(behindDates)-1]

	if latestAhead != "2026-07-26" {
		t.Errorf("UTC+14 latest occurrence = %q, want 2026-07-26", latestAhead)
	}

	if latestBehind != "2026-07-25" {
		t.Errorf("UTC-10 latest occurrence = %q, want 2026-07-25", latestBehind)
	}
}

func TestSpawnedTaskCarriesTemplateFields(t *testing.T) {
	f := newFixture(t)

	estimate := int64(30)

	created, err := f.store.CreateRecurrence(context.Background(), f.userID, store.RecurrenceCreate{
		ContextID:       f.ctxID,
		Title:           "Weekly report",
		Details:         strptr("Summarise the week"),
		RRule:           "FREQ=WEEKLY;BYDAY=MO",
		Timezone:        "UTC",
		StartsOn:        "2026-07-20",
		EstimateMinutes: &estimate,
		Source:          strptr("self"),
	})
	if err != nil {
		t.Fatalf("create recurrence: %v", err)
	}

	f.spawner.now = func() time.Time { return time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC) }
	f.run()

	tasks, _, err := f.store.ListTasks(context.Background(), f.userID, store.TaskFilter{
		RecurrenceID: created.ID,
	})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}

	if len(tasks) == 0 {
		t.Fatal("no task spawned")
	}

	task := tasks[0]

	if task.Title != "Weekly report" {
		t.Errorf("title = %q, want the template's", task.Title)
	}

	if task.Details == nil || *task.Details != "Summarise the week" {
		t.Errorf("details = %v, want the template's", task.Details)
	}

	if task.EstimateMinutes == nil || *task.EstimateMinutes != 30 {
		t.Errorf("estimate_minutes = %v, want 30", task.EstimateMinutes)
	}

	if task.CaptureMethod != "recurrence" {
		t.Errorf("capture_method = %q, want recurrence", task.CaptureMethod)
	}

	if task.Source == nil || *task.Source != "self" {
		t.Errorf("source = %v, want self", task.Source)
	}

	// due_on is the occurrence date, which is what puts it in the daily brief.
	if task.DueOn == nil || *task.DueOn != "2026-07-27" {
		t.Errorf("due_on = %v, want the occurrence date", task.DueOn)
	}

	// kind is derived, and a spawned task must read as recurring.
	if task.Kind != "recurring" {
		t.Errorf("kind = %q, want recurring", task.Kind)
	}

	if task.Status != "todo" {
		t.Errorf("status = %q, want todo", task.Status)
	}
}

// TestDelegatedTemplateSpawnsDelegated covers the schema CHECK that forbids a
// delegated task with no delegate: a template with a delegate has to produce a
// task whose status matches, or the insert is rejected outright.
func TestDelegatedTemplateSpawnsDelegated(t *testing.T) {
	f := newFixture(t)

	person, err := f.store.CreatePerson(context.Background(), f.userID, store.PersonCreate{
		Name: "Marc",
	})
	if err != nil {
		t.Fatalf("create person: %v", err)
	}

	id := f.createRecurrence(recurrenceOpts{
		RRule: "FREQ=DAILY", StartsOn: "2026-07-25", Delegated: &person.ID,
	})

	if result := f.run(); result.Created != 1 {
		t.Fatalf("created = %d, want 1; a delegated template must still spawn", result.Created)
	}

	tasks, _, err := f.store.ListTasks(context.Background(), f.userID, store.TaskFilter{
		RecurrenceID: id,
	})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}

	if len(tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(tasks))
	}

	if tasks[0].Status != "delegated" {
		t.Errorf("status = %q, want delegated", tasks[0].Status)
	}

	if tasks[0].DelegatedToID == nil || *tasks[0].DelegatedToID != person.ID {
		t.Errorf("delegated_to_id = %v, want %q", tasks[0].DelegatedToID, person.ID)
	}
}

// TestBadRuleDoesNotStopOtherTemplates keeps one corrupt row from silently
// halting every other series.
func TestBadRuleDoesNotStopOtherTemplates(t *testing.T) {
	f := newFixture(t)

	broken := f.createRecurrence(recurrenceOpts{RRule: "FREQ=DAILY", StartsOn: "2026-07-25"})

	// Bypass validation the way a hand-edited database or a future bug would.
	if _, err := f.store.DB().Exec(
		`UPDATE recurrences SET rrule = 'COMPLETE NONSENSE' WHERE id = ?`, broken,
	); err != nil {
		t.Fatalf("corrupt the rule: %v", err)
	}

	healthy := f.createRecurrence(recurrenceOpts{RRule: "FREQ=DAILY", StartsOn: "2026-07-25"})

	result := f.run()

	if result.Failed != 1 {
		t.Errorf("failed = %d, want 1", result.Failed)
	}

	if result.Created != 1 {
		t.Errorf("created = %d, want 1 from the healthy template", result.Created)
	}

	if got := f.occurrences(healthy); len(got) != 1 {
		t.Errorf("the healthy series spawned %v, want one occurrence", got)
	}

	if got := f.occurrences(broken); len(got) != 0 {
		t.Errorf("the broken series spawned %v, want nothing", got)
	}
}

func TestInactiveAndDeletedTemplatesAreIgnored(t *testing.T) {
	f := newFixture(t)

	paused := f.createRecurrence(recurrenceOpts{RRule: "FREQ=DAILY", StartsOn: "2026-07-25"})
	deleted := f.createRecurrence(recurrenceOpts{RRule: "FREQ=DAILY", StartsOn: "2026-07-25"})

	if _, err := f.store.DB().Exec(`UPDATE recurrences SET active = 0 WHERE id = ?`, paused); err != nil {
		t.Fatalf("pause: %v", err)
	}

	if err := f.store.DeleteRecurrence(context.Background(), f.userID, deleted); err != nil {
		t.Fatalf("delete: %v", err)
	}

	result := f.run()

	if result.Templates != 0 {
		t.Errorf("templates = %d, want 0", result.Templates)
	}

	for label, id := range map[string]string{"paused": paused, "deleted": deleted} {
		if got := f.occurrences(id); len(got) != 0 {
			t.Errorf("the %s series spawned %v, want nothing", label, got)
		}
	}
}

// TestCompletingAnOccurrenceDoesNotBlockTheNext checks the point of
// materializing: each occurrence is an independent task with its own history.
func TestCompletingAnOccurrenceDoesNotBlockTheNext(t *testing.T) {
	f := newFixture(t)

	id := f.createRecurrence(recurrenceOpts{RRule: "FREQ=DAILY", StartsOn: "2026-07-25"})

	f.run()

	tasks, _, err := f.store.ListTasks(context.Background(), f.userID, store.TaskFilter{
		RecurrenceID: id,
	})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}

	if len(tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(tasks))
	}

	if _, err := f.store.UpdateTask(context.Background(), f.userID, tasks[0].ID, store.TaskUpdate{
		Status: setField("done"),
	}); err != nil {
		t.Fatalf("complete the occurrence: %v", err)
	}

	// Move to the next day and run again.
	f.spawner.now = func() time.Time { return fixedNow.AddDate(0, 0, 1) }

	if result := f.run(); result.Created != 1 {
		t.Errorf("created = %d on the following day, want 1", result.Created)
	}

	got := f.occurrences(id)
	if len(got) != 2 {
		t.Fatalf("occurrences = %v, want two independent days", got)
	}

	// The completed one is still there: history survives.
	var doneCount int
	if err := f.store.DB().QueryRow(
		`SELECT count(*) FROM tasks WHERE recurrence_id = ? AND status = 'done'`, id,
	).Scan(&doneCount); err != nil {
		t.Fatalf("count completed: %v", err)
	}

	if doneCount != 1 {
		t.Errorf("completed occurrences = %d, want 1 retained as history", doneCount)
	}
}

func TestNormalizeRRule(t *testing.T) {
	for input, want := range map[string]string{
		"FREQ=DAILY":       "RRULE:FREQ=DAILY",
		"RRULE:FREQ=DAILY": "RRULE:FREQ=DAILY",
		"rrule:FREQ=DAILY": "rrule:FREQ=DAILY",
	} {
		if got := normalizeRRule(input); got != want {
			t.Errorf("normalizeRRule(%q) = %q, want %q", input, got, want)
		}
	}
}

func strptr(s string) *string { return &s }

// setField builds a patch field carrying a value, mirroring what a PATCH would.
func setField(value string) patch.Field[string] {
	return patch.Field[string]{Set: true, Value: value}
}
