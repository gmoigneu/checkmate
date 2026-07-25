// Package config resolves runtime configuration from the environment.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds every knob the server reads at boot.
type Config struct {
	// Env is "development" or "production"; it only affects log formatting.
	Env string

	// Addr is the listen address, e.g. ":8080".
	Addr string

	// DatabasePath is the sqlite file. Its parent directory is created on boot.
	DatabasePath string

	// AutoMigrate applies pending migrations during boot.
	AutoMigrate bool

	// ShutdownTimeout bounds how long in-flight requests get to finish.
	ShutdownTimeout time.Duration

	// BaseURL is the externally reachable origin, e.g. https://checkmate.example.
	// OIDC redirect URIs are built from it and cookie/CSRF checks compare
	// against it, so it must match what a browser actually sees.
	BaseURL string

	// SecureCookies sets the Secure flag on the session cookie. Defaults to true
	// outside development, where plain-http localhost has to work.
	SecureCookies bool

	// SessionIdleTimeout expires a session that stops being used.
	SessionIdleTimeout time.Duration

	// SessionMaxLifetime is the ceiling an active session cannot slide past.
	SessionMaxLifetime time.Duration

	// AllowedEmails gates account provisioning from a federated login.
	//
	// Empty means no new accounts: existing users can sign in, strangers cannot
	// create anything. That is the safe default for a server on the public
	// internet, where "sign in with Google" would otherwise let anyone with a
	// Google account onto it.
	AllowedEmails []string

	// Google holds the OIDC client credentials. Login is offered only when both
	// the id and the secret are set.
	Google OIDCProvider

	// OAuthEnabled turns on the authorization server that remote MCP clients
	// authenticate against.
	OAuthEnabled bool

	// OAuthAllowDynamicRegistration enables the RFC 7591 endpoint. The MCP draft
	// spec deprecates it in favour of Client ID Metadata Documents, but current
	// clients still rely on it.
	OAuthAllowDynamicRegistration bool

	// OAuthMaxDynamicClients caps open registration so an unauthenticated
	// endpoint cannot be used to fill the database.
	OAuthMaxDynamicClients int
}

// OIDCProvider is one federated identity provider's client credentials.
type OIDCProvider struct {
	Issuer       string
	ClientID     string
	ClientSecret string
}

// Configured reports whether the provider can be used.
func (p OIDCProvider) Configured() bool {
	return p.ClientID != "" && p.ClientSecret != ""
}

// Load reads configuration from CHECKMATE_* environment variables, falling back
// to defaults suitable for local development.
func Load() (Config, error) {
	cfg := Config{
		Env:             env("CHECKMATE_ENV", "development"),
		Addr:            env("CHECKMATE_ADDR", ":8080"),
		DatabasePath:    env("CHECKMATE_DB_PATH", "checkmate.db"),
		AutoMigrate:     true,
		ShutdownTimeout: 15 * time.Second,
	}

	var err error

	if cfg.AutoMigrate, err = envBool("CHECKMATE_AUTO_MIGRATE", true); err != nil {
		return Config{}, err
	}

	if cfg.ShutdownTimeout, err = envDuration("CHECKMATE_SHUTDOWN_TIMEOUT", 15*time.Second); err != nil {
		return Config{}, err
	}

	switch cfg.Env {
	case "development", "production":
	default:
		return Config{}, fmt.Errorf("config: CHECKMATE_ENV must be development or production, got %q", cfg.Env)
	}

	if cfg.SessionIdleTimeout, err = envDuration("CHECKMATE_SESSION_IDLE_TIMEOUT", 14*24*time.Hour); err != nil {
		return Config{}, err
	}

	if cfg.SessionMaxLifetime, err = envDuration("CHECKMATE_SESSION_MAX_LIFETIME", 90*24*time.Hour); err != nil {
		return Config{}, err
	}

	if cfg.SessionMaxLifetime < cfg.SessionIdleTimeout {
		return Config{}, fmt.Errorf(
			"config: CHECKMATE_SESSION_MAX_LIFETIME (%s) is shorter than CHECKMATE_SESSION_IDLE_TIMEOUT (%s)",
			cfg.SessionMaxLifetime, cfg.SessionIdleTimeout)
	}

	cfg.BaseURL = strings.TrimRight(env("CHECKMATE_BASE_URL", defaultBaseURL(cfg.Addr)), "/")

	if err := validateBaseURL(cfg.BaseURL); err != nil {
		return Config{}, err
	}

	if cfg.SecureCookies, err = envBool("CHECKMATE_SECURE_COOKIES", !cfg.Development()); err != nil {
		return Config{}, err
	}

	// Refusing to serve cookies without Secure over https would be safe but
	// useless; the reverse -- sending a session cookie unprotected over the
	// public internet -- is the mistake worth blocking.
	if !cfg.SecureCookies && strings.HasPrefix(cfg.BaseURL, "https://") {
		return Config{}, errors.New(
			"config: CHECKMATE_SECURE_COOKIES=false with an https base URL would send session cookies in the clear")
	}

	if cfg.Env == "production" && !strings.HasPrefix(cfg.BaseURL, "https://") {
		return Config{}, fmt.Errorf(
			"config: CHECKMATE_BASE_URL must be https in production, got %q", cfg.BaseURL)
	}

	cfg.AllowedEmails = parseList(env("CHECKMATE_ALLOWED_EMAILS", ""))

	cfg.Google = OIDCProvider{
		Issuer:       env("CHECKMATE_GOOGLE_ISSUER", "https://accounts.google.com"),
		ClientID:     env("CHECKMATE_GOOGLE_CLIENT_ID", ""),
		ClientSecret: env("CHECKMATE_GOOGLE_CLIENT_SECRET", ""),
	}

	if cfg.OAuthEnabled, err = envBool("CHECKMATE_OAUTH_ENABLED", true); err != nil {
		return Config{}, err
	}

	if cfg.OAuthAllowDynamicRegistration, err = envBool("CHECKMATE_OAUTH_ALLOW_DCR", true); err != nil {
		return Config{}, err
	}

	if cfg.OAuthMaxDynamicClients, err = envInt("CHECKMATE_OAUTH_MAX_DYNAMIC_CLIENTS", 200); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// MCPResource is the canonical RFC 8707 resource identifier for the MCP endpoint.
func (c Config) MCPResource() string { return c.BaseURL + "/mcp" }

// EmailAllowed reports whether an address may have an account provisioned for it.
func (c Config) EmailAllowed(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))

	for _, allowed := range c.AllowedEmails {
		allowed = strings.ToLower(allowed)

		// A leading "@domain.com" entry admits everyone at that domain.
		if strings.HasPrefix(allowed, "@") {
			if strings.HasSuffix(email, allowed) {
				return true
			}

			continue
		}

		if allowed == email {
			return true
		}
	}

	return false
}

// defaultBaseURL guesses a development origin from the listen address.
func defaultBaseURL(addr string) string {
	host, port, found := strings.Cut(addr, ":")
	if !found {
		return "http://localhost"
	}

	if host == "" {
		host = "localhost"
	}

	return "http://" + host + ":" + port
}

func validateBaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("config: CHECKMATE_BASE_URL is not a URL: %w", err)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("config: CHECKMATE_BASE_URL must be http or https, got %q", raw)
	}

	if parsed.Host == "" {
		return fmt.Errorf("config: CHECKMATE_BASE_URL has no host: %q", raw)
	}

	return nil
}

// parseList splits a comma-separated setting into trimmed, non-empty entries.
func parseList(raw string) []string {
	var out []string

	for _, part := range strings.Split(raw, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}

	return out
}

// Development reports whether the server is running in development mode.
func (c Config) Development() bool { return c.Env == "development" }

// Timezone is the default IANA zone for a newly provisioned account.
func (c Config) Timezone() string {
	return env("CHECKMATE_DEFAULT_TIMEZONE", "UTC")
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}

	return fallback
}

func envBool(key string, fallback bool) (bool, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}

	v, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false, fmt.Errorf("config: %s: %w", key, err)
	}

	return v, nil
}

func envInt(key string, fallback int) (int, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}

	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("config: %s: %w", key, err)
	}

	if v < 0 {
		return 0, fmt.Errorf("config: %s cannot be negative", key)
	}

	return v, nil
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}

	v, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("config: %s: %w", key, err)
	}

	return v, nil
}
