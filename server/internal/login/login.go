// Package login runs the federated (OIDC) sign-in dance.
//
// Google authenticates the human; Checkmate issues its own session. Nothing from
// the provider is trusted beyond the verified id token, and account creation is
// gated by an allowlist so a server on the public internet does not hand an
// account to everyone with a Google account.
package login

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/nls/checkmate/server/internal/account"
	"github.com/nls/checkmate/server/internal/config"
	"github.com/nls/checkmate/server/internal/store"
)

// flowTTL bounds how long a login may sit half-finished.
const flowTTL = 15 * time.Minute

// ErrNotAllowed means the address has no account and is not on the allowlist.
var ErrNotAllowed = errors.New("login: this address is not allowed to sign in")

// ErrEmailUnverified means the provider did not vouch for the address.
var ErrEmailUnverified = errors.New("login: the provider did not verify this email address")

// ErrNoProviders means no OIDC provider is configured.
var ErrNoProviders = errors.New("login: no identity provider is configured")

// Provider is one configured OIDC provider.
type Provider struct {
	Name     string
	verifier *oidc.IDTokenVerifier
	oauth    *oauth2.Config
}

// Service starts and finishes logins.
type Service struct {
	store     *store.Store
	cfg       config.Config
	providers map[string]*Provider
}

// New discovers every configured provider. Discovery talks to the issuer, so
// this needs network at startup and fails loudly rather than at first login.
func New(ctx context.Context, st *store.Store, cfg config.Config) (*Service, error) {
	svc := &Service{store: st, cfg: cfg, providers: map[string]*Provider{}}

	if cfg.Google.Configured() {
		p, err := discover(ctx, "google", cfg.Google, cfg.BaseURL)
		if err != nil {
			return nil, err
		}

		svc.providers["google"] = p
	}

	return svc, nil
}

func discover(ctx context.Context, name string, pc config.OIDCProvider, baseURL string) (*Provider, error) {
	provider, err := oidc.NewProvider(ctx, pc.Issuer)
	if err != nil {
		return nil, fmt.Errorf("login: discover %s at %s: %w", name, pc.Issuer, err)
	}

	return &Provider{
		Name: name,
		// Checks the signature against the issuer's JWKS, plus iss, aud and exp.
		verifier: provider.Verifier(&oidc.Config{ClientID: pc.ClientID}),
		oauth: &oauth2.Config{
			ClientID:     pc.ClientID,
			ClientSecret: pc.ClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  RedirectURI(baseURL, name),
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		},
	}, nil
}

// RedirectURI is the callback URL to register with the provider.
func RedirectURI(baseURL, provider string) string {
	return strings.TrimRight(baseURL, "/") + "/auth/callback/" + provider
}

// Providers lists the configured provider names.
func (s *Service) Providers() []string {
	out := make([]string, 0, len(s.providers))
	for name := range s.providers {
		out = append(out, name)
	}

	return out
}

// Enabled reports whether any provider is available.
func (s *Service) Enabled() bool { return len(s.providers) > 0 }

// Begin records a login attempt and returns the provider URL to send the browser to.
func (s *Service) Begin(ctx context.Context, providerName, redirectTo string) (string, error) {
	p, ok := s.providers[providerName]
	if !ok {
		return "", fmt.Errorf("login: unknown provider %q", providerName)
	}

	state, err := randomString()
	if err != nil {
		return "", err
	}

	nonce, err := randomString()
	if err != nil {
		return "", err
	}

	verifier, err := randomString()
	if err != nil {
		return "", err
	}

	err = s.store.BeginFlow(ctx, store.Flow{
		State:        state,
		Nonce:        nonce,
		CodeVerifier: verifier,
		Provider:     providerName,
		RedirectTo:   SafeRedirect(redirectTo),
	}, flowTTL)
	if err != nil {
		return "", err
	}

	// PKCE on the outbound leg too: it costs nothing and stops an intercepted
	// authorization code being redeemed by anyone else.
	challenge := pkceChallenge(verifier)

	return p.oauth.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	), nil
}

// Result is a completed login.
type Result struct {
	UserID     string
	Email      string
	RedirectTo string
	Created    bool
}

// Complete verifies the callback and resolves it to a Checkmate user, creating
// one if the address is allowed.
func (s *Service) Complete(ctx context.Context, state, code string) (Result, error) {
	// Consuming the flow first makes the callback single-use even if what
	// follows fails, so a leaked callback URL cannot be retried.
	flow, err := s.store.ConsumeFlow(ctx, state)
	if err != nil {
		return Result{}, err
	}

	p, ok := s.providers[flow.Provider]
	if !ok {
		return Result{}, fmt.Errorf("login: unknown provider %q", flow.Provider)
	}

	token, err := p.oauth.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", flow.CodeVerifier),
	)
	if err != nil {
		return Result{}, fmt.Errorf("login: exchange code: %w", err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return Result{}, errors.New("login: provider returned no id_token")
	}

	idToken, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return Result{}, fmt.Errorf("login: verify id_token: %w", err)
	}

	// Binds this id token to the flow we started, so one obtained elsewhere
	// cannot be injected into our callback.
	if idToken.Nonce != flow.Nonce {
		return Result{}, errors.New("login: id_token nonce does not match the login attempt")
	}

	var claims struct {
		Subject       string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
	}

	if err := idToken.Claims(&claims); err != nil {
		return Result{}, fmt.Errorf("login: read id_token claims: %w", err)
	}

	if claims.Subject == "" {
		return Result{}, errors.New("login: id_token has no subject claim")
	}

	userID, created, err := s.resolveUser(ctx, flow.Provider, claims.Subject,
		claims.Email, claims.EmailVerified, claims.Name)
	if err != nil {
		return Result{}, err
	}

	return Result{
		UserID:     userID,
		Email:      claims.Email,
		RedirectTo: flow.RedirectTo,
		Created:    created,
	}, nil
}

// resolveUser maps a verified provider identity onto a Checkmate user.
//
// The subject claim is the join key, because it is the only stable identifier a
// provider offers: an email address can be renamed, and on some providers
// reassigned to a different person, so matching on it alone would eventually
// hand one person somebody else's account. Email is used only to attach a
// provider to a pre-existing account, and only when the provider says it is
// verified.
func (s *Service) resolveUser(
	ctx context.Context,
	provider, subject, email string,
	emailVerified bool,
	name string,
) (userID string, created bool, err error) {
	// Already linked: the common path, and it needs no email at all.
	switch userID, err = s.store.FindUserByOIDCSubject(ctx, provider, subject); {
	case err == nil:
		if emailVerified && email != "" {
			if err := s.store.LinkOIDCIdentity(ctx, userID, provider, subject, email); err != nil {
				return "", false, err
			}
		}

		return userID, false, nil
	case !errors.Is(err, store.ErrNotFound):
		return "", false, err
	}

	// A first login needs an address to attach to, and an unverified one is
	// worthless: anyone can claim any address at a provider that does not check.
	if email == "" {
		return "", false, ErrEmailUnverified
	}

	if !emailVerified {
		return "", false, ErrEmailUnverified
	}

	// Existing account, first time through this provider: link them.
	switch userID, err = s.store.FindUserIDByEmail(ctx, email); {
	case err == nil:
		if err := s.store.LinkOIDCIdentity(ctx, userID, provider, subject, email); err != nil {
			return "", false, err
		}

		return userID, false, nil
	case !errors.Is(err, store.ErrNotFound):
		return "", false, err
	}

	// Nobody here yet. An empty allowlist means no provisioning at all, which
	// keeps a fresh public deployment closed until an address is added.
	if !s.cfg.EmailAllowed(email) {
		return "", false, ErrNotAllowed
	}

	displayName := strings.TrimSpace(name)
	if displayName == "" {
		displayName, _, _ = strings.Cut(email, "@")
	}

	user, err := account.CreateUser(ctx, s.store.DB(), email, displayName, s.cfg.Timezone())
	if err != nil {
		return "", false, fmt.Errorf("login: provision user: %w", err)
	}

	if err := s.store.LinkOIDCIdentity(ctx, user.ID, provider, subject, email); err != nil {
		return "", false, err
	}

	return user.ID, true, nil
}

// SafeRedirect keeps only a same-site path, so ?redirect_to= cannot be used to
// bounce a freshly authenticated visitor to an attacker's site.
func SafeRedirect(raw string) string {
	if raw == "" {
		return "/"
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "/"
	}

	// An absolute URL, a scheme-relative //host, or anything not rooted at /
	// is discarded rather than sanitised.
	if parsed.IsAbs() || parsed.Host != "" || !strings.HasPrefix(parsed.Path, "/") {
		return "/"
	}

	if strings.HasPrefix(raw, "//") {
		return "/"
	}

	out := parsed.EscapedPath()
	if parsed.RawQuery != "" {
		out += "?" + parsed.RawQuery
	}

	return out
}

func randomString() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("login: read random: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))

	return base64.RawURLEncoding.EncodeToString(sum[:])
}
