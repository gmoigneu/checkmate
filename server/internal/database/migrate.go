package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

const migrationsDir = "migrations"

func init() {
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		panic(fmt.Sprintf("database: set goose dialect: %v", err))
	}
}

// Migrate applies every pending migration. Safe to call on every boot.
func Migrate(ctx context.Context, db *sql.DB, log *slog.Logger) error {
	if log == nil {
		goose.SetLogger(goose.NopLogger())
	} else {
		goose.SetLogger(slogGooseLogger{log})
	}

	if err := goose.UpContext(ctx, db, migrationsDir); err != nil {
		return fmt.Errorf("database: migrate up: %w", err)
	}

	return nil
}

// MigrateDown rolls back the most recent migration.
func MigrateDown(ctx context.Context, db *sql.DB, log *slog.Logger) error {
	if log != nil {
		goose.SetLogger(slogGooseLogger{log})
	}

	if err := goose.DownContext(ctx, db, migrationsDir); err != nil {
		return fmt.Errorf("database: migrate down: %w", err)
	}

	return nil
}

// MigrationStatus writes the applied/pending state of each migration to w.
func MigrationStatus(ctx context.Context, db *sql.DB, w io.Writer) error {
	goose.SetLogger(writerGooseLogger{w})

	if err := goose.StatusContext(ctx, db, migrationsDir); err != nil {
		return fmt.Errorf("database: migration status: %w", err)
	}

	return nil
}

// Version returns the current schema version.
func Version(ctx context.Context, db *sql.DB) (int64, error) {
	v, err := goose.GetDBVersionContext(ctx, db)
	if err != nil {
		return 0, fmt.Errorf("database: schema version: %w", err)
	}

	return v, nil
}

// writerGooseLogger sends goose's output straight to a writer, which is what
// `migrate status` wants: a table on stdout, not structured log lines.
type writerGooseLogger struct{ w io.Writer }

func (l writerGooseLogger) Printf(format string, v ...any) {
	l.print(format, v...)
}

func (l writerGooseLogger) Fatalf(format string, v ...any) {
	l.print(format, v...)
}

// goose's format strings omit the trailing newline, expecting a log.Logger to
// supply it, so add one here or the status table arrives as a single line.
func (l writerGooseLogger) print(format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	if !strings.HasSuffix(msg, "\n") {
		msg += "\n"
	}

	fmt.Fprint(l.w, msg)
}

// slogGooseLogger adapts goose's printf-style logger onto slog.
type slogGooseLogger struct{ log *slog.Logger }

func (l slogGooseLogger) Printf(format string, v ...any) {
	l.log.Info(trimNewline(fmt.Sprintf(format, v...)))
}

func (l slogGooseLogger) Fatalf(format string, v ...any) {
	l.log.Error(trimNewline(fmt.Sprintf(format, v...)))
}

func trimNewline(s string) string {
	return strings.TrimRight(s, "\r\n")
}
