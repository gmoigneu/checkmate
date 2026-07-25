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
		SELECT t.id, t.user_id, t.scopes, u.email, u.timezone
		FROM api_tokens t
		JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = ?
		  AND t.revoked_at IS NULL
		  AND (t.expires_at IS NULL OR t.expires_at > `+nowExpr+`)`,
		account.HashToken(secret),
	).Scan(&ident.TokenID, &ident.UserID, &scopes, &ident.Email, &ident.Timezone)

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
