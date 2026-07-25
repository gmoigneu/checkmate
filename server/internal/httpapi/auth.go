package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/nls/checkmate/server/internal/model"
	"github.com/nls/checkmate/server/internal/oauth"
	"github.com/nls/checkmate/server/internal/store"
)

// Scopes granted to API tokens. Read covers every GET; write covers every
// mutation. Session cookies always carry both, since they stand in for the
// owner's own browser.
const (
	ScopeRead  = "read"
	ScopeWrite = "write"
)

type identityKey struct{}

type callerHolderKey struct{}

// callerHolder carries the resolved user id back out to the request logger,
// which sits above the auth middleware and therefore cannot see its context.
// Only ever touched by the one goroutine serving the request.
type callerHolder struct {
	userID string
}

func withCallerHolder(ctx context.Context, h *callerHolder) context.Context {
	return context.WithValue(ctx, callerHolderKey{}, h)
}

// recordCaller notes the authenticated user for the log line.
func recordCaller(ctx context.Context, userID string) {
	if h, ok := ctx.Value(callerHolderKey{}).(*callerHolder); ok {
		h.userID = userID
	}
}

// withIdentity stores the authenticated caller on the request context.
func withIdentity(ctx context.Context, ident model.Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, ident)
}

// identityFrom returns the caller placed on the context by requireAuth.
func identityFrom(ctx context.Context) (model.Identity, bool) {
	ident, ok := ctx.Value(identityKey{}).(model.Identity)

	return ident, ok
}

// caller returns the authenticated identity, writing a 500 and reporting false
// if it is somehow missing. Every resource route runs behind requireAuth, so a
// miss means the router was wired wrong rather than that the client did anything.
func (s *Server) caller(w http.ResponseWriter, r *http.Request) (model.Identity, bool) {
	ident, ok := identityFrom(r.Context())
	if !ok {
		s.log.Error("handler reached without an identity on the context",
			slog.String("path", r.URL.Path))
		s.writeError(w, r, http.StatusInternalServerError, "internal server error")

		return model.Identity{}, false
	}

	return ident, true
}

// requireAuth authenticates a request and enforces the scope its method implies.
//
// Two credential kinds are accepted: a bearer token for native and machine
// clients, and a session cookie for the web UI. Both resolve to the same users
// row, so everything below this point is identical.
//
// This is deliberately the only place a request acquires an identity: handlers
// receive a user id they cannot choose, so no route lets a caller name someone
// else as the owner of their data.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ident, ok := s.authenticate(w, r)
		if !ok {
			return
		}

		recordCaller(r.Context(), ident.UserID)

		// A cookie is attached by the browser automatically, so a cross-site
		// page could drive a state change with it. SameSite=Lax already blocks
		// that for unsafe methods; this is the second lock, and it also covers
		// clients that ignore SameSite. Bearer tokens are exempt because they
		// have to be attached deliberately.
		if ident.ViaCookie() && !isSafeMethod(r.Method) {
			if err := s.checkSameOrigin(r); err != nil {
				s.writeError(w, r, http.StatusForbidden, err.Error())

				return
			}
		}

		required := ScopeWrite
		if isSafeMethod(r.Method) {
			required = ScopeRead
		}

		if !ident.HasScope(required) {
			// 403 with insufficient_scope, per RFC 6750, so an OAuth client
			// knows to step up rather than treating this as fatal.
			s.challenge(w, r, "insufficient_scope",
				"the "+required+" scope is required for this request")
			s.writeError(w, r, http.StatusForbidden, "token is missing the "+required+" scope")

			return
		}

		next.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), ident)))
	})
}

// optionalAuth resolves a credential if one is present but never rejects the
// request. Used by the OAuth authorize endpoint, which redirects an anonymous
// visitor into sign-in rather than answering 401.
func (s *Server) optionalAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(s.sessionCookieName())
		if err != nil || cookie.Value == "" {
			next.ServeHTTP(w, r)

			return
		}

		ident, err := s.store.AuthenticateSession(r.Context(), cookie.Value, s.cfg.SessionIdleTimeout)
		if err != nil {
			next.ServeHTTP(w, r)

			return
		}

		recordCaller(r.Context(), ident.UserID)
		next.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), ident)))
	})
}

// authenticate resolves whichever credential the request carries.
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (model.Identity, bool) {
	if secret, err := bearerToken(r); err == nil {
		ident, err := s.resolveBearer(r, secret)
		if err != nil {
			switch {
			case errors.Is(err, store.ErrWrongAudience):
				// The token is real but was minted for a different resource.
				// MCP requires this to be refused: accepting it is exactly the
				// audience-confusion the resource indicator exists to prevent.
				s.challenge(w, r, "invalid_token",
					"the access token was issued for a different resource")
				s.writeError(w, r, http.StatusUnauthorized,
					"token audience does not match this resource")

			case errors.Is(err, store.ErrInvalidToken):
				s.challenge(w, r, "invalid_token",
					"the access token is invalid or expired")
				s.writeError(w, r, http.StatusUnauthorized, "invalid or expired token")

			default:
				s.log.Error("authenticate bearer token", slog.Any("error", err))
				s.challenge(w, r, "invalid_token", "")
				s.writeError(w, r, http.StatusUnauthorized, "invalid or expired token")
			}

			return model.Identity{}, false
		}

		return ident, true
	}

	if cookie, err := r.Cookie(s.sessionCookieName()); err == nil && cookie.Value != "" {
		ident, err := s.store.AuthenticateSession(r.Context(), cookie.Value, s.cfg.SessionIdleTimeout)
		if err != nil {
			if !errors.Is(err, store.ErrInvalidSession) {
				s.log.Error("authenticate session", slog.Any("error", err))
			}

			// Clear the dead cookie so the browser stops presenting it.
			s.clearSessionCookie(w)
			s.writeError(w, r, http.StatusUnauthorized, "session is invalid or expired")

			return model.Identity{}, false
		}

		return ident, true
	}

	s.challenge(w, r, "", "")
	s.writeError(w, r, http.StatusUnauthorized, "authentication required")

	return model.Identity{}, false
}

// resolveBearer routes a bearer token to the table that issued it.
//
// Prefix dispatch rather than probing both tables: the credential kinds have
// different lifecycles, and an OAuth token additionally has to pass audience
// validation, which a device token has no concept of.
func (s *Server) resolveBearer(r *http.Request, secret string) (model.Identity, error) {
	if strings.HasPrefix(secret, oauth.AccessTokenPrefix) {
		if s.oauth == nil {
			return model.Identity{}, store.ErrInvalidToken
		}

		return s.store.AuthenticateAccessToken(r.Context(), secret, s.oauth.AcceptedAudiences())
	}

	// A refresh token is not a credential for the API, and silently failing the
	// lookup would leave a confusing 401 for an easy client mistake.
	if strings.HasPrefix(secret, oauth.RefreshTokenPrefix) {
		return model.Identity{}, store.ErrInvalidToken
	}

	return s.store.AuthenticateToken(r.Context(), secret)
}

// challenge writes the WWW-Authenticate header MCP requires on 401 and 403.
//
// resource_metadata points a client at the RFC 9728 document, which is how it
// discovers the authorization server without being configured with it. The scope
// parameter tells it what to ask for, so it does not have to request everything.
//
// The advertised scope is derived from the request method, not from the status.
// Naming "read" on an unauthenticated write would send the client through
// authorization, hand it a read-only token, and fail its very next request with
// insufficient_scope: a whole extra round trip through the user for no reason.
func (s *Server) challenge(w http.ResponseWriter, r *http.Request, errCode, description string) {
	params := []string{`Bearer realm="checkmate"`}

	if errCode != "" {
		params = append(params, `error="`+errCode+`"`)
	}

	if description != "" {
		params = append(params, `error_description="`+sanitizeHeaderValue(description)+`"`)
	}

	if s.oauth != nil {
		params = append(params,
			`resource_metadata="`+s.cfg.BaseURL+`/.well-known/oauth-protected-resource"`)

		// A write needs both: read to see what it changed, write to change it.
		// Challenging incrementally would force a second step-up.
		scope := strings.Join(oauth.ResourceScopes, " ")
		if isSafeMethod(r.Method) {
			scope = oauth.ScopeRead
		}

		params = append(params, `scope="`+scope+`"`)
	}

	w.Header().Set("WWW-Authenticate", strings.Join(params, ", "))
}

// sanitizeHeaderValue strips characters that would break out of a quoted header
// parameter or inject a new header line.
func sanitizeHeaderValue(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '"', '\\', '\r', '\n':
			return -1
		default:
			return r
		}
	}, s)
}

// checkSameOrigin rejects a cookie-authenticated mutation that did not come from
// our own origin.
//
// Fetch metadata is preferred when present because the browser computes it and a
// page cannot forge it. Origin is the fallback. A request carrying neither is
// refused rather than trusted: that is the shape a CSRF attempt takes from an
// older client.
func (s *Server) checkSameOrigin(r *http.Request) error {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return nil
	case "cross-site", "same-site":
		return errors.New("cross-origin request refused")
	}

	origin := r.Header.Get("Origin")
	if origin == "" {
		return errors.New("Origin header is required for cookie-authenticated writes")
	}

	if !strings.EqualFold(strings.TrimRight(origin, "/"), s.cfg.BaseURL) {
		return errors.New("cross-origin request refused")
	}

	return nil
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

// errNoBearer means the Authorization header was absent or not a bearer token.
var errNoBearer = errors.New("httpapi: no bearer token")

func bearerToken(r *http.Request) (string, error) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", errNoBearer
	}

	scheme, value, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", errNoBearer
	}

	value = strings.TrimSpace(value)
	if value == "" {
		return "", errNoBearer
	}

	return value, nil
}

// sessionCookieName picks the cookie name matching the Secure setting. Browsers
// reject a __Host- prefixed cookie that is not Secure, so development over plain
// http needs the unprefixed name.
func (s *Server) sessionCookieName() string {
	if s.cfg.SecureCookies {
		return store.SessionCookieName
	}

	return store.SessionCookieNameInsecure
}

func (s *Server) setSessionCookie(w http.ResponseWriter, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.sessionCookieName(),
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.SecureCookies,
		SameSite: http.SameSiteLaxMode,
		// No Expires: a session cookie dies with the browser session, and the
		// server-side row is the real lifetime.
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.sessionCookieName(),
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.SecureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}
