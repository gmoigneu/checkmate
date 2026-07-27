package main

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/nls/checkmate/server/internal/account"
	"github.com/nls/checkmate/server/internal/config"
	"github.com/nls/checkmate/server/internal/database"
)

func TestFixturesCmdResetsAllDataAndProvisionsRequestedAccount(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "checkmate.db")
	cfg := config.Config{
		Env:          "development",
		DatabasePath: path,
		AutoMigrate:  true,
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	args := []string{"load", "-token=", "local@example.com"}

	if err := fixturesCmd(ctx, cfg, log, args); err != nil {
		t.Fatal(err)
	}

	db, err := database.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	assertFixtureCounts(t, ctx, db, 1, 50)

	var firstUserID string
	if err := db.QueryRowContext(ctx,
		`SELECT id FROM users WHERE email = 'local@example.com'`).Scan(&firstUserID); err != nil {
		t.Fatal(err)
	}

	if _, err := account.CreateUser(ctx, db, "other@example.com", "Other User", "UTC"); err != nil {
		t.Fatal(err)
	}

	if err := fixturesCmd(ctx, cfg, log, args); err != nil {
		t.Fatal(err)
	}
	assertFixtureCounts(t, ctx, db, 1, 50)

	var secondUserID string
	if err := db.QueryRowContext(ctx,
		`SELECT id FROM users WHERE email = 'local@example.com'`).Scan(&secondUserID); err != nil {
		t.Fatal(err)
	}
	if secondUserID == firstUserID {
		t.Fatal("fixture reload kept the old account instead of reprovisioning it")
	}

	if err := fixturesCmd(ctx, cfg, log, []string{"load", "-token="}); err == nil {
		t.Fatal("fixture load without an account email succeeded")
	}
	assertFixtureCounts(t, ctx, db, 1, 50)

	if err := fixturesCmd(ctx, cfg, log, []string{"load", "-token=", "not-an-email"}); err == nil {
		t.Fatal("fixture load with an invalid account email succeeded")
	}
	assertFixtureCounts(t, ctx, db, 1, 50)

	production := cfg
	production.Env = "production"
	if err := fixturesCmd(ctx, production, log, args); err == nil {
		t.Fatal("fixture load in production succeeded")
	}
	assertFixtureCounts(t, ctx, db, 1, 50)
}

func assertFixtureCounts(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	wantUsers, minimumTasks int,
) {
	t.Helper()

	var users, tasks int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM users`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `
		SELECT count(*)
		FROM tasks
		WHERE user_id = (SELECT id FROM users WHERE email = 'local@example.com')`).Scan(&tasks); err != nil {
		t.Fatal(err)
	}

	if users != wantUsers {
		t.Fatalf("users = %d, want %d", users, wantUsers)
	}
	if tasks < minimumTasks {
		t.Fatalf("tasks = %d, want at least %d", tasks, minimumTasks)
	}
}
