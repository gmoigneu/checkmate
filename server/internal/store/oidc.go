package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/nls/checkmate/server/internal/database"
	"github.com/nls/checkmate/server/internal/id"
)

// ErrUnknownFlow means the state parameter did not match a live login attempt.
var ErrUnknownFlow = errors.New("store: unknown or expired login flow")

// Flow is an in-flight federated login.
type Flow struct {
	State        string
	Nonce        string
	CodeVerifier string
	Provider     string
	RedirectTo   string
}

// BeginFlow records a login attempt before redirecting to the provider.
func (s *Store) BeginFlow(ctx context.Context, f Flow, ttl time.Duration) error {
	expires := time.Now().UTC().Add(ttl).Format(database.Timestamp)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO oidc_flows (state, nonce, code_verifier, provider, redirect_to, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		f.State, f.Nonce, f.CodeVerifier, f.Provider, nullIfEmpty(f.RedirectTo), expires,
	)
	if err != nil {
		return fmt.Errorf("store: begin login flow: %w", err)
	}

	return nil
}

// ConsumeFlow atomically fetches and deletes a login flow.
//
// The delete is what makes a callback single-use: replaying an intercepted
// callback URL finds nothing the second time. DELETE ... RETURNING keeps the
// fetch and the delete in one statement so two concurrent callbacks cannot both
// succeed.
func (s *Store) ConsumeFlow(ctx context.Context, state string) (Flow, error) {
	var (
		f          Flow
		redirectTo sql.NullString
	)

	err := s.db.QueryRowContext(ctx,
		`DELETE FROM oidc_flows
		 WHERE state = ? AND expires_at > `+nowExpr+`
		 RETURNING state, nonce, code_verifier, provider, redirect_to`,
		state,
	).Scan(&f.State, &f.Nonce, &f.CodeVerifier, &f.Provider, &redirectTo)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Flow{}, ErrUnknownFlow
	case err != nil:
		return Flow{}, fmt.Errorf("store: consume login flow: %w", err)
	}

	f.RedirectTo = redirectTo.String

	return f, nil
}

// FindUserByOIDCSubject resolves a federated identity to a Checkmate user.
func (s *Store) FindUserByOIDCSubject(ctx context.Context, provider, subject string) (string, error) {
	var userID string

	err := s.db.QueryRowContext(ctx,
		`SELECT user_id FROM oidc_identities WHERE provider = ? AND subject = ?`,
		provider, subject,
	).Scan(&userID)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", ErrNotFound
	case err != nil:
		return "", fmt.Errorf("store: find oidc identity: %w", err)
	}

	return userID, nil
}

// LinkOIDCIdentity attaches a provider identity to a user, refreshing the cached
// email if the identity already exists.
func (s *Store) LinkOIDCIdentity(ctx context.Context, userID, provider, subject, email string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO oidc_identities (id, user_id, provider, subject, email)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (provider, subject) DO UPDATE SET
		     email = excluded.email,
		     updated_at = `+nowExpr,
		id.New(), userID, provider, subject, nullIfEmpty(email),
	)
	if err != nil {
		return fmt.Errorf("store: link oidc identity: %w", err)
	}

	return nil
}

// FindUserIDByEmail resolves an email to a user id.
func (s *Store) FindUserIDByEmail(ctx context.Context, email string) (string, error) {
	var userID string

	err := s.db.QueryRowContext(ctx, `SELECT id FROM users WHERE email = ?`, email).Scan(&userID)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", ErrNotFound
	case err != nil:
		return "", fmt.Errorf("store: find user by email: %w", err)
	}

	return userID, nil
}
