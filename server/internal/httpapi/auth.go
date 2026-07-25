package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/nls/checkmate/server/internal/model"
)

// Scopes granted to API tokens. Read covers every GET; write covers every
// mutation. OAuth may add finer grains later, but a personal system does not
// need per-resource scopes to start with.
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

// requireAuth authenticates a bearer token and enforces the read/write scope
// implied by the request method.
//
// This is deliberately the only place a request acquires an identity: handlers
// receive a user id they cannot choose, so there is no route on which a caller
// can name someone else as the owner of their data.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret, err := bearerToken(r)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="checkmate"`)
			s.writeError(w, r, http.StatusUnauthorized, "missing or malformed bearer token")

			return
		}

		ident, err := s.store.AuthenticateToken(r.Context(), secret)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="checkmate", error="invalid_token"`)
			s.writeError(w, r, http.StatusUnauthorized, "invalid or expired token")

			return
		}

		required := ScopeWrite
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			required = ScopeRead
		}

		recordCaller(r.Context(), ident.UserID)

		if !ident.HasScope(required) {
			s.writeError(w, r, http.StatusForbidden, "token is missing the "+required+" scope")

			return
		}

		next.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), ident)))
	})
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
