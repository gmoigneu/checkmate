// Package httpapi wires the HTTP surface of Checkmate.
//
// Routing is stdlib net/http: Go's ServeMux handles method+pattern matching and
// path values, which is all this API needs.
//
// Every /v1 route runs behind requireAuth, which is the only place an identity
// enters a request. Handlers pass that user id to the store, which scopes all of
// its SQL by it, so ownership is enforced structurally rather than remembered
// per handler.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/nls/checkmate/server/internal/config"
	"github.com/nls/checkmate/server/internal/login"
	"github.com/nls/checkmate/server/internal/oauth"
	"github.com/nls/checkmate/server/internal/store"
)

// Server holds the dependencies shared by every handler.
type Server struct {
	store   *store.Store
	login   *login.Service
	oauth   *oauth.Service
	cfg     config.Config
	log     *slog.Logger
	version string
}

// New builds a Server. loginSvc may be nil when no identity provider is
// configured, in which case the sign-in routes report 501 and bearer tokens
// remain the only way in. oauthSvc may be nil to disable the authorization
// server entirely.
func New(
	st *store.Store,
	loginSvc *login.Service,
	oauthSvc *oauth.Service,
	cfg config.Config,
	log *slog.Logger,
	version string,
) *Server {
	return &Server{
		store:   st,
		login:   loginSvc,
		oauth:   oauthSvc,
		cfg:     cfg,
		log:     log,
		version: version,
	}
}

// Handler returns the fully wired root handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Unauthenticated by necessity: a health probe should not need a credential,
	// and the sign-in routes are how a caller gets one in the first place.
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /auth/config", s.handleAuthConfig)
	mux.HandleFunc("GET /auth/login/{provider}", s.handleLoginStart)
	mux.HandleFunc("GET /auth/callback/{provider}", s.handleLoginCallback)

	// OAuth discovery and the token endpoints are unauthenticated by definition:
	// they are how a client obtains a credential in the first place.
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.handleAuthorizationServerMetadata)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", s.handleProtectedResourceMetadata)

	// Path-inserted form, per RFC 9728: a client probing for the metadata of a
	// resource at /mcp asks for /.well-known/oauth-protected-resource/mcp.
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/{resource...}",
		s.handleProtectedResourceMetadata)

	mux.HandleFunc("POST /oauth/token", s.handleToken)
	mux.HandleFunc("POST /oauth/revoke", s.handleRevoke)
	mux.HandleFunc("POST /oauth/register", s.handleRegister)

	// The authorize endpoint needs a signed-in human but cannot 401: an
	// unauthenticated visitor is redirected into the sign-in flow instead, so it
	// runs behind optionalAuth rather than requireAuth.
	mux.Handle("GET /oauth/authorize", s.optionalAuth(http.HandlerFunc(s.handleAuthorize)))
	mux.Handle("POST /oauth/authorize", s.requireAuth(http.HandlerFunc(s.handleAuthorizeDecision)))

	api := http.NewServeMux()

	api.HandleFunc("GET /v1/grants", s.handleListGrants)
	api.HandleFunc("DELETE /v1/grants/{id}", s.handleRevokeGrant)

	api.HandleFunc("GET /v1/me", s.handleMe)
	api.HandleFunc("POST /v1/logout", s.handleLogout)
	api.HandleFunc("GET /v1/sources", s.handleListSources)

	api.HandleFunc("GET /v1/tokens", s.handleListTokens)
	api.HandleFunc("POST /v1/tokens", s.handleCreateToken)
	api.HandleFunc("DELETE /v1/tokens/{id}", s.handleRevokeToken)

	for _, res := range []struct {
		path   string
		list   http.HandlerFunc
		create http.HandlerFunc
		get    http.HandlerFunc
		update http.HandlerFunc
		remove http.HandlerFunc
	}{
		{
			"contexts",
			s.handleListContexts, s.handleCreateContext,
			s.handleGetContext, s.handleUpdateContext, s.handleDeleteContext,
		},
		{
			"projects",
			s.handleListProjects, s.handleCreateProject,
			s.handleGetProject, s.handleUpdateProject, s.handleDeleteProject,
		},
		{
			"people",
			s.handleListPeople, s.handleCreatePerson,
			s.handleGetPerson, s.handleUpdatePerson, s.handleDeletePerson,
		},
		{
			"recurrences",
			s.handleListRecurrences, s.handleCreateRecurrence,
			s.handleGetRecurrence, s.handleUpdateRecurrence, s.handleDeleteRecurrence,
		},
		{
			"tasks",
			s.handleListTasks, s.handleCreateTask,
			s.handleGetTask, s.handleUpdateTask, s.handleDeleteTask,
		},
	} {
		api.HandleFunc("GET /v1/"+res.path, res.list)
		api.HandleFunc("POST /v1/"+res.path, res.create)
		api.HandleFunc("GET /v1/"+res.path+"/{id}", res.get)
		api.HandleFunc("PATCH /v1/"+res.path+"/{id}", res.update)
		api.HandleFunc("DELETE /v1/"+res.path+"/{id}", res.remove)
	}

	mux.Handle("/v1/", s.requireAuth(api))

	return s.recoverer(s.requestLogger(mux))
}

type healthResponse struct {
	Status   string `json:"status"`
	Version  string `json:"version"`
	Database string `json:"database"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	res := healthResponse{Status: "ok", Version: s.version, Database: "ok"}
	status := http.StatusOK

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := s.store.DB().PingContext(ctx); err != nil {
		res.Status = "degraded"
		res.Database = "unreachable"
		status = http.StatusServiceUnavailable

		s.log.Error("health check: database unreachable", slog.Any("error", err))
	}

	s.writeJSON(w, r, status, res)
}

// statusRecorder captures the status code so the logger can report it.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}

	n, err := r.ResponseWriter.Write(b)
	r.bytes += n

	return n, err
}

func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}

		// requireAuth runs deeper in the chain and derives its own context, so
		// the identity it resolves is invisible from out here. This holder is
		// created before the descent and filled in on the way down, which is
		// what lets a request be logged with the user it turned out to belong to.
		holder := &callerHolder{}

		next.ServeHTTP(rec, r.WithContext(withCallerHolder(r.Context(), holder)))

		if rec.status == 0 {
			rec.status = http.StatusOK
		}

		attrs := []any{
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Int("bytes", rec.bytes),
			slog.Duration("duration", time.Since(start)),
		}

		// Attribute the request to a user when one was resolved, never to a
		// token value.
		if holder.userID != "" {
			attrs = append(attrs, slog.String("user_id", holder.userID))
		}

		s.log.Info("request", attrs...)
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("panic serving request",
					slog.Any("panic", v),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.String("stack", string(debug.Stack())),
				)

				s.writeError(w, r, http.StatusInternalServerError, "internal server error")
			}
		}()

		next.ServeHTTP(w, r)
	})
}
