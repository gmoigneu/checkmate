package main

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nls/checkmate/server/internal/config"
	"github.com/nls/checkmate/server/internal/database"
)

func TestFixturesCmdLoadsAndSafelyResetsDemoAccount(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "checkmate.db")
	cfg := config.Config{
		Env:          "development",
		DatabasePath: path,
		AutoMigrate:  true,
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	args := []string{"load", "-email=local@example.com", "-token="}

	if err := fixturesCmd(ctx, cfg, log, args); err != nil {
		t.Fatal(err)
	}

	db, err := database.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	assertFixtureCounts(t, ctx, db, 1, 50)

	err = fixturesCmd(ctx, cfg, log, args)
	if err == nil || !strings.Contains(err.Error(), "pass -reset") {
		t.Fatalf("second load error = %v, want reset guidance", err)
	}
	assertFixtureCounts(t, ctx, db, 1, 50)

	if err := fixturesCmd(ctx, cfg, log, append(args, "-reset")); err != nil {
		t.Fatal(err)
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
		`SELECT count(*) FROM users WHERE email = 'local@example.com'`).Scan(&users); err != nil {
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
