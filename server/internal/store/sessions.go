package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nls/checkmate/server/internal/database"
	"github.com/nls/checkmate/server/internal/id"
	"github.com/nls/checkmate/server/internal/model"
)

// ErrInvalidSession means the session cookie is unknown, revoked or expired.
var ErrInvalidSession = errors.New("store: invalid session")

// SessionCookieName is the cookie the web UI authenticates with. The __Host-
// prefix binds it to this exact host with no subdomain scope and requires
// Secure, so a compromised sibling host cannot set it.
const SessionCookieName = "__Host-checkmate_session"

// SessionCookieNameInsecure is used when Secure cookies are off, since browsers
// reject a __Host- cookie without Secure. Development only.
const SessionCookieNameInsecure = "checkmate_session"

// Session is a browser login.
type Session struct {
	ID                string
	UserID            string
	ExpiresAt         string
	AbsoluteExpiresAt string
	LastSeenAt        *string
	UserAgent         *string
	IP                *string
	CreatedAt         string
}

// CreateSession opens a session and returns the plaintext cookie value, which is
// never stored and cannot be recovered afterwards.
func (s *Store) CreateSession(
	ctx context.Context,
	userID string,
	idleTimeout, maxLifetime time.Duration,
	userAgent, ip string,
) (string, Session, error) {
	secret, err := newSessionSecret()
	if err != nil {
		return "", Session{}, err
	}

	now := time.Now().UTC()

	sess := Session{
		ID:                id.New(),
		UserID:            userID,
		ExpiresAt:         now.Add(idleTimeout).Format(database.Timestamp),
		AbsoluteExpiresAt: now.Add(maxLifetime).Format(database.Timestamp),
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, token_hash, expires_at, absolute_expires_at, user_agent, ip)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sess.ID, userID, HashSecret(secret), sess.ExpiresAt, sess.AbsoluteExpiresAt,
		nullIfEmpty(userAgent), nullIfEmpty(ip),
	)
	if err != nil {
		return "", Session{}, fmt.Errorf("store: insert session: %w", err)
	}

	return secret, sess, nil
}

// AuthenticateSession resolves a cookie value to its owner and slides the idle
// timeout forward, never past the absolute ceiling.
func (s *Store) AuthenticateSession(
	ctx context.Context,
	secret string,
	idleTimeout time.Duration,
) (model.Identity, error) {
	var (
		ident     model.Identity
		sessionID string
		absolute  string
	)

	err := s.db.QueryRowContext(ctx, `
		SELECT s.id, s.user_id, s.absolute_expires_at, u.email, coalesce(u.name, u.email), u.timezone
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ?
		  AND s.revoked_at IS NULL
		  AND s.expires_at > `+nowExpr+`
		  AND s.absolute_expires_at > `+nowExpr,
		HashSecret(secret),
	).Scan(&sessionID, &ident.UserID, &absolute, &ident.Email, &ident.Name, &ident.Timezone)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return model.Identity{}, ErrInvalidSession
	case err != nil:
		return model.Identity{}, fmt.Errorf("store: authenticate session: %w", err)
	}

	ident.SessionID = sessionID

	// A session cookie stands in for the user, so it carries full access; the
	// read/write split exists to limit machine tokens, not the owner's browser.
	ident.Scopes = []string{"read", "write"}

	// min(now + idle, absolute): sliding expiry must not outlive the ceiling.
	next := time.Now().UTC().Add(idleTimeout).Format(database.Timestamp)
	if next > absolute {
		next = absolute
	}

	if _, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET expires_at = ?, last_seen_at = `+nowExpr+` WHERE id = ?`,
		next, sessionID,
	); err != nil {
		// Bookkeeping only: the caller is already authenticated.
		return ident, nil //nolint:nilerr // sliding the expiry is not worth failing a request
	}

	return ident, nil
}

// RevokeSession ends one session.
func (s *Store) RevokeSession(ctx context.Context, sessionID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = `+nowExpr+` WHERE id = ? AND revoked_at IS NULL`,
		sessionID,
	)
	if err != nil {
		return fmt.Errorf("store: revoke session: %w", err)
	}

	return nil
}

// RevokeUserSessions ends every session for a user, which is what "sign out
// everywhere" needs.
func (s *Store) RevokeUserSessions(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = `+nowExpr+` WHERE user_id = ? AND revoked_at IS NULL`,
		userID,
	)
	if err != nil {
		return fmt.Errorf("store: revoke user sessions: %w", err)
	}

	return nil
}

// PurgeExpired deletes dead sessions and abandoned login flows, returning how
// many rows went. Safe to run on a timer.
func (s *Store) PurgeExpired(ctx context.Context) (int64, error) {
	var total int64

	for _, q := range []string{
		`DELETE FROM sessions WHERE absolute_expires_at <= ` + nowExpr + `
		   OR (revoked_at IS NOT NULL AND revoked_at <= datetime('now', '-7 days'))`,
		`DELETE FROM oidc_flows WHERE expires_at <= ` + nowExpr,
	} {
		res, err := s.db.ExecContext(ctx, q)
		if err != nil {
			return total, fmt.Errorf("store: purge expired: %w", err)
		}

		if n, err := res.RowsAffected(); err == nil {
			total += n
		}
	}

	return total, nil
}

// HashSecret returns the hex-encoded SHA-256 of a high-entropy secret.
//
// Session cookies and API tokens are 256 bits of randomness, so there is nothing
// to brute-force and a plain digest is the right primitive: lookups stay a single
// indexed read, and the stored value is useless if the database leaks.
func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))

	return hex.EncodeToString(sum[:])
}

func newSessionSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("store: read random: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}

	return s
}
