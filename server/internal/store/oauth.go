package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nls/checkmate/server/internal/database"
	"github.com/nls/checkmate/server/internal/id"
	"github.com/nls/checkmate/server/internal/model"
)

// OAuth store errors.
var (
	// ErrUnknownClient means no client is registered under that id.
	ErrUnknownClient = errors.New("store: unknown oauth client")

	// ErrUnknownCode means the authorization code is unknown or expired.
	ErrUnknownCode = errors.New("store: unknown authorization code")

	// ErrRefreshReused means an already-rotated refresh token was presented
	// again, which is the signature of a leaked token.
	ErrRefreshReused = errors.New("store: refresh token was already used")

	// ErrUnknownRefresh means the refresh token is unknown, revoked or expired.
	ErrUnknownRefresh = errors.New("store: unknown refresh token")
)

// OAuthClient is a registered client.
type OAuthClient struct {
	ID                      string   `json:"client_id"`
	Kind                    string   `json:"-"`
	Name                    string   `json:"client_name"`
	ClientURI               *string  `json:"client_uri,omitempty"`
	LogoURI                 *string  `json:"logo_uri,omitempty"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	Scope                   *string  `json:"scope,omitempty"`
	ApplicationType         string   `json:"application_type"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	SoftwareID              *string  `json:"software_id,omitempty"`
	SoftwareVersion         *string  `json:"software_version,omitempty"`
	MetadataExpiresAt       *string  `json:"-"`
	CreatedAt               string   `json:"-"`

	secretHash *string
}

// Public reports whether the client authenticates with no secret, which is the
// normal case for MCP clients running on a desktop.
func (c OAuthClient) Public() bool { return c.TokenEndpointAuthMethod == "none" }

// SecretHash returns the stored secret hash, if the client is confidential.
func (c OAuthClient) SecretHash() string {
	if c.secretHash == nil {
		return ""
	}

	return *c.secretHash
}

// AllowsRedirectURI reports whether uri is registered, compared exactly.
//
// Exact comparison is required by OAuth 2.1: prefix or wildcard matching is how
// open-redirect bugs get in.
func (c OAuthClient) AllowsRedirectURI(uri string) bool {
	for _, registered := range c.RedirectURIs {
		if registered == uri {
			return true
		}
	}

	return false
}

const oauthClientColumns = `id, kind, client_name, client_uri, logo_uri, redirect_uris,
	grant_types, response_types, scope, application_type, token_endpoint_auth_method,
	client_secret_hash, software_id, software_version, metadata_expires_at, created_at`

// UpsertOAuthClient inserts or replaces a client.
//
// Replacement is what refreshes a cached Client ID Metadata Document: the
// client_id is the document URL, so re-fetching updates the same row.
func (s *Store) UpsertOAuthClient(ctx context.Context, c OAuthClient, secretHash string) error {
	redirects, err := json.Marshal(c.RedirectURIs)
	if err != nil {
		return fmt.Errorf("store: encode redirect_uris: %w", err)
	}

	grants, err := json.Marshal(c.GrantTypes)
	if err != nil {
		return fmt.Errorf("store: encode grant_types: %w", err)
	}

	responses, err := json.Marshal(c.ResponseTypes)
	if err != nil {
		return fmt.Errorf("store: encode response_types: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO oauth_clients (id, kind, client_name, client_uri, logo_uri, redirect_uris,
			grant_types, response_types, scope, application_type, token_endpoint_auth_method,
			client_secret_hash, software_id, software_version,
			metadata_fetched_at, metadata_expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, `+nowExpr+`, ?)
		 ON CONFLICT (id) DO UPDATE SET
			client_name = excluded.client_name,
			client_uri = excluded.client_uri,
			logo_uri = excluded.logo_uri,
			redirect_uris = excluded.redirect_uris,
			grant_types = excluded.grant_types,
			response_types = excluded.response_types,
			scope = excluded.scope,
			application_type = excluded.application_type,
			token_endpoint_auth_method = excluded.token_endpoint_auth_method,
			software_id = excluded.software_id,
			software_version = excluded.software_version,
			metadata_fetched_at = excluded.metadata_fetched_at,
			metadata_expires_at = excluded.metadata_expires_at,
			updated_at = `+nowExpr,
		c.ID, c.Kind, c.Name, c.ClientURI, c.LogoURI, string(redirects),
		string(grants), string(responses), c.Scope, c.ApplicationType, c.TokenEndpointAuthMethod,
		nullIfEmpty(secretHash), c.SoftwareID, c.SoftwareVersion, c.MetadataExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("store: upsert oauth client: %w", err)
	}

	return nil
}

// GetOAuthClient loads a client by id.
func (s *Store) GetOAuthClient(ctx context.Context, clientID string) (OAuthClient, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+oauthClientColumns+` FROM oauth_clients WHERE id = ?`, clientID)

	c, err := scanOAuthClient(row)
	if errors.Is(err, ErrNotFound) {
		return OAuthClient{}, ErrUnknownClient
	}

	return c, err
}

// CountOAuthClients reports how many clients exist of a kind, so open dynamic
// registration can be capped.
func (s *Store) CountOAuthClients(ctx context.Context, kind string) (int, error) {
	var n int

	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM oauth_clients WHERE kind = ?`, kind).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count oauth clients: %w", err)
	}

	return n, nil
}

func scanOAuthClient(sc scanner) (OAuthClient, error) {
	var (
		c                            OAuthClient
		redirects, grants, responses string
	)

	err := sc.Scan(&c.ID, &c.Kind, &c.Name, &c.ClientURI, &c.LogoURI, &redirects,
		&grants, &responses, &c.Scope, &c.ApplicationType, &c.TokenEndpointAuthMethod,
		&c.secretHash, &c.SoftwareID, &c.SoftwareVersion, &c.MetadataExpiresAt, &c.CreatedAt)
	if err != nil {
		return OAuthClient{}, notFoundOr(err, "scan oauth client")
	}

	for raw, dst := range map[string]*[]string{
		redirects: &c.RedirectURIs,
		grants:    &c.GrantTypes,
		responses: &c.ResponseTypes,
	} {
		if err := json.Unmarshal([]byte(raw), dst); err != nil {
			return OAuthClient{}, fmt.Errorf("store: decode oauth client json: %w", err)
		}
	}

	return c, nil
}

// AuthorizeRequest is a pending authorization awaiting the user's consent.
type AuthorizeRequest struct {
	ID                  string
	ClientID            string
	UserID              string
	RedirectURI         string
	Scope               string
	Resource            string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
}

// CreateAuthorizeRequest parks an authorization request for the consent screen.
func (s *Store) CreateAuthorizeRequest(ctx context.Context, r AuthorizeRequest, ttl time.Duration) (string, error) {
	requestID := id.New()

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO oauth_authorize_requests (id, client_id, user_id, redirect_uri, scope,
			resource, state, code_challenge, code_challenge_method, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		requestID, r.ClientID, r.UserID, r.RedirectURI, r.Scope, r.Resource,
		nullIfEmpty(r.State), r.CodeChallenge, r.CodeChallengeMethod,
		time.Now().UTC().Add(ttl).Format(database.Timestamp),
	)
	if err != nil {
		return "", fmt.Errorf("store: create authorize request: %w", err)
	}

	return requestID, nil
}

// ConsumeAuthorizeRequest fetches and deletes a pending request, scoped to the
// user who is consenting so one user cannot approve another's pending request.
func (s *Store) ConsumeAuthorizeRequest(ctx context.Context, requestID, userID string) (AuthorizeRequest, error) {
	var (
		r     AuthorizeRequest
		state sql.NullString
	)

	err := s.db.QueryRowContext(ctx,
		`DELETE FROM oauth_authorize_requests
		 WHERE id = ? AND user_id = ? AND expires_at > `+nowExpr+`
		 RETURNING id, client_id, user_id, redirect_uri, scope, resource, state,
		           code_challenge, code_challenge_method`,
		requestID, userID,
	).Scan(&r.ID, &r.ClientID, &r.UserID, &r.RedirectURI, &r.Scope, &r.Resource,
		&state, &r.CodeChallenge, &r.CodeChallengeMethod)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return AuthorizeRequest{}, ErrNotFound
	case err != nil:
		return AuthorizeRequest{}, fmt.Errorf("store: consume authorize request: %w", err)
	}

	r.State = state.String

	return r, nil
}

// UpsertGrant records consent and returns the grant id.
func (s *Store) UpsertGrant(ctx context.Context, userID, clientID, scope, audience string) (string, error) {
	var grantID string

	// Scope is replaced rather than merged: the user just consented to this
	// exact set, and silently keeping an older wider set would grant more than
	// what the consent screen showed.
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO oauth_grants (id, user_id, client_id, scope, audience)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (user_id, client_id, audience) WHERE revoked_at IS NULL
		 DO UPDATE SET scope = excluded.scope, updated_at = `+nowExpr+`
		 RETURNING id`,
		id.New(), userID, clientID, scope, audience,
	).Scan(&grantID)
	if err != nil {
		return "", fmt.Errorf("store: upsert grant: %w", err)
	}

	return grantID, nil
}

// GrantSummary describes a consent for the account UI.
type GrantSummary struct {
	ID         string   `json:"id"`
	ClientID   string   `json:"client_id"`
	ClientName string   `json:"client_name"`
	ClientURI  *string  `json:"client_uri"`
	Scopes     []string `json:"scopes"`
	Audience   string   `json:"audience"`
	CreatedAt  string   `json:"created_at"`
	UpdatedAt  string   `json:"updated_at"`
}

// ListGrants returns the caller's live consents.
func (s *Store) ListGrants(ctx context.Context, userID string) ([]GrantSummary, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT g.id, g.client_id, c.client_name, c.client_uri, g.scope, g.audience,
		        g.created_at, g.updated_at
		 FROM oauth_grants g
		 JOIN oauth_clients c ON c.id = g.client_id
		 WHERE g.user_id = ? AND g.revoked_at IS NULL
		 ORDER BY g.id DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list grants: %w", err)
	}
	defer rows.Close()

	out := []GrantSummary{}

	for rows.Next() {
		var (
			g     GrantSummary
			scope string
		)

		if err := rows.Scan(&g.ID, &g.ClientID, &g.ClientName, &g.ClientURI, &scope,
			&g.Audience, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, fmt.Errorf("store: scan grant: %w", err)
		}

		g.Scopes = strings.Fields(scope)
		out = append(out, g)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list grants: %w", err)
	}

	return out, nil
}

// RevokeGrant withdraws consent and kills every token issued under it.
func (s *Store) RevokeGrant(ctx context.Context, userID, grantID string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE oauth_grants SET revoked_at = `+nowExpr+`, updated_at = `+nowExpr+`
			 WHERE id = ? AND user_id = ? AND revoked_at IS NULL`,
			grantID, userID,
		)
		if err != nil {
			return fmt.Errorf("store: revoke grant: %w", err)
		}

		if affected, err := res.RowsAffected(); err != nil {
			return fmt.Errorf("store: revoke grant: %w", err)
		} else if affected == 0 {
			return ErrNotFound
		}

		return revokeGrantTokens(ctx, tx, grantID)
	})
}

// revokeGrantTokens invalidates every credential hanging off a grant.
func revokeGrantTokens(ctx context.Context, q querier, grantID string) error {
	for _, table := range []string{"oauth_access_tokens", "oauth_refresh_tokens"} {
		_, err := q.ExecContext(ctx,
			`UPDATE `+table+` SET revoked_at = `+nowExpr+`
			 WHERE grant_id = ? AND revoked_at IS NULL`,
			grantID,
		)
		if err != nil {
			return fmt.Errorf("store: revoke %s: %w", table, err)
		}
	}

	// Outstanding codes must go too, or one could still be redeemed into a
	// fresh token after consent was withdrawn.
	if _, err := q.ExecContext(ctx,
		`DELETE FROM oauth_authorization_codes WHERE grant_id = ?`, grantID,
	); err != nil {
		return fmt.Errorf("store: delete codes: %w", err)
	}

	return nil
}

// CodeParams is an authorization code about to be stored.
type CodeParams struct {
	CodeHash            string
	GrantID             string
	ClientID            string
	UserID              string
	RedirectURI         string
	Scope               string
	Resource            string
	CodeChallenge       string
	CodeChallengeMethod string
}

// CreateAuthorizationCode stores a code hash.
func (s *Store) CreateAuthorizationCode(ctx context.Context, p CodeParams, ttl time.Duration) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO oauth_authorization_codes (code_hash, grant_id, client_id, user_id,
			redirect_uri, scope, resource, code_challenge, code_challenge_method, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.CodeHash, p.GrantID, p.ClientID, p.UserID, p.RedirectURI, p.Scope, p.Resource,
		p.CodeChallenge, p.CodeChallengeMethod,
		time.Now().UTC().Add(ttl).Format(database.Timestamp),
	)
	if err != nil {
		return fmt.Errorf("store: create authorization code: %w", err)
	}

	return nil
}

// ConsumeAuthorizationCode fetches and deletes a code in one statement.
//
// Deleting as part of the read is what makes a code single-use even if two
// requests race: only one DELETE can match the row.
func (s *Store) ConsumeAuthorizationCode(ctx context.Context, codeHash string) (CodeParams, error) {
	var p CodeParams

	err := s.db.QueryRowContext(ctx,
		`DELETE FROM oauth_authorization_codes
		 WHERE code_hash = ? AND expires_at > `+nowExpr+`
		 RETURNING code_hash, grant_id, client_id, user_id, redirect_uri, scope, resource,
		           code_challenge, code_challenge_method`,
		codeHash,
	).Scan(&p.CodeHash, &p.GrantID, &p.ClientID, &p.UserID, &p.RedirectURI, &p.Scope,
		&p.Resource, &p.CodeChallenge, &p.CodeChallengeMethod)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return CodeParams{}, ErrUnknownCode
	case err != nil:
		return CodeParams{}, fmt.Errorf("store: consume authorization code: %w", err)
	}

	return p, nil
}

// TokenPair is a freshly issued access/refresh pair.
type TokenPair struct {
	AccessTokenHash  string
	RefreshTokenHash string
	GrantID          string
	ClientID         string
	UserID           string
	Scope            string
	Audience         string
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
}

// IssueTokens stores an access token and, when a refresh hash is given, its
// refresh token. replacesRefreshID links a rotation to its predecessor.
func (s *Store) IssueTokens(ctx context.Context, p TokenPair, replacesRefreshID string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		accessID := id.New()

		_, err := tx.ExecContext(ctx,
			`INSERT INTO oauth_access_tokens (id, token_hash, grant_id, client_id, user_id,
				scope, audience, expires_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			accessID, p.AccessTokenHash, p.GrantID, p.ClientID, p.UserID, p.Scope, p.Audience,
			p.AccessExpiresAt.UTC().Format(database.Timestamp),
		)
		if err != nil {
			return fmt.Errorf("store: insert access token: %w", err)
		}

		if p.RefreshTokenHash == "" {
			return nil
		}

		refreshID := id.New()

		_, err = tx.ExecContext(ctx,
			`INSERT INTO oauth_refresh_tokens (id, token_hash, grant_id, client_id, user_id,
				scope, audience, expires_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			refreshID, p.RefreshTokenHash, p.GrantID, p.ClientID, p.UserID, p.Scope, p.Audience,
			p.RefreshExpiresAt.UTC().Format(database.Timestamp),
		)
		if err != nil {
			return fmt.Errorf("store: insert refresh token: %w", err)
		}

		if replacesRefreshID != "" {
			_, err = tx.ExecContext(ctx,
				`UPDATE oauth_refresh_tokens SET replaced_by = ? WHERE id = ?`,
				refreshID, replacesRefreshID,
			)
			if err != nil {
				return fmt.Errorf("store: link rotated refresh token: %w", err)
			}
		}

		return nil
	})
}

// RefreshRecord is a refresh token resolved for rotation.
type RefreshRecord struct {
	ID       string
	GrantID  string
	ClientID string
	UserID   string
	Scope    string
	Audience string
}

// ClaimRefreshToken marks a refresh token used and returns its details.
//
// Rotation and replay detection in one step: the UPDATE only matches a token
// that has not been used, so a replayed token falls through to the reuse check
// and takes the whole grant down with it.
func (s *Store) ClaimRefreshToken(ctx context.Context, tokenHash string) (RefreshRecord, error) {
	var r RefreshRecord

	err := s.db.QueryRowContext(ctx,
		`UPDATE oauth_refresh_tokens SET used_at = `+nowExpr+`
		 WHERE token_hash = ?
		   AND used_at IS NULL
		   AND revoked_at IS NULL
		   AND expires_at > `+nowExpr+`
		 RETURNING id, grant_id, client_id, user_id, scope, audience`,
		tokenHash,
	).Scan(&r.ID, &r.GrantID, &r.ClientID, &r.UserID, &r.Scope, &r.Audience)

	if err == nil {
		return r, nil
	}

	if !errors.Is(err, sql.ErrNoRows) {
		return RefreshRecord{}, fmt.Errorf("store: claim refresh token: %w", err)
	}

	// Nothing was claimable. Distinguish "already used" from "never existed":
	// the former means a leaked token is being replayed.
	var grantID string

	lookupErr := s.db.QueryRowContext(ctx,
		`SELECT grant_id FROM oauth_refresh_tokens WHERE token_hash = ? AND used_at IS NOT NULL`,
		tokenHash,
	).Scan(&grantID)

	switch {
	case lookupErr == nil:
		// Revoke the family: if the token leaked, the attacker may already hold
		// a valid successor, and refusing this one request would not stop them.
		if err := s.tx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx,
				`UPDATE oauth_grants SET revoked_at = `+nowExpr+` WHERE id = ? AND revoked_at IS NULL`,
				grantID,
			)
			if err != nil {
				return fmt.Errorf("store: revoke grant on reuse: %w", err)
			}

			return revokeGrantTokens(ctx, tx, grantID)
		}); err != nil {
			return RefreshRecord{}, err
		}

		return RefreshRecord{}, ErrRefreshReused
	case !errors.Is(lookupErr, sql.ErrNoRows):
		return RefreshRecord{}, fmt.Errorf("store: check refresh reuse: %w", lookupErr)
	}

	return RefreshRecord{}, ErrUnknownRefresh
}

// AuthenticateAccessToken resolves an OAuth access token to its bearer.
//
// audienceAllowed is the set of resource identifiers this server answers to; a
// token issued for anything else is rejected, which is the audience validation
// MCP requires of a resource server.
func (s *Store) AuthenticateAccessToken(
	ctx context.Context,
	secret string,
	audienceAllowed []string,
) (model.Identity, error) {
	var (
		ident    model.Identity
		scope    string
		audience string
	)

	err := s.db.QueryRowContext(ctx, `
		SELECT t.id, t.user_id, t.scope, t.audience, t.client_id,
		       u.email, coalesce(u.name, u.email), u.timezone
		FROM oauth_access_tokens t
		JOIN users u ON u.id = t.user_id
		JOIN oauth_grants g ON g.id = t.grant_id
		WHERE t.token_hash = ?
		  AND t.revoked_at IS NULL
		  AND t.expires_at > `+nowExpr+`
		  AND g.revoked_at IS NULL`,
		HashSecret(secret),
	).Scan(&ident.TokenID, &ident.UserID, &scope, &audience, &ident.ClientID,
		&ident.Email, &ident.Name, &ident.Timezone)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return model.Identity{}, ErrInvalidToken
	case err != nil:
		return model.Identity{}, fmt.Errorf("store: authenticate access token: %w", err)
	}

	if !containsString(audienceAllowed, audience) {
		return model.Identity{}, ErrWrongAudience
	}

	ident.Scopes = strings.Fields(scope)
	ident.Audience = audience

	return ident, nil
}

// ErrWrongAudience means the token was issued for a different resource.
var ErrWrongAudience = errors.New("store: token audience does not match this resource")

// RevokeAccessTokenByHash revokes an access token. Reports whether one matched.
func (s *Store) RevokeAccessTokenByHash(ctx context.Context, clientID, tokenHash string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE oauth_access_tokens SET revoked_at = `+nowExpr+`
		 WHERE token_hash = ? AND client_id = ? AND revoked_at IS NULL`,
		tokenHash, clientID,
	)
	if err != nil {
		return false, fmt.Errorf("store: revoke access token: %w", err)
	}

	affected, err := res.RowsAffected()

	return affected > 0, err
}

// RevokeRefreshTokenByHash revokes a refresh token and everything else issued
// under the same grant, since RFC 7009 lets the server cascade.
func (s *Store) RevokeRefreshTokenByHash(ctx context.Context, clientID, tokenHash string) (bool, error) {
	var grantID string

	err := s.db.QueryRowContext(ctx,
		`SELECT grant_id FROM oauth_refresh_tokens WHERE token_hash = ? AND client_id = ?`,
		tokenHash, clientID,
	).Scan(&grantID)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("store: find refresh token: %w", err)
	}

	if err := s.tx(ctx, func(tx *sql.Tx) error {
		return revokeGrantTokens(ctx, tx, grantID)
	}); err != nil {
		return false, err
	}

	return true, nil
}

// PurgeExpiredOAuth deletes dead OAuth rows.
func (s *Store) PurgeExpiredOAuth(ctx context.Context) (int64, error) {
	var total int64

	for _, q := range []string{
		`DELETE FROM oauth_authorization_codes WHERE expires_at <= ` + nowExpr,
		`DELETE FROM oauth_authorize_requests WHERE expires_at <= ` + nowExpr,
		`DELETE FROM oauth_access_tokens WHERE expires_at <= datetime('now', '-1 day')`,
		`DELETE FROM oauth_refresh_tokens WHERE expires_at <= datetime('now', '-1 day')`,
	} {
		res, err := s.db.ExecContext(ctx, q)
		if err != nil {
			return total, fmt.Errorf("store: purge oauth: %w", err)
		}

		if n, err := res.RowsAffected(); err == nil {
			total += n
		}
	}

	return total, nil
}

func containsString(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}

	return false
}
