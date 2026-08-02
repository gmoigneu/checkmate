// Package oauth implements the OAuth 2.1 authorization server that remote MCP
// clients authenticate against.
//
// Built against the MCP authorization spec (stable revision 2025-11-25, with the
// forward-looking additions from the draft that becomes 2026-07-28), which
// selects this subset of OAuth:
//
//   - OAuth 2.1 authorization code grant, PKCE S256 mandatory, no implicit grant
//   - RFC 8414 authorization server metadata
//   - RFC 9728 protected resource metadata
//   - RFC 8707 resource indicators, so tokens are audience-bound
//   - RFC 9207 iss in authorization responses (draft SHOULD, done here already)
//   - Client ID Metadata Documents for registration, with RFC 7591 dynamic
//     registration kept for clients that predate CIMD
//
// Checkmate is both the authorization server and the resource server. Tokens
// issued here are Checkmate's own and are never forwarded upstream: MCP forbids
// token passthrough, which is what turns a resource server into a confused
// deputy.
package oauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Token and code lifetimes.
//
// Access tokens are deliberately short: the spec asks for short-lived tokens to
// limit the damage from a leak, and the refresh flow makes it invisible to a
// working client.
const (
	AccessTokenTTL  = time.Hour
	RefreshTokenTTL = 30 * 24 * time.Hour
	CodeTTL         = time.Minute
	AuthorizeTTL    = 15 * time.Minute
)

// Token prefixes, so a leaked credential is recognisable and the middleware can
// route it to the right table without probing both.
const (
	AccessTokenPrefix  = "cmat_"
	RefreshTokenPrefix = "cmrt_"
)

// Scopes. read and write mirror the device-token scopes, so one enforcement path
// covers both credential kinds.
const (
	ScopeRead  = "read"
	ScopeWrite = "write"

	// ScopeOfflineAccess is how an OIDC-flavoured client asks for a refresh
	// token. Advertised by the authorization server but deliberately absent from
	// the resource server's scopes_supported: a refresh token is not something
	// the resource requires.
	ScopeOfflineAccess = "offline_access"
)

// SupportedScopes is what the authorization server will grant.
var SupportedScopes = []string{ScopeRead, ScopeWrite, ScopeOfflineAccess}

// ResourceScopes is what the protected resource advertises as needed.
var ResourceScopes = []string{ScopeRead, ScopeWrite}

// Error is an OAuth error response as defined by RFC 6749 section 5.2.
type Error struct {
	Code        string
	Description string

	// Status is the HTTP status to use at the token and registration endpoints.
	Status int
}

func (e *Error) Error() string {
	if e.Description == "" {
		return "oauth: " + e.Code
	}

	return "oauth: " + e.Code + ": " + e.Description
}

// Standard OAuth error codes used by this server.
func errInvalidRequest(desc string) *Error {
	return &Error{Code: "invalid_request", Description: desc, Status: 400}
}

func errInvalidClient(desc string) *Error {
	return &Error{Code: "invalid_client", Description: desc, Status: 401}
}

func errInvalidGrant(desc string) *Error {
	return &Error{Code: "invalid_grant", Description: desc, Status: 400}
}

func errInvalidScope(desc string) *Error {
	return &Error{Code: "invalid_scope", Description: desc, Status: 400}
}

func errUnsupportedGrantType(desc string) *Error {
	return &Error{Code: "unsupported_grant_type", Description: desc, Status: 400}
}

// ErrAccessDenied is returned when the user refuses consent.
var ErrAccessDenied = &Error{Code: "access_denied", Description: "the user declined the request", Status: 403}

// VerifyPKCE checks a code verifier against the stored S256 challenge.
//
// Only S256 is accepted: OAuth 2.1 removed "plain", and the MCP spec requires
// S256 whenever the client is technically capable, which every real client is.
func VerifyPKCE(challenge, method, verifier string) error {
	if method != "S256" {
		return errInvalidGrant("unsupported code_challenge_method")
	}

	// RFC 7636 bounds the verifier; a short one would weaken the binding.
	if len(verifier) < 43 || len(verifier) > 128 {
		return errInvalidGrant("code_verifier must be 43 to 128 characters")
	}

	sum := sha256.Sum256([]byte(verifier))
	expected := base64.RawURLEncoding.EncodeToString(sum[:])

	// Constant time, since a timing signal here would leak the challenge.
	if subtle.ConstantTimeCompare([]byte(expected), []byte(challenge)) != 1 {
		return errInvalidGrant("code_verifier does not match the code_challenge")
	}

	return nil
}

// ValidateCodeChallenge checks an inbound PKCE challenge at the authorize step.
func ValidateCodeChallenge(challenge, method string) error {
	if challenge == "" {
		return errInvalidRequest("code_challenge is required; this server requires PKCE")
	}

	if method == "" {
		// RFC 7636 defaults to "plain", which OAuth 2.1 forbids, so an omitted
		// method is an error rather than a silent downgrade.
		return errInvalidRequest("code_challenge_method is required and must be S256")
	}

	if method != "S256" {
		return errInvalidRequest("code_challenge_method must be S256")
	}

	// Base64url of a SHA-256 digest is always 43 characters unpadded.
	if len(challenge) != 43 {
		return errInvalidRequest("code_challenge must be a base64url-encoded SHA-256 digest")
	}

	if _, err := base64.RawURLEncoding.DecodeString(challenge); err != nil {
		return errInvalidRequest("code_challenge must be base64url-encoded without padding")
	}

	return nil
}

// NormalizeScopes filters a requested scope string down to what this server
// grants, preserving the order the client asked in.
//
// Unknown scopes are an error rather than silently dropped: a client that asked
// for something it will not receive should be told, not left assuming it has a
// permission it lacks.
func NormalizeScopes(requested string) (string, error) {
	fields := strings.Fields(requested)
	if len(fields) == 0 {
		// No scope requested means the resource's baseline.
		return strings.Join(ResourceScopes, " "), nil
	}

	var out []string

	for _, scope := range fields {
		if !slices.Contains(SupportedScopes, scope) {
			return "", errInvalidScope(fmt.Sprintf("unknown scope %q", scope))
		}

		if !slices.Contains(out, scope) {
			out = append(out, scope)
		}
	}

	// A token with neither read nor write could do nothing at all, which is
	// almost certainly a client mistake rather than an intent.
	if !slices.Contains(out, ScopeRead) && !slices.Contains(out, ScopeWrite) {
		return "", errInvalidScope("at least one of read or write is required")
	}

	return strings.Join(out, " "), nil
}

// WantsRefreshToken reports whether a refresh token should be issued.
//
// Issued by default rather than only on offline_access: MCP clients are
// long-running and the alternative is sending the user back through consent
// every hour. offline_access is accepted for OIDC-flavoured clients that ask
// explicitly.
func WantsRefreshToken(grantTypes []string) bool {
	return len(grantTypes) == 0 || slices.Contains(grantTypes, "refresh_token")
}

// CanonicalResource normalizes an RFC 8707 resource identifier.
//
// The canonical form has no fragment, no trailing slash, and lowercase scheme
// and host. The spec asks servers to accept uppercase for robustness while
// treating the lowercase form as canonical.
func CanonicalResource(raw string) (string, error) {
	if raw == "" {
		return "", errInvalidRequest("resource is required")
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", errInvalidRequest("resource is not a valid URI")
	}

	if parsed.Scheme == "" || parsed.Host == "" {
		return "", errInvalidRequest("resource must be an absolute URI with a scheme and host")
	}

	if parsed.Fragment != "" || strings.Contains(raw, "#") {
		return "", errInvalidRequest("resource must not contain a fragment")
	}

	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""

	return parsed.String(), nil
}

// ValidateRedirectURI enforces the transport rules OAuth 2.1 puts on redirect
// URIs: https everywhere, except loopback for native clients.
//
// "localhost" is deliberately allowed alongside the literal loopback addresses
// because MCP clients in the wild register it, and the MCP spec's own examples
// use it.
func ValidateRedirectURI(raw, applicationType string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return errInvalidRequest(fmt.Sprintf("redirect_uri %q is not a valid URI", raw))
	}

	if parsed.Fragment != "" {
		return errInvalidRequest("redirect_uri must not contain a fragment")
	}

	switch parsed.Scheme {
	case "https":
		return nil

	case "http":
		if applicationType != "native" {
			return errInvalidRequest("a web client's redirect_uri must use https")
		}

		if !isLoopbackHost(parsed.Hostname()) {
			return errInvalidRequest("an http redirect_uri is only allowed on loopback")
		}

		return nil

	case "":
		return errInvalidRequest("redirect_uri must be absolute")

	default:
		// A reversed-domain private-use scheme (com.example.app:/callback) is how
		// mobile apps receive redirects, and is allowed for native clients only.
		if applicationType != "native" {
			return errInvalidRequest("a web client's redirect_uri must use https")
		}

		if !strings.Contains(parsed.Scheme, ".") {
			return errInvalidRequest(
				"a private-use scheme must be a reversed domain name, e.g. com.example.app")
		}

		return nil
	}
}

func isLoopbackHost(host string) bool {
	switch host {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	default:
		// 127.0.0.0/8 is all loopback.
		return strings.HasPrefix(host, "127.")
	}
}

// IsLoopbackOnly reports whether every redirect URI is a loopback address.
//
// The consent screen warns about these: the MCP security guidance points out
// that a Client ID Metadata Document cannot stop a local impersonator from
// claiming a legitimate client's identity and binding its own loopback port.
func IsLoopbackOnly(redirectURIs []string) bool {
	if len(redirectURIs) == 0 {
		return false
	}

	for _, raw := range redirectURIs {
		parsed, err := url.Parse(raw)
		if err != nil || !isLoopbackHost(parsed.Hostname()) {
			return false
		}
	}

	return true
}

// RedirectHosts lists the distinct hostnames a client can be redirected to, for
// display on the consent screen. The spec requires showing the redirect host,
// since that is what distinguishes a real client from an impersonator.
func RedirectHosts(redirectURIs []string) []string {
	var out []string

	for _, raw := range redirectURIs {
		parsed, err := url.Parse(raw)
		if err != nil {
			continue
		}

		host := parsed.Host
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			host = parsed.Scheme
		} else if host == "" {
			host = parsed.Scheme
		}

		if !slices.Contains(out, host) {
			out = append(out, host)
		}
	}

	return out
}

// subtleCompare compares two hex digests without leaking timing information.
func subtleCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// AsError unwraps an OAuth error, or wraps an unexpected one as server_error.
func AsError(err error) *Error {
	var oauthErr *Error
	if errors.As(err, &oauthErr) {
		return oauthErr
	}

	return &Error{Code: "server_error", Description: "internal server error", Status: 500}
}
