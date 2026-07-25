package account_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nls/checkmate/server/internal/account"
	"github.com/nls/checkmate/server/internal/database"
)

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

func TestCreateUserSeedsContexts(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	u, err := account.CreateUser(ctx, db, "you@example.com", "You", "Europe/Paris")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	if u.ID == "" {
		t.Error("user id is empty")
	}

	var count int
	if err := db.QueryRow(`SELECT count(*) FROM contexts WHERE user_id = ?`, u.ID).Scan(&count); err != nil {
		t.Fatalf("count contexts: %v", err)
	}

	if want := len(account.DefaultContexts); count != want {
		t.Errorf("seeded contexts = %d, want %d", count, want)
	}

	var tz string
	if err := db.QueryRow(`SELECT timezone FROM users WHERE id = ?`, u.ID).Scan(&tz); err != nil {
		t.Fatalf("read timezone: %v", err)
	}

	if tz != "Europe/Paris" {
		t.Errorf("timezone = %q, want Europe/Paris", tz)
	}
}

func TestCreateUserRejectsDuplicateEmail(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if _, err := account.CreateUser(ctx, db, "you@example.com", "You", ""); err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Different case, same address: the email index is NOCASE.
	_, err := account.CreateUser(ctx, db, "You@Example.com", "You Again", "")
	if !errors.Is(err, account.ErrEmailTaken) {
		t.Fatalf("second create error = %v, want ErrEmailTaken", err)
	}

	var count int
	if err := db.QueryRow(`SELECT count(*) FROM users`).Scan(&count); err != nil {
		t.Fatalf("count users: %v", err)
	}

	if count != 1 {
		t.Errorf("users = %d, want 1 (failed create must not leave rows behind)", count)
	}
}

func TestCreateUserRejectsBadInput(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	cases := map[string][2]string{
		"no at sign":  {"notanemail", "Name"},
		"empty email": {"", "Name"},
		"empty name":  {"you@example.com", "  "},
	}

	for label, in := range cases {
		if _, err := account.CreateUser(ctx, db, in[0], in[1], ""); err == nil {
			t.Errorf("%s: expected an error", label)
		}
	}
}

func TestCreateTokenStoresOnlyTheHash(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	u, err := account.CreateUser(ctx, db, "you@example.com", "You", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	secret, err := account.CreateToken(ctx, db, u.ID, "iOS", "")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	if !strings.HasPrefix(secret, account.TokenPrefix) {
		t.Errorf("token %q missing prefix %q", secret, account.TokenPrefix)
	}

	var stored, scopes string

	err = db.QueryRow(`SELECT token_hash, scopes FROM api_tokens WHERE user_id = ?`, u.ID).
		Scan(&stored, &scopes)
	if err != nil {
		t.Fatalf("read token: %v", err)
	}

	if stored == secret {
		t.Error("plaintext token was stored in the database")
	}

	if stored != account.HashToken(secret) {
		t.Error("stored hash does not match HashToken(secret)")
	}

	if scopes != account.DefaultScopes {
		t.Errorf("scopes = %q, want %q", scopes, account.DefaultScopes)
	}

	// Two tokens for the same device name must not collide.
	other, err := account.CreateToken(ctx, db, u.ID, "iOS", "tasks:read")
	if err != nil {
		t.Fatalf("second token: %v", err)
	}

	if other == secret {
		t.Error("two tokens generated the same secret")
	}
}

func TestCreateTokenUnknownUser(t *testing.T) {
	db := newTestDB(t)

	if _, err := account.CreateToken(context.Background(), db, "nope", "iOS", ""); err == nil {
		t.Fatal("expected an error for an unknown user")
	}
}
