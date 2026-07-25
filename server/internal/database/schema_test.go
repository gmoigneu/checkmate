package database_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/nls/checkmate/server/internal/database"
	"github.com/nls/checkmate/server/internal/id"
)

// newTestDB returns a migrated database backed by a file in the test's temp
// directory. A file rather than :memory: so WAL and multiple connections behave
// exactly as they do in production.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()

	ctx := context.Background()

	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	t.Cleanup(func() { db.Close() })

	if err := database.Migrate(ctx, db, nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	return db
}

func newUser(t *testing.T, db *sql.DB) string {
	t.Helper()

	uid := id.New()

	_, err := db.Exec(`INSERT INTO users (id, email, name) VALUES (?, ?, ?)`,
		uid, uid+"@example.com", "Test User")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	return uid
}

func newContext(t *testing.T, db *sql.DB, userID, slug string) string {
	t.Helper()

	cid := id.New()

	_, err := db.Exec(`INSERT INTO contexts (id, user_id, name, slug) VALUES (?, ?, ?, ?)`,
		cid, userID, slug, slug)
	if err != nil {
		t.Fatalf("insert context: %v", err)
	}

	return cid
}

// newTask inserts a minimal task and returns its id.
func newTask(t *testing.T, db *sql.DB, userID, contextID, title string) string {
	t.Helper()

	tid := id.New()

	_, err := db.Exec(
		`INSERT INTO tasks (id, user_id, context_id, title, status) VALUES (?, ?, ?, ?, 'todo')`,
		tid, userID, contextID, title)
	if err != nil {
		t.Fatalf("insert task %q: %v", title, err)
	}

	return tid
}

func TestMigrationsApply(t *testing.T) {
	db := newTestDB(t)

	version, err := database.Version(context.Background(), db)
	if err != nil {
		t.Fatalf("version: %v", err)
	}

	// Compared against the embedded migrations rather than a hardcoded number,
	// so adding a migration does not need this test edited.
	latest, err := database.LatestVersion()
	if err != nil {
		t.Fatalf("latest version: %v", err)
	}

	if version != latest {
		t.Errorf("schema version = %d, want %d (every migration applied)", version, latest)
	}

	want := []string{
		"users", "api_tokens", "sources", "contexts", "projects",
		"people", "recurrences", "tasks", "change_seq",
	}

	for _, table := range want {
		var name string

		err := db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %s missing: %v", table, err)
		}
	}

	var sources int
	if err := db.QueryRow(`SELECT count(*) FROM sources`).Scan(&sources); err != nil {
		t.Fatalf("count sources: %v", err)
	}

	if sources != 6 {
		t.Errorf("seeded sources = %d, want 6", sources)
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	db := newTestDB(t)
	uid := newUser(t, db)

	_, err := db.Exec(
		`INSERT INTO tasks (id, user_id, context_id, title) VALUES (?, ?, ?, ?)`,
		id.New(), uid, "does-not-exist", "orphan")
	if err == nil {
		t.Fatal("inserting a task with an unknown context_id succeeded, want FK violation")
	}
}

func TestTaskPriorityVocabularyIsEnforced(t *testing.T) {
	db := newTestDB(t)
	uid := newUser(t, db)
	cid := newContext(t, db, uid, "work")

	if _, err := db.Exec(
		`INSERT INTO tasks (id, user_id, context_id, title, priority)
		 VALUES (?, ?, ?, ?, 'critical')`,
		id.New(), uid, cid, "invalid priority",
	); err == nil {
		t.Fatal("inserting an unknown task priority succeeded, want CHECK violation")
	}

	if _, err := db.Exec(
		`INSERT INTO tasks (id, user_id, context_id, title, priority)
		 VALUES (?, ?, ?, ?, 'urgent')`,
		id.New(), uid, cid, "valid priority",
	); err != nil {
		t.Fatalf("inserting an urgent task: %v", err)
	}
}

func TestRevIsGloballyMonotonic(t *testing.T) {
	db := newTestDB(t)
	uid := newUser(t, db)

	cid := newContext(t, db, uid, "upsun")
	contextRev := revOf(t, db, "contexts", cid)

	if contextRev == 0 {
		t.Fatal("context rev = 0 after insert, want a stamped value")
	}

	tid := newTask(t, db, uid, cid, "write the api")
	insertRev := revOf(t, db, "tasks", tid)

	if insertRev <= contextRev {
		t.Errorf("task rev = %d, want greater than context rev %d", insertRev, contextRev)
	}

	if _, err := db.Exec(`UPDATE tasks SET title = ? WHERE id = ?`, "write the API", tid); err != nil {
		t.Fatalf("update task: %v", err)
	}

	updateRev := revOf(t, db, "tasks", tid)
	if updateRev <= insertRev {
		t.Errorf("rev after update = %d, want greater than %d", updateRev, insertRev)
	}

	// A tombstone is just another update, so deletes show up in the delta too.
	if _, err := db.Exec(
		`UPDATE tasks SET deleted_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`, tid,
	); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	if deleteRev := revOf(t, db, "tasks", tid); deleteRev <= updateRev {
		t.Errorf("rev after soft delete = %d, want greater than %d", deleteRev, updateRev)
	}
}

// TestRevTriggersSurviveRecursiveTriggers pins the reason the rev triggers carry
// a WHEN guard: their own write re-enters the update trigger when sqlite has
// recursive_triggers on, and the guard is what stops it terminating in an error.
func TestRevTriggersSurviveRecursiveTriggers(t *testing.T) {
	db := newTestDB(t)

	if _, err := db.Exec(`PRAGMA recursive_triggers = ON`); err != nil {
		t.Fatalf("enable recursive triggers: %v", err)
	}

	uid := newUser(t, db)
	cid := newContext(t, db, uid, "upsun")
	tid := newTask(t, db, uid, cid, "recursion check")

	before := revOf(t, db, "tasks", tid)

	if _, err := db.Exec(`UPDATE tasks SET status = 'done' WHERE id = ?`, tid); err != nil {
		t.Fatalf("update with recursive triggers on: %v", err)
	}

	if after := revOf(t, db, "tasks", tid); after <= before {
		t.Errorf("rev = %d after update, want greater than %d", after, before)
	}
}

func revOf(t *testing.T, db *sql.DB, table, rowID string) int64 {
	t.Helper()

	var rev int64

	// table is a test-supplied constant, never user input.
	if err := db.QueryRow(`SELECT rev FROM `+table+` WHERE id = ?`, rowID).Scan(&rev); err != nil {
		t.Fatalf("read rev from %s: %v", table, err)
	}

	return rev
}

func TestDelegatedRequiresPerson(t *testing.T) {
	db := newTestDB(t)
	uid := newUser(t, db)
	cid := newContext(t, db, uid, "upsun")

	_, err := db.Exec(
		`INSERT INTO tasks (id, user_id, context_id, title, status) VALUES (?, ?, ?, ?, 'delegated')`,
		id.New(), uid, cid, "chase the invoice")
	if err == nil {
		t.Fatal("delegated task without delegated_to_id succeeded, want CHECK violation")
	}

	pid := id.New()
	if _, err := db.Exec(
		`INSERT INTO people (id, user_id, name) VALUES (?, ?, ?)`, pid, uid, "Marc",
	); err != nil {
		t.Fatalf("insert person: %v", err)
	}

	if _, err := db.Exec(
		`INSERT INTO tasks (id, user_id, context_id, title, status, delegated_to_id)
		 VALUES (?, ?, ?, ?, 'delegated', ?)`,
		id.New(), uid, cid, "chase the invoice", pid,
	); err != nil {
		t.Fatalf("delegated task with a person should insert: %v", err)
	}
}

func TestDateColumnsRejectNonISOFormat(t *testing.T) {
	db := newTestDB(t)
	uid := newUser(t, db)
	cid := newContext(t, db, uid, "upsun")

	for _, col := range []string{"due_on", "planned_on"} {
		_, err := db.Exec(
			`INSERT INTO tasks (id, user_id, context_id, title, `+col+`) VALUES (?, ?, ?, ?, ?)`,
			id.New(), uid, cid, "badly dated", "25/07/2026")
		if err == nil {
			t.Errorf("%s accepted 25/07/2026, want CHECK violation", col)
		}
	}

	if _, err := db.Exec(
		`INSERT INTO tasks (id, user_id, context_id, title, due_on, planned_on)
		 VALUES (?, ?, ?, ?, '2026-07-25', '2026-07-24')`,
		id.New(), uid, cid, "well dated",
	); err != nil {
		t.Fatalf("ISO dates should insert: %v", err)
	}
}

func TestOccurrenceIsUniquePerSeries(t *testing.T) {
	db := newTestDB(t)
	uid := newUser(t, db)
	cid := newContext(t, db, uid, "upsun")

	rid := id.New()

	_, err := db.Exec(
		`INSERT INTO recurrences (id, user_id, context_id, title, rrule, starts_on)
		 VALUES (?, ?, ?, 'daily standup', 'FREQ=DAILY', '2026-07-25')`,
		rid, uid, cid)
	if err != nil {
		t.Fatalf("insert recurrence: %v", err)
	}

	insert := func() error {
		_, err := db.Exec(
			`INSERT INTO tasks (id, user_id, context_id, title, recurrence_id, occurrence_on, capture_method)
			 VALUES (?, ?, ?, 'daily standup', ?, '2026-07-25', 'recurrence')`,
			id.New(), uid, cid, rid)

		return err
	}

	if err := insert(); err != nil {
		t.Fatalf("first occurrence: %v", err)
	}

	if err := insert(); err == nil {
		t.Fatal("second occurrence for the same date succeeded, want unique violation")
	}

	// The partial index must not constrain ordinary one-shot tasks, which all
	// carry NULL in both columns.
	newTask(t, db, uid, cid, "one-shot a")
	newTask(t, db, uid, cid, "one-shot b")
}

func TestSelfReferenceRejected(t *testing.T) {
	db := newTestDB(t)
	uid := newUser(t, db)
	cid := newContext(t, db, uid, "upsun")
	tid := newTask(t, db, uid, cid, "ship it")

	for _, col := range []string{"parent_id", "blocked_by_id"} {
		if _, err := db.Exec(`UPDATE tasks SET `+col+` = id WHERE id = ?`, tid); err == nil {
			t.Errorf("%s = own id succeeded, want CHECK violation", col)
		}
	}
}

// TestDerivedKind pins the mapping from brief section B onto the columns that
// actually store the information.
func TestDerivedKind(t *testing.T) {
	db := newTestDB(t)
	uid := newUser(t, db)
	cid := newContext(t, db, uid, "upsun")

	short := newTask(t, db, uid, cid, "short one")

	long := newTask(t, db, uid, cid, "long one")
	child := newTask(t, db, uid, cid, "a subtask")

	if _, err := db.Exec(`UPDATE tasks SET parent_id = ? WHERE id = ?`, long, child); err != nil {
		t.Fatalf("attach subtask: %v", err)
	}

	blocker := newTask(t, db, uid, cid, "the blocker")
	blocked := newTask(t, db, uid, cid, "the blocked one")

	if _, err := db.Exec(
		`UPDATE tasks SET blocked_by_id = ?, status = 'blocked' WHERE id = ?`, blocker, blocked,
	); err != nil {
		t.Fatalf("set blocker: %v", err)
	}

	pid := id.New()
	if _, err := db.Exec(
		`INSERT INTO people (id, user_id, name) VALUES (?, ?, 'Marc')`, pid, uid,
	); err != nil {
		t.Fatalf("insert person: %v", err)
	}

	delegated := id.New()
	if _, err := db.Exec(
		`INSERT INTO tasks (id, user_id, context_id, title, status, delegated_to_id)
		 VALUES (?, ?, ?, 'delegated one', 'delegated', ?)`,
		delegated, uid, cid, pid,
	); err != nil {
		t.Fatalf("insert delegated task: %v", err)
	}

	rid := id.New()
	if _, err := db.Exec(
		`INSERT INTO recurrences (id, user_id, context_id, title, rrule, starts_on)
		 VALUES (?, ?, ?, 'standup', 'FREQ=DAILY', '2026-07-25')`,
		rid, uid, cid,
	); err != nil {
		t.Fatalf("insert recurrence: %v", err)
	}

	recurring := id.New()
	if _, err := db.Exec(
		`INSERT INTO tasks (id, user_id, context_id, title, recurrence_id, occurrence_on, capture_method)
		 VALUES (?, ?, ?, 'standup', ?, '2026-07-25', 'recurrence')`,
		recurring, uid, cid, rid,
	); err != nil {
		t.Fatalf("insert recurring task: %v", err)
	}

	cases := map[string]string{
		short:     "short",
		long:      "long",
		child:     "short",
		blocked:   "blocked",
		delegated: "delegated",
		recurring: "recurring",
	}

	for taskID, wantKind := range cases {
		var got, title string

		err := db.QueryRow(`SELECT kind, title FROM tasks_with_kind WHERE id = ?`, taskID).
			Scan(&got, &title)
		if err != nil {
			t.Fatalf("read kind: %v", err)
		}

		if got != wantKind {
			t.Errorf("kind of %q = %q, want %q", title, got, wantKind)
		}
	}
}

func TestMigrateDownThenUp(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	// Unwind every migration, not just the most recent, so each down step is
	// exercised and the schema returns to empty.
	for {
		version, err := database.Version(ctx, db)
		if err != nil {
			t.Fatalf("version: %v", err)
		}

		if version == 0 {
			break
		}

		if err := database.MigrateDown(ctx, db, nil); err != nil {
			t.Fatalf("migrate down from version %d: %v", version, err)
		}
	}

	var tables int

	err := db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'tasks'`,
	).Scan(&tables)
	if err != nil {
		t.Fatalf("count tasks table: %v", err)
	}

	if tables != 0 {
		t.Error("tasks table still present after migrate down")
	}

	if err := database.Migrate(ctx, db, nil); err != nil {
		t.Fatalf("migrate up again: %v", err)
	}

	if _, err := db.Exec(`SELECT 1 FROM tasks`); err != nil {
		t.Errorf("tasks table not restored: %v", err)
	}
}

// TestMigrationsPreserveExistingData seeds data at the first schema version and
// then migrates all the way up, asserting nothing was lost on the way.
//
// This exists because of a specific hazard: sqlite cannot ALTER a column's
// default or nullability, so changing one means rebuilding the table, and with
// foreign keys enabled DROP TABLE performs an implicit DELETE that fires
// ON DELETE CASCADE into every child table. PRAGMA foreign_keys is a no-op
// inside a transaction, so a migration cannot switch that off. A rebuild of a
// parent table would therefore wipe user data silently, and the migration would
// still report success.
func TestMigrationsPreserveExistingData(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	t.Cleanup(func() { db.Close() })

	if err := database.MigrateUpTo(ctx, db, 1, nil); err != nil {
		t.Fatalf("migrate to version 1: %v", err)
	}

	uid := newUser(t, db)
	cid := newContext(t, db, uid, "upsun")
	tid := newTask(t, db, uid, cid, "must survive every migration")

	if _, err := db.Exec(
		`INSERT INTO api_tokens (id, user_id, name, token_hash) VALUES (?, ?, 'device', 'deadbeef')`,
		id.New(), uid,
	); err != nil {
		t.Fatalf("insert token: %v", err)
	}

	if _, err := db.Exec(
		`INSERT INTO projects (id, user_id, context_id, name) VALUES (?, ?, ?, 'a project')`,
		id.New(), uid, cid,
	); err != nil {
		t.Fatalf("insert project: %v", err)
	}

	if err := database.Migrate(ctx, db, nil); err != nil {
		t.Fatalf("migrate to latest: %v", err)
	}

	for table, want := range map[string]int{
		"users": 1, "contexts": 1, "tasks": 1, "projects": 1, "api_tokens": 1,
	} {
		var got int

		if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}

		if got != want {
			t.Errorf("%s has %d rows after migrating up, want %d", table, got, want)
		}
	}

	// The specific row, not just the count.
	var title string
	if err := db.QueryRow(`SELECT title FROM tasks WHERE id = ?`, tid).Scan(&title); err != nil {
		t.Fatalf("read the seeded task back: %v", err)
	}

	if title != "must survive every migration" {
		t.Errorf("task title = %q after migrating", title)
	}
}

func TestCascadeOnUserDelete(t *testing.T) {
	db := newTestDB(t)
	uid := newUser(t, db)
	cid := newContext(t, db, uid, "upsun")
	newTask(t, db, uid, cid, "goes away with the user")

	if _, err := db.Exec(`DELETE FROM users WHERE id = ?`, uid); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	for _, table := range []string{"contexts", "tasks"} {
		var count int

		if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}

		if count != 0 {
			t.Errorf("%s has %d rows after user delete, want 0", table, count)
		}
	}
}
