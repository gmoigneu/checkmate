package fixtures

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
	"github.com/nls/checkmate/server/internal/model"
)

func TestLoadCreatesRepresentativeDataset(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "fixtures.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := database.Migrate(ctx, db, log); err != nil {
		t.Fatal(err)
	}

	user, err := account.CreateUser(ctx, db, "fixtures@example.com", "Fixture User", "Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	summary, err := Load(ctx, db, user.ID, Options{Now: now, Timezone: user.Timezone})
	if err != nil {
		t.Fatal(err)
	}

	if summary.Contexts != 4 || summary.Projects != 6 || summary.People != 5 || summary.Recurrences != 4 {
		t.Fatalf("unexpected entity summary: %+v", summary)
	}
	if summary.Tasks < 50 {
		t.Fatalf("tasks = %d, want at least 50", summary.Tasks)
	}
	if summary.Activity <= summary.Tasks {
		t.Fatalf("activity = %d, want more than one creation per task on average", summary.Activity)
	}
	if summary.HistoryFrom != "2026-04-26" {
		t.Fatalf("history starts %s, want 2026-04-26", summary.HistoryFrom)
	}

	assertValues(t, db, `
		SELECT DISTINCT kind
		FROM tasks_with_kind
		WHERE user_id = ? AND deleted_at IS NULL`, user.ID, model.TaskKinds)
	assertValues(t, db, `
		SELECT DISTINCT status
		FROM tasks
		WHERE user_id = ? AND deleted_at IS NULL`, user.ID, model.WritableTaskStatuses)
	assertValues(t, db, `
		SELECT DISTINCT kind
		FROM recurrences
		WHERE user_id = ? AND deleted_at IS NULL`, user.ID, model.RecurrenceKinds)
	assertValues(t, db, `
		SELECT DISTINCT priority
		FROM tasks
		WHERE user_id = ? AND deleted_at IS NULL AND priority IS NOT NULL`, user.ID, model.TaskPriorities)
	assertValues(t, db, `
		SELECT DISTINCT capture_method
		FROM tasks
		WHERE user_id = ? AND deleted_at IS NULL`, user.ID, model.CaptureMethods)
	assertValues(t, db, `
		SELECT DISTINCT source_key
		FROM tasks
		WHERE user_id = ? AND deleted_at IS NULL AND source_key IS NOT NULL`,
		user.ID, []string{"self", "email", "slack", "google_chat", "meeting", "phone"})
	assertValues(t, db, `
		SELECT DISTINCT status
		FROM projects
		WHERE user_id = ? AND deleted_at IS NULL`,
		user.ID, model.ProjectStatuses)

	var (
		activeRecurrences   int
		pausedRecurrences   int
		finishedRecurrences int
		archivedContexts    int
		tombstonedTasks     int
		expiredTasks        int
		earliestCompletion  string
	)
	err = db.QueryRowContext(ctx, `
		SELECT
			sum(active = 1),
			sum(active = 0 AND completed_at IS NULL),
			sum(active = 0 AND completed_at IS NOT NULL)
		FROM recurrences
		WHERE user_id = ? AND deleted_at IS NULL`, user.ID).
		Scan(&activeRecurrences, &pausedRecurrences, &finishedRecurrences)
	if err != nil {
		t.Fatal(err)
	}
	if activeRecurrences == 0 || pausedRecurrences == 0 || finishedRecurrences == 0 {
		t.Fatalf("recurrence states active=%d paused=%d finished=%d",
			activeRecurrences, pausedRecurrences, finishedRecurrences)
	}

	var dueActiveRecurrences int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*)
		FROM recurrences
		WHERE user_id = ? AND active = 1 AND next_occurrence_on <= '2026-07-26'`,
		user.ID).Scan(&dueActiveRecurrences); err != nil {
		t.Fatal(err)
	}
	if dueActiveRecurrences != 0 {
		t.Fatalf("active recurrences still due = %d, want 0", dueActiveRecurrences)
	}

	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM contexts WHERE user_id = ? AND archived_at IS NOT NULL`,
		user.ID).Scan(&archivedContexts); err != nil {
		t.Fatal(err)
	}
	if archivedContexts == 0 {
		t.Fatal("expected an archived context")
	}

	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM tasks WHERE user_id = ? AND deleted_at IS NOT NULL`,
		user.ID).Scan(&tombstonedTasks); err != nil {
		t.Fatal(err)
	}
	if tombstonedTasks == 0 {
		t.Fatal("expected a tombstoned task")
	}

	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM tasks
		 WHERE user_id = ? AND status = ? AND expired_at IS NOT NULL`,
		user.ID, model.StatusCancelled).Scan(&expiredTasks); err != nil {
		t.Fatal(err)
	}
	if expiredTasks == 0 {
		t.Fatal("expected an expired routine task")
	}

	if err := db.QueryRowContext(ctx,
		`SELECT min(completed_at) FROM tasks WHERE user_id = ? AND completed_at IS NOT NULL`,
		user.ID).Scan(&earliestCompletion); err != nil {
		t.Fatal(err)
	}
	if earliestCompletion > "2026-05-03" {
		t.Fatalf("earliest completion = %s, want history reaching late April", earliestCompletion)
	}

	assertFixtureActivity(t, ctx, db, user.ID, summary)

	rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("fixture data violates a foreign key")
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}

	if err := Reset(ctx, db); err != nil {
		t.Fatal(err)
	}

	var users, sources int
	var rev int64
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sources`).Scan(&sources); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT value FROM change_seq WHERE id = 1`).Scan(&rev); err != nil {
		t.Fatal(err)
	}
	if users != 0 || sources != 6 || rev != 0 {
		t.Fatalf("after reset users=%d sources=%d rev=%d, want 0, 6, 0", users, sources, rev)
	}
}

func TestLoadDoesNotSeedFutureHistoryAtMonthStart(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "month-start.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := database.Migrate(ctx, db, log); err != nil {
		t.Fatal(err)
	}

	user, err := account.CreateUser(ctx, db, "month-start@example.com", "Month Start", "UTC")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	if _, err := Load(ctx, db, user.ID, Options{Now: now, Timezone: user.Timezone}); err != nil {
		t.Fatal(err)
	}

	var futureTasks, futureActivity int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*)
		FROM tasks
		WHERE user_id = ? AND (
			completed_at > ? OR cancelled_at > ? OR expired_at > ?
		)`, user.ID, now.Format(database.Timestamp), now.Format(database.Timestamp),
		now.Format(database.Timestamp)).Scan(&futureTasks); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `
		SELECT count(*)
		FROM task_activity
		WHERE user_id = ? AND occurred_at > ?`,
		user.ID, now.Format(database.Timestamp)).Scan(&futureActivity); err != nil {
		t.Fatal(err)
	}
	if futureTasks != 0 || futureActivity != 0 {
		t.Fatalf("future task timestamps=%d activity=%d, want none", futureTasks, futureActivity)
	}
}

func assertFixtureActivity(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	userID string,
	summary Summary,
) {
	t.Helper()

	var activityCount, createdCount int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*), sum(action = 'created')
		FROM task_activity
		WHERE user_id = ?`, userID).Scan(&activityCount, &createdCount); err != nil {
		t.Fatal(err)
	}
	if activityCount != summary.Activity || createdCount != summary.Tasks {
		t.Fatalf("activity=%d created=%d, want summary=%d tasks=%d",
			activityCount, createdCount, summary.Activity, summary.Tasks)
	}

	assertValues(t, db, `
		SELECT DISTINCT action
		FROM task_activity
		WHERE user_id = ?`, userID, []string{"created", "updated", "deleted"})
	assertValues(t, db, `
		SELECT DISTINCT status_after
		FROM task_activity
		WHERE user_id = ? AND changed_fields LIKE '%status%'`,
		userID, []string{model.StatusDone, model.StatusCancelled, model.StatusInProgress,
			model.StatusBlocked, model.StatusDelegated})

	rows, err := db.QueryContext(ctx, `
		SELECT occurred_at
		FROM task_activity
		WHERE user_id = ?
		ORDER BY id`, userID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	previous := ""
	for rows.Next() {
		var occurredAt string
		if err := rows.Scan(&occurredAt); err != nil {
			t.Fatal(err)
		}
		if occurredAt < previous {
			t.Fatalf("activity id order is not chronological: %s follows %s", occurredAt, previous)
		}
		previous = occurredAt
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func assertValues(
	t *testing.T,
	db *sql.DB,
	query, userID string,
	want []string,
) {
	t.Helper()

	rows, err := db.QueryContext(context.Background(), query, userID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	got := make(map[string]bool)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		got[value] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	for _, value := range want {
		if !got[value] {
			t.Errorf("missing fixture value %q", value)
		}
	}
}
