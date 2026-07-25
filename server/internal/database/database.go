// Package database owns the sqlite connection and the embedded migrations.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // pure-Go driver: keeps the binary CGO-free
)

// Timestamp is the layout used for every TEXT timestamp column. It matches the
// strftime format the schema defaults to, sorts lexicographically, and is valid
// RFC3339 so clients can parse it without help.
const Timestamp = "2006-01-02T15:04:05.000Z"

// DateOnly is the layout for calendar-date columns (due_on, planned_on, ...).
const DateOnly = "2006-01-02"

// Open connects to the sqlite database at path, creating the file and its
// parent directory if needed, and verifies the connection is usable.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	if path == "" {
		return nil, errors.New("database: empty path")
	}

	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("database: create dir %s: %w", dir, err)
		}
	}

	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("database: open %s: %w", path, err)
	}

	// One connection means writes never contend, so SQLITE_BUSY cannot happen.
	// For a personal system with a handful of clients this costs nothing; if
	// read throughput ever matters, split into a 1-conn writer pool and an
	// N-conn reader pool rather than raising this.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("database: ping %s: %w", path, err)
	}

	return db, nil
}

// dsn builds the connection string. modernc.org/sqlite applies each _pragma to
// every new connection, which is what we want for foreign_keys in particular:
// it is per-connection and off by default.
func dsn(path string) string {
	pragmas := []string{
		"foreign_keys(1)",
		"journal_mode(WAL)",
		"busy_timeout(5000)",
		"synchronous(NORMAL)",
	}

	q := make(url.Values, len(pragmas))
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}

	// modernc's driver takes the path verbatim, so ":memory:" and file paths
	// both work as-is with the query string appended.
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}

	return path + sep + q.Encode()
}
