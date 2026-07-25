// Package httpapi wires the HTTP surface of Checkmate.
//
// Routing is stdlib net/http: Go's ServeMux handles method+pattern matching and
// path values, which is all this API needs.
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

// Server holds the dependencies shared by every handler.
type Server struct {
	db      *sql.DB
	log     *slog.Logger
	version string
}

// New builds a Server. version is reported by the health endpoint.
func New(db *sql.DB, log *slog.Logger, version string) *Server {
	return &Server{db: db, log: log, version: version}
}

// Handler returns the fully wired root handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealth)

	// Resource routes land here as they are built: /v1/tasks, /v1/contexts,
	// /v1/projects, /v1/people, /v1/recurrences, /v1/sync.

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

	if err := s.db.PingContext(ctx); err != nil {
		res.Status = "degraded"
		res.Database = "unreachable"
		status = http.StatusServiceUnavailable

		s.log.Error("health check: database unreachable", slog.Any("error", err))
	}

	writeJSON(w, status, res)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	if body == nil {
		return
	}

	// The response is already committed by WriteHeader, so a failure here can
	// only be logged by the caller's middleware, not corrected.
	_ = json.NewEncoder(w).Encode(body)
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

		next.ServeHTTP(rec, r)

		if rec.status == 0 {
			rec.status = http.StatusOK
		}

		s.log.Info("request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Int("bytes", rec.bytes),
			slog.Duration("duration", time.Since(start)),
		)
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

				writeJSON(w, http.StatusInternalServerError, map[string]string{
					"error": "internal server error",
				})
			}
		}()

		next.ServeHTTP(w, r)
	})
}
