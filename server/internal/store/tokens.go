package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/nls/checkmate/server/internal/account"
	"github.com/nls/checkmate/server/internal/model"
)

// ErrInvalidToken means the presented token is unknown, revoked or expired.
var ErrInvalidToken = errors.New("store: invalid token")

// TokenInfo describes an API token without exposing anything usable.
type TokenInfo struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Scopes     []string `json:"scopes"`
	LastUsedAt *string  `json:"last_used_at"`
	ExpiresAt  *string  `json:"expires_at"`
	RevokedAt  *string  `json:"revoked_at"`
	CreatedAt  string   `json:"created_at"`
}

// ListTokens returns the caller's tokens. The secret is not stored, so it cannot
// appear here even by accident.
func (s *Store) ListTokens(ctx context.Context, userID string) ([]TokenInfo, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, scopes, last_used_at, expires_at, revoked_at, created_at
		 FROM api_tokens WHERE user_id = ? ORDER BY id DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list tokens: %w", err)
	}
	defer rows.Close()

	out := []TokenInfo{}

	for rows.Next() {
		var (
			info   TokenInfo
			scopes string
		)

		err := rows.Scan(&info.ID, &info.Name, &scopes, &info.LastUsedAt,
			&info.ExpiresAt, &info.RevokedAt, &info.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("store: scan token: %w", err)
		}

		info.Scopes = strings.Fields(scopes)
		out = append(out, info)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list tokens: %w", err)
	}

	return out, nil
}

// GetToken returns one of the caller's tokens.
func (s *Store) GetToken(ctx context.Context, userID, tokenID string) (TokenInfo, error) {
	var (
		info   TokenInfo
		scopes string
	)

	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, scopes, last_used_at, expires_at, revoked_at, created_at
		 FROM api_tokens WHERE id = ? AND user_id = ?`,
		tokenID, userID,
	).Scan(&info.ID, &info.Name, &scopes, &info.LastUsedAt,
		&info.ExpiresAt, &info.RevokedAt, &info.CreatedAt)
	if err != nil {
		return TokenInfo{}, notFoundOr(err, "get token")
	}

	info.Scopes = strings.Fields(scopes)

	return info, nil
}

// IssueToken creates a token for the caller and returns the secret once.
// expiresAt may be empty for a token that does not expire.
func (s *Store) IssueToken(
	ctx context.Context,
	userID, name, scopes, expiresAt string,
) (secret string, info TokenInfo, err error) {
	secret, err = account.CreateTokenWithExpiry(ctx, s.db, userID, name, scopes, expiresAt)
	if err != nil {
		return "", TokenInfo{}, err
	}

	info, err = s.tokenByHash(ctx, userID, account.HashToken(secret))
	if err != nil {
		return "", TokenInfo{}, err
	}

	return secret, info, nil
}

func (s *Store) tokenByHash(ctx context.Context, userID, hash string) (TokenInfo, error) {
	var (
		info   TokenInfo
		scopes string
	)

	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, scopes, last_used_at, expires_at, revoked_at, created_at
		 FROM api_tokens WHERE token_hash = ? AND user_id = ?`,
		hash, userID,
	).Scan(&info.ID, &info.Name, &scopes, &info.LastUsedAt,
		&info.ExpiresAt, &info.RevokedAt, &info.CreatedAt)
	if err != nil {
		return TokenInfo{}, notFoundOr(err, "read issued token")
	}

	info.Scopes = strings.Fields(scopes)

	return info, nil
}

// RevokeToken revokes one of the caller's tokens.
func (s *Store) RevokeToken(ctx context.Context, userID, tokenID string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE api_tokens SET revoked_at = `+nowExpr+`
		 WHERE id = ? AND user_id = ? AND revoked_at IS NULL`,
		tokenID, userID,
	)
	if err != nil {
		return fmt.Errorf("store: revoke token: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: revoke token: %w", err)
	}

	if affected == 0 {
		// Either it is not ours, does not exist, or was already revoked. Confirm
		// which, so revoking twice is not reported as a missing token.
		if _, err := s.GetToken(ctx, userID, tokenID); err != nil {
			return err
		}
	}

	return nil
}

// AuthenticateToken resolves a plaintext bearer token to its owner.
//
// The lookup is by hash, so the database never holds anything that can be
// replayed. Expiry and revocation are filtered in SQL rather than compared in
// Go, keeping "is this token usable" a single condition in one place.
func (s *Store) AuthenticateToken(ctx context.Context, secret string) (model.Identity, error) {
	var (
		ident  model.Identity
		scopes string
	)

	err := s.db.QueryRowContext(ctx, `
		SELECT t.id, t.user_id, t.scopes, u.email, coalesce(u.name, u.email), u.timezone
		FROM api_tokens t
		JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = ?
		  AND t.revoked_at IS NULL
		  AND (t.expires_at IS NULL OR t.expires_at > `+nowExpr+`)`,
		account.HashToken(secret),
	).Scan(&ident.TokenID, &ident.UserID, &scopes, &ident.Email, &ident.Name, &ident.Timezone)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return model.Identity{}, ErrInvalidToken
	case err != nil:
		return model.Identity{}, fmt.Errorf("store: authenticate token: %w", err)
	}

	ident.Scopes = strings.Fields(scopes)

	// Best effort: a failed bookkeeping write must not fail the request.
	if _, err := s.db.ExecContext(ctx,
		`UPDATE api_tokens SET last_used_at = `+nowExpr+` WHERE id = ?`, ident.TokenID,
	); err != nil {
		return ident, nil //nolint:nilerr // last_used_at is diagnostic only
	}

	return ident, nil
}
