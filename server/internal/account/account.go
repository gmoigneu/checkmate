// Package account creates users, their starting contexts, and API tokens.
//
// Checkmate has no signup flow yet: these are driven from the CLI so there is a
// way to get a usable database before the HTTP handlers exist.
package account

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/nls/checkmate/server/internal/id"
)

// TokenPrefix marks a Checkmate API token so leaked credentials are easy to
// recognise in logs and secret scanners.
const TokenPrefix = "cm_"

// DefaultScopes is what a token gets when none are requested: full access for
// the owner's own data. Read covers GET, write covers every mutation.
const DefaultScopes = "read write"

// DefaultContexts are the buckets tasks arrive into (brief section A). Created
// with every new user; rename or delete them freely afterwards.
var DefaultContexts = []struct {
	Name  string
	Slug  string
	Color string
}{
	{"Upsun", "upsun", "#6b46c1"},
	{"Personal", "personal", "#2f855a"},
	{"Gaal", "gaal", "#c05621"},
	{"Arkea", "arkea", "#2b6cb0"},
}

// User is a Checkmate account.
type User struct {
	ID       string
	Email    string
	Name     string
	Timezone string
}

var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// ErrEmailTaken is returned when an account already exists for that address.
var ErrEmailTaken = errors.New("account: email already registered")

// CreateUser inserts a user together with the default set of contexts, in one
// transaction. timezone may be empty, in which case UTC is used.
func CreateUser(ctx context.Context, db *sql.DB, email, name, timezone string) (User, error) {
	email = strings.TrimSpace(email)
	name = strings.TrimSpace(name)
	timezone = strings.TrimSpace(timezone)

	if !emailRe.MatchString(email) {
		return User{}, fmt.Errorf("account: %q is not a valid email", email)
	}

	if name == "" {
		return User{}, errors.New("account: name is required")
	}

	if timezone == "" {
		timezone = "UTC"
	}

	u := User{ID: id.New(), Email: email, Name: name, Timezone: timezone}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("account: begin: %w", err)
	}
	defer tx.Rollback()

	var exists int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM users WHERE email = ?`, email).Scan(&exists)

	switch {
	case err == nil:
		return User{}, ErrEmailTaken
	case !errors.Is(err, sql.ErrNoRows):
		return User{}, fmt.Errorf("account: lookup email: %w", err)
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO users (id, email, name, timezone) VALUES (?, ?, ?, ?)`,
		u.ID, u.Email, u.Name, u.Timezone,
	)
	if err != nil {
		return User{}, fmt.Errorf("account: insert user: %w", err)
	}

	for i, c := range DefaultContexts {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO contexts (id, user_id, name, slug, color, sort_order)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			id.New(), u.ID, c.Name, c.Slug, c.Color, (i+1)*10,
		)
		if err != nil {
			return User{}, fmt.Errorf("account: insert context %s: %w", c.Slug, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("account: commit: %w", err)
	}

	return u, nil
}

// CreateToken issues an API token for userID and returns the plaintext secret.
// Only its hash is stored, so this is the one and only chance to read it.
func CreateToken(ctx context.Context, db *sql.DB, userID, name, scopes string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("account: token name is required")
	}

	if strings.TrimSpace(scopes) == "" {
		scopes = DefaultScopes
	}

	var known int
	err := db.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id = ?`, userID).Scan(&known)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", fmt.Errorf("account: no user with id %s", userID)
	case err != nil:
		return "", fmt.Errorf("account: lookup user: %w", err)
	}

	secret, err := newSecret()
	if err != nil {
		return "", err
	}

	_, err = db.ExecContext(ctx,
		`INSERT INTO api_tokens (id, user_id, name, token_hash, scopes) VALUES (?, ?, ?, ?, ?)`,
		id.New(), userID, name, HashToken(secret), strings.TrimSpace(scopes),
	)
	if err != nil {
		return "", fmt.Errorf("account: insert token: %w", err)
	}

	return secret, nil
}

// FindUserByEmail looks up an account by address.
func FindUserByEmail(ctx context.Context, db *sql.DB, email string) (User, error) {
	var u User

	err := db.QueryRowContext(ctx,
		`SELECT id, email, name, timezone FROM users WHERE email = ?`,
		strings.TrimSpace(email),
	).Scan(&u.ID, &u.Email, &u.Name, &u.Timezone)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return User{}, fmt.Errorf("account: no user with email %s", email)
	case err != nil:
		return User{}, fmt.Errorf("account: lookup user: %w", err)
	}

	return u, nil
}

// HashToken returns the hex-encoded SHA-256 of a plaintext token. Tokens are
// high-entropy random strings, so a plain digest is the right primitive here:
// there is nothing to brute-force, and lookups stay a single indexed read.
func HashToken(secret string) string {
	sum := sha256.Sum256([]byte(secret))

	return hex.EncodeToString(sum[:])
}

func newSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("account: read random: %w", err)
	}

	return TokenPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}
