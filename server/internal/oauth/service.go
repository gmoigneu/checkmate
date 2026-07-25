package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/nls/checkmate/server/internal/id"
	"github.com/nls/checkmate/server/internal/store"
)

// Service is the authorization server.
type Service struct {
	store    *store.Store
	fetcher  *MetadataFetcher
	issuer   string
	resource string

	// resourceAliases are every identifier this server accepts as its own
	// audience. The bare origin and the MCP endpoint path are both in the set,
	// because clients legitimately name either as the resource.
	resourceAliases []string

	allowDynamicRegistration bool
	maxDynamicClients        int
}

// Config configures the authorization server.
type Config struct {
	// Issuer is this server's issuer identifier, and the base for its endpoints.
	Issuer string

	// Resource is the canonical resource identifier tokens are bound to.
	Resource string

	// ResourceAliases are additional accepted audience values.
	ResourceAliases []string

	// AllowDynamicRegistration enables the deprecated RFC 7591 endpoint.
	AllowDynamicRegistration bool

	// MaxDynamicClients caps open registration. Zero means unlimited.
	MaxDynamicClients int

	// AllowPrivateMetadataHosts lets Client ID Metadata Documents be fetched
	// from loopback and private addresses.
	//
	// This disables the SSRF protection and exists so tests can serve a document
	// from httptest. Nothing in config.Load sets it, and it must not be wired to
	// an environment variable: on a public deployment it would let any client
	// aim this server at internal endpoints.
	AllowPrivateMetadataHosts bool
}

// New builds the authorization server.
func New(st *store.Store, cfg Config) *Service {
	aliases := append([]string{cfg.Resource}, cfg.ResourceAliases...)

	fetcher := NewMetadataFetcher()
	if cfg.AllowPrivateMetadataHosts {
		fetcher = newMetadataFetcherForTests()
	}

	return &Service{
		store:                    st,
		fetcher:                  fetcher,
		issuer:                   strings.TrimRight(cfg.Issuer, "/"),
		resource:                 cfg.Resource,
		resourceAliases:          aliases,
		allowDynamicRegistration: cfg.AllowDynamicRegistration,
		maxDynamicClients:        cfg.MaxDynamicClients,
	}
}

// Issuer returns the issuer identifier.
func (s *Service) Issuer() string { return s.issuer }

// Resource returns the canonical resource identifier.
func (s *Service) Resource() string { return s.resource }

// AcceptedAudiences returns every audience value this resource answers to.
func (s *Service) AcceptedAudiences() []string { return slices.Clone(s.resourceAliases) }

// DynamicRegistrationEnabled reports whether RFC 7591 registration is offered.
func (s *Service) DynamicRegistrationEnabled() bool { return s.allowDynamicRegistration }

// AuthorizeParams is a parsed authorization request.
type AuthorizeParams struct {
	ClientID            string
	RedirectURI         string
	ResponseType        string
	Scope               string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
	Resource            string
}

// PendingAuthorization is what the consent screen needs to render.
type PendingAuthorization struct {
	RequestID     string
	Client        store.OAuthClient
	Scopes        []string
	Resource      string
	RedirectURI   string
	RedirectHosts []string

	// LoopbackWarning is set when every redirect is a loopback address, which
	// the MCP security guidance says the user should be warned about because any
	// local process could be impersonating this client.
	LoopbackWarning bool

	// AlreadyGranted is set when the user previously consented to the same
	// scopes, letting the UI say "reconnect" rather than "authorize".
	AlreadyGranted bool
}

// ResolveClient loads a client, fetching its metadata document if the client_id
// is a URL and the cached copy is missing or stale.
func (s *Service) ResolveClient(ctx context.Context, clientID string) (store.OAuthClient, error) {
	if !IsMetadataDocumentID(clientID) {
		client, err := s.store.GetOAuthClient(ctx, clientID)
		if errors.Is(err, store.ErrUnknownClient) {
			return store.OAuthClient{}, errInvalidClient("unknown client_id")
		}

		return client, err
	}

	// A cached document that has not expired is used as-is.
	cached, err := s.store.GetOAuthClient(ctx, clientID)
	if err == nil && cached.MetadataExpiresAt != nil {
		if expires, parseErr := time.Parse(timestampLayout, *cached.MetadataExpiresAt); parseErr == nil {
			if time.Now().UTC().Before(expires) {
				return cached, nil
			}
		}
	} else if err != nil && !errors.Is(err, store.ErrUnknownClient) {
		return store.OAuthClient{}, err
	}

	meta, err := s.fetcher.Fetch(ctx, clientID)
	if err != nil {
		// A refetch failure on a document we already hold should not break a
		// working client; the cached copy is stale, not wrong.
		if cached.ID != "" {
			return cached, nil
		}

		return store.OAuthClient{}, err
	}

	client := store.OAuthClient{
		ID:                      clientID,
		Kind:                    "cimd",
		Name:                    meta.ClientName,
		RedirectURIs:            meta.RedirectURIs,
		GrantTypes:              meta.GrantTypes,
		ResponseTypes:           meta.ResponseTypes,
		ApplicationType:         meta.ApplicationType,
		TokenEndpointAuthMethod: "none",
		ClientURI:               nullable(meta.ClientURI),
		LogoURI:                 nullable(meta.LogoURI),
		Scope:                   nullable(meta.Scope),
		SoftwareID:              nullable(meta.SoftwareID),
		SoftwareVersion:         nullable(meta.SoftwareVersion),
		MetadataExpiresAt:       nullable(meta.CacheExpiresAt.UTC().Format(timestampLayout)),
	}

	if err := s.store.UpsertOAuthClient(ctx, client, ""); err != nil {
		return store.OAuthClient{}, err
	}

	return client, nil
}

const timestampLayout = "2006-01-02T15:04:05.000Z"

// BeginAuthorization validates an authorization request and parks it for consent.
//
// Errors are split by whether they can be safely redirected: until the client and
// its redirect URI are both verified, an error must be shown to the user rather
// than sent to a URI an attacker may have chosen. That is why client and
// redirect_uri are checked first and returned as non-redirectable.
func (s *Service) BeginAuthorization(
	ctx context.Context,
	p AuthorizeParams,
	userID string,
) (PendingAuthorization, error) {
	if p.ClientID == "" {
		return PendingAuthorization{}, errInvalidRequest("client_id is required")
	}

	client, err := s.ResolveClient(ctx, p.ClientID)
	if err != nil {
		return PendingAuthorization{}, err
	}

	if p.RedirectURI == "" {
		// Only defaulted when the client registered exactly one, per OAuth 2.1;
		// guessing among several would be an open redirect waiting to happen.
		if len(client.RedirectURIs) != 1 {
			return PendingAuthorization{}, errInvalidRequest(
				"redirect_uri is required when the client has more than one registered")
		}

		p.RedirectURI = client.RedirectURIs[0]
	}

	if !client.AllowsRedirectURI(p.RedirectURI) {
		return PendingAuthorization{}, errInvalidRequest(
			"redirect_uri does not exactly match a registered redirect URI")
	}

	// Past this point errors may be delivered to the redirect URI, since it has
	// been proven to belong to the client.

	if p.ResponseType != "code" {
		return PendingAuthorization{}, &Error{
			Code:        "unsupported_response_type",
			Description: "only the code response type is supported",
			Status:      400,
		}
	}

	if err := ValidateCodeChallenge(p.CodeChallenge, p.CodeChallengeMethod); err != nil {
		return PendingAuthorization{}, err
	}

	scope, err := NormalizeScopes(p.Scope)
	if err != nil {
		return PendingAuthorization{}, err
	}

	// RFC 8707. Absent, the token is bound to this server's canonical resource:
	// the spec requires clients to send it, but binding to a sane default is
	// safer than issuing an unbound token to a client that forgot.
	resource := s.resource

	if p.Resource != "" {
		canonical, err := CanonicalResource(p.Resource)
		if err != nil {
			return PendingAuthorization{}, err
		}

		if !slices.Contains(s.resourceAliases, canonical) {
			return PendingAuthorization{}, &Error{
				Code: "invalid_target",
				Description: fmt.Sprintf(
					"this server does not issue tokens for %q; expected %s", canonical, s.resource),
				Status: 400,
			}
		}

		resource = canonical
	}

	requestID, err := s.store.CreateAuthorizeRequest(ctx, store.AuthorizeRequest{
		ClientID:            client.ID,
		UserID:              userID,
		RedirectURI:         p.RedirectURI,
		Scope:               scope,
		Resource:            resource,
		State:               p.State,
		CodeChallenge:       p.CodeChallenge,
		CodeChallengeMethod: "S256",
	}, AuthorizeTTL)
	if err != nil {
		return PendingAuthorization{}, err
	}

	granted, err := s.store.ListGrants(ctx, userID)
	if err != nil {
		return PendingAuthorization{}, err
	}

	alreadyGranted := false

	for _, g := range granted {
		if g.ClientID == client.ID && g.Audience == resource {
			alreadyGranted = true

			break
		}
	}

	return PendingAuthorization{
		RequestID:       requestID,
		Client:          client,
		Scopes:          strings.Fields(scope),
		Resource:        resource,
		RedirectURI:     p.RedirectURI,
		RedirectHosts:   RedirectHosts(client.RedirectURIs),
		LoopbackWarning: IsLoopbackOnly(client.RedirectURIs),
		AlreadyGranted:  alreadyGranted,
	}, nil
}

// CompleteAuthorization records consent and mints an authorization code,
// returning the URL to redirect the user agent to.
func (s *Service) CompleteAuthorization(ctx context.Context, requestID, userID string) (string, error) {
	req, err := s.store.ConsumeAuthorizeRequest(ctx, requestID, userID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", errInvalidRequest("this authorization request has expired; please start again")
		}

		return "", err
	}

	grantID, err := s.store.UpsertGrant(ctx, userID, req.ClientID, req.Scope, req.Resource)
	if err != nil {
		return "", err
	}

	code, err := randomSecret()
	if err != nil {
		return "", err
	}

	err = s.store.CreateAuthorizationCode(ctx, store.CodeParams{
		CodeHash:            store.HashSecret(code),
		GrantID:             grantID,
		ClientID:            req.ClientID,
		UserID:              userID,
		RedirectURI:         req.RedirectURI,
		Scope:               req.Scope,
		Resource:            req.Resource,
		CodeChallenge:       req.CodeChallenge,
		CodeChallengeMethod: req.CodeChallengeMethod,
	}, CodeTTL)
	if err != nil {
		return "", err
	}

	return s.redirectWith(req.RedirectURI, url.Values{
		"code":  {code},
		"state": {req.State},
	}), nil
}

// DenyAuthorization discards a pending request and returns the error redirect.
func (s *Service) DenyAuthorization(ctx context.Context, requestID, userID string) (string, error) {
	req, err := s.store.ConsumeAuthorizeRequest(ctx, requestID, userID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", errInvalidRequest("this authorization request has expired")
		}

		return "", err
	}

	return s.ErrorRedirect(req.RedirectURI, req.State, ErrAccessDenied), nil
}

// ErrorRedirect builds an error response redirect.
func (s *Service) ErrorRedirect(redirectURI, state string, err *Error) string {
	return s.redirectWith(redirectURI, url.Values{
		"error":             {err.Code},
		"error_description": {err.Description},
		"state":             {state},
	})
}

// redirectWith appends parameters to a redirect URI as a query string.
//
// The iss parameter (RFC 9207) goes on every response including errors, so a
// client can tell which authorization server answered. That closes a class of
// mix-up attack the MCP draft spec highlights as more likely in MCP's
// one-client-many-servers world.
func (s *Service) redirectWith(redirectURI string, params url.Values) string {
	parsed, err := url.Parse(redirectURI)
	if err != nil {
		return redirectURI
	}

	query := parsed.Query()

	for key, values := range params {
		if len(values) > 0 && values[0] != "" {
			query.Set(key, values[0])
		}
	}

	query.Set("iss", s.issuer)
	parsed.RawQuery = query.Encode()

	return parsed.String()
}

// TokenRequest is a parsed request to the token endpoint.
type TokenRequest struct {
	GrantType    string
	Code         string
	RedirectURI  string
	CodeVerifier string
	RefreshToken string
	ClientID     string
	ClientSecret string
	Resource     string
	Scope        string
}

// TokenResponse is a successful token endpoint response.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope"`
}

// Token handles the token endpoint.
func (s *Service) Token(ctx context.Context, req TokenRequest) (TokenResponse, error) {
	switch req.GrantType {
	case "authorization_code":
		return s.exchangeCode(ctx, req)
	case "refresh_token":
		return s.refresh(ctx, req)
	case "":
		return TokenResponse{}, errInvalidRequest("grant_type is required")
	default:
		return TokenResponse{}, errUnsupportedGrantType(
			fmt.Sprintf("grant_type %q is not supported", req.GrantType))
	}
}

// authenticateClient verifies the client at the token endpoint.
func (s *Service) authenticateClient(ctx context.Context, req TokenRequest) (store.OAuthClient, error) {
	if req.ClientID == "" {
		return store.OAuthClient{}, errInvalidClient("client_id is required")
	}

	client, err := s.ResolveClient(ctx, req.ClientID)
	if err != nil {
		return store.OAuthClient{}, err
	}

	if client.Public() {
		// A public client has no secret to prove; PKCE is what binds the code to
		// it. One presenting a secret is confused about its own registration.
		if req.ClientSecret != "" {
			return store.OAuthClient{}, errInvalidClient(
				"this client is registered as public and must not send a client_secret")
		}

		return client, nil
	}

	if req.ClientSecret == "" {
		return store.OAuthClient{}, errInvalidClient("client_secret is required for this client")
	}

	if !secretMatches(client.SecretHash(), req.ClientSecret) {
		return store.OAuthClient{}, errInvalidClient("invalid client credentials")
	}

	return client, nil
}

func (s *Service) exchangeCode(ctx context.Context, req TokenRequest) (TokenResponse, error) {
	client, err := s.authenticateClient(ctx, req)
	if err != nil {
		return TokenResponse{}, err
	}

	if req.Code == "" {
		return TokenResponse{}, errInvalidRequest("code is required")
	}

	code, err := s.store.ConsumeAuthorizationCode(ctx, store.HashSecret(req.Code))
	if err != nil {
		if errors.Is(err, store.ErrUnknownCode) {
			return TokenResponse{}, errInvalidGrant("the authorization code is invalid, expired or already used")
		}

		return TokenResponse{}, err
	}

	// A code issued to one client must not be redeemable by another.
	if code.ClientID != client.ID {
		return TokenResponse{}, errInvalidGrant("this authorization code was issued to another client")
	}

	// OAuth 2.1 requires redirect_uri to match the authorization request when it
	// was present there, which stops a code being replayed to a different URI.
	if req.RedirectURI != "" && req.RedirectURI != code.RedirectURI {
		return TokenResponse{}, errInvalidGrant("redirect_uri does not match the authorization request")
	}

	if err := VerifyPKCE(code.CodeChallenge, code.CodeChallengeMethod, req.CodeVerifier); err != nil {
		return TokenResponse{}, err
	}

	// A resource named here must agree with the one the code was bound to, or the
	// client is trying to retarget an approved grant.
	if req.Resource != "" {
		canonical, err := CanonicalResource(req.Resource)
		if err != nil {
			return TokenResponse{}, err
		}

		if canonical != code.Resource {
			return TokenResponse{}, &Error{
				Code:        "invalid_target",
				Description: "resource does not match the authorization request",
				Status:      400,
			}
		}
	}

	return s.issue(ctx, code.GrantID, client, code.UserID, code.Scope, code.Resource, "")
}

func (s *Service) refresh(ctx context.Context, req TokenRequest) (TokenResponse, error) {
	client, err := s.authenticateClient(ctx, req)
	if err != nil {
		return TokenResponse{}, err
	}

	if req.RefreshToken == "" {
		return TokenResponse{}, errInvalidRequest("refresh_token is required")
	}

	record, err := s.store.ClaimRefreshToken(ctx, store.HashSecret(req.RefreshToken))
	if err != nil {
		switch {
		case errors.Is(err, store.ErrRefreshReused):
			// The grant has already been revoked by the store. Reporting
			// invalid_grant tells the client to start a fresh authorization,
			// which is exactly right whether this was theft or a buggy retry.
			return TokenResponse{}, errInvalidGrant(
				"this refresh token was already used; the grant has been revoked, please re-authorize")
		case errors.Is(err, store.ErrUnknownRefresh):
			return TokenResponse{}, errInvalidGrant("the refresh token is invalid, expired or revoked")
		default:
			return TokenResponse{}, err
		}
	}

	if record.ClientID != client.ID {
		return TokenResponse{}, errInvalidGrant("this refresh token was issued to another client")
	}

	scope := record.Scope

	// A refresh may narrow the scope but never widen it.
	if req.Scope != "" {
		requested, err := NormalizeScopes(req.Scope)
		if err != nil {
			return TokenResponse{}, err
		}

		granted := strings.Fields(record.Scope)

		for _, want := range strings.Fields(requested) {
			if !slices.Contains(granted, want) {
				return TokenResponse{}, errInvalidScope(
					fmt.Sprintf("scope %q was not granted; a refresh cannot widen scope", want))
			}
		}

		scope = requested
	}

	return s.issue(ctx, record.GrantID, client, record.UserID, scope, record.Audience, record.ID)
}

// issue mints an access token, plus a refresh token when the client supports it.
func (s *Service) issue(
	ctx context.Context,
	grantID string,
	client store.OAuthClient,
	userID, scope, audience, replacesRefreshID string,
) (TokenResponse, error) {
	accessToken, err := randomSecret()
	if err != nil {
		return TokenResponse{}, err
	}

	accessToken = AccessTokenPrefix + accessToken

	pair := store.TokenPair{
		AccessTokenHash:  store.HashSecret(accessToken),
		GrantID:          grantID,
		ClientID:         client.ID,
		UserID:           userID,
		Scope:            scope,
		Audience:         audience,
		AccessExpiresAt:  time.Now().UTC().Add(AccessTokenTTL),
		RefreshExpiresAt: time.Now().UTC().Add(RefreshTokenTTL),
	}

	var refreshToken string

	if WantsRefreshToken(client.GrantTypes) {
		secret, err := randomSecret()
		if err != nil {
			return TokenResponse{}, err
		}

		refreshToken = RefreshTokenPrefix + secret
		pair.RefreshTokenHash = store.HashSecret(refreshToken)
	}

	if err := s.store.IssueTokens(ctx, pair, replacesRefreshID); err != nil {
		return TokenResponse{}, err
	}

	return TokenResponse{
		AccessToken:  accessToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(AccessTokenTTL.Seconds()),
		RefreshToken: refreshToken,
		Scope:        scope,
	}, nil
}

// Revoke implements RFC 7009. It is deliberately silent about whether the token
// existed, since telling a caller that would be a probing oracle.
func (s *Service) Revoke(ctx context.Context, clientID, token, hint string) error {
	if token == "" {
		return errInvalidRequest("token is required")
	}

	hash := store.HashSecret(token)

	// Try the hinted table first, then the other one.
	order := []string{"access_token", "refresh_token"}
	if hint == "refresh_token" {
		order = []string{"refresh_token", "access_token"}
	}

	for _, kind := range order {
		var (
			found bool
			err   error
		)

		if kind == "access_token" {
			found, err = s.store.RevokeAccessTokenByHash(ctx, clientID, hash)
		} else {
			found, err = s.store.RevokeRefreshTokenByHash(ctx, clientID, hash)
		}

		if err != nil {
			return err
		}

		if found {
			return nil
		}
	}

	return nil
}

// RegisterDynamic implements RFC 7591 registration.
func (s *Service) RegisterDynamic(ctx context.Context, meta ClientMetadata) (store.OAuthClient, string, error) {
	if !s.allowDynamicRegistration {
		return store.OAuthClient{}, "", &Error{
			Code:        "invalid_request",
			Description: "dynamic client registration is disabled; use a Client ID Metadata Document",
			Status:      403,
		}
	}

	if s.maxDynamicClients > 0 {
		count, err := s.store.CountOAuthClients(ctx, "dynamic")
		if err != nil {
			return store.OAuthClient{}, "", err
		}

		if count >= s.maxDynamicClients {
			return store.OAuthClient{}, "", &Error{
				Code:        "invalid_request",
				Description: "this server has reached its dynamic client registration limit",
				Status:      403,
			}
		}
	}

	if strings.TrimSpace(meta.ClientName) == "" {
		return store.OAuthClient{}, "", errInvalidRequest("client_name is required")
	}

	if len(meta.RedirectURIs) == 0 {
		return store.OAuthClient{}, "", errInvalidRequest("redirect_uris is required")
	}

	if meta.ApplicationType == "" {
		meta.ApplicationType = "native"
	}

	if meta.ApplicationType != "native" && meta.ApplicationType != "web" {
		return store.OAuthClient{}, "", errInvalidRequest("application_type must be native or web")
	}

	for _, uri := range meta.RedirectURIs {
		if err := ValidateRedirectURI(uri, meta.ApplicationType); err != nil {
			return store.OAuthClient{}, "", err
		}
	}

	if len(meta.GrantTypes) == 0 {
		meta.GrantTypes = []string{"authorization_code", "refresh_token"}
	}

	for _, grant := range meta.GrantTypes {
		if grant != "authorization_code" && grant != "refresh_token" {
			return store.OAuthClient{}, "", errInvalidRequest(
				fmt.Sprintf("unsupported grant_type %q", grant))
		}
	}

	if len(meta.ResponseTypes) == 0 {
		meta.ResponseTypes = []string{"code"}
	}

	for _, responseType := range meta.ResponseTypes {
		if responseType != "code" {
			return store.OAuthClient{}, "", errInvalidRequest(
				"only the code response_type is supported")
		}
	}

	authMethod := meta.TokenEndpointAuthMethod
	if authMethod == "" {
		authMethod = "none"
	}

	var secret, secretHash string

	if authMethod != "none" {
		if authMethod != "client_secret_basic" && authMethod != "client_secret_post" {
			return store.OAuthClient{}, "", errInvalidRequest(
				"token_endpoint_auth_method must be none, client_secret_basic or client_secret_post")
		}

		generated, err := randomSecret()
		if err != nil {
			return store.OAuthClient{}, "", err
		}

		secret = generated
		secretHash = store.HashSecret(secret)
	}

	if meta.Scope != "" {
		if _, err := NormalizeScopes(meta.Scope); err != nil {
			return store.OAuthClient{}, "", err
		}
	}

	client := store.OAuthClient{
		ID:                      id.New(),
		Kind:                    "dynamic",
		Name:                    meta.ClientName,
		RedirectURIs:            meta.RedirectURIs,
		GrantTypes:              meta.GrantTypes,
		ResponseTypes:           meta.ResponseTypes,
		ApplicationType:         meta.ApplicationType,
		TokenEndpointAuthMethod: authMethod,
		ClientURI:               nullable(meta.ClientURI),
		LogoURI:                 nullable(meta.LogoURI),
		Scope:                   nullable(meta.Scope),
		SoftwareID:              nullable(meta.SoftwareID),
		SoftwareVersion:         nullable(meta.SoftwareVersion),
	}

	if err := s.store.UpsertOAuthClient(ctx, client, secretHash); err != nil {
		return store.OAuthClient{}, "", err
	}

	return client, secret, nil
}

func randomSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("oauth: read random: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func secretMatches(storedHash, presented string) bool {
	if storedHash == "" {
		return false
	}

	return subtleCompare(storedHash, store.HashSecret(presented))
}

func nullable(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}

	return &s
}
