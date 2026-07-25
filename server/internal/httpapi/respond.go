package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/nls/checkmate/server/internal/store"
)

// maxBodyBytes caps request bodies. Task details are prose, not uploads.
const maxBodyBytes = 1 << 20 // 1 MiB

// errorBody is the shape of every non-2xx response.
//
//	{"error": "validation failed", "fields": {"title": "is required"}}
type errorBody struct {
	Error  string            `json:"error"`
	Fields map[string]string `json:"fields,omitempty"`
}

// listBody wraps a collection with its paging cursor. next_cursor is null on the
// last page.
type listBody[T any] struct {
	Data       []T     `json:"data"`
	NextCursor *string `json:"next_cursor"`
}

// validationError collects per-field problems found before the store is called.
type validationError struct {
	fields map[string]string
}

func newValidationError() *validationError {
	return &validationError{fields: map[string]string{}}
}

// add records a problem for a field. The first problem per field wins.
func (v *validationError) add(field, detail string) {
	if _, seen := v.fields[field]; !seen {
		v.fields[field] = detail
	}
}

func (v *validationError) any() bool { return len(v.fields) > 0 }

func (v *validationError) Error() string {
	return fmt.Sprintf("validation failed: %v", v.fields)
}

func (s *Server) writeJSON(w http.ResponseWriter, r *http.Request, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	if body == nil {
		return
	}

	if err := json.NewEncoder(w).Encode(body); err != nil {
		// The status line is already sent, so all that is left is a record of it.
		s.log.Error("encode response",
			slog.Any("error", err),
			slog.String("path", r.URL.Path),
		)
	}
}

// writeList renders a collection response. A free function rather than a method
// because Go has no generic methods.
func writeList[T any](s *Server, w http.ResponseWriter, r *http.Request, items []T, cursor string) {
	if items == nil {
		items = []T{}
	}

	body := listBody[T]{Data: items}
	if cursor != "" {
		body.NextCursor = &cursor
	}

	s.writeJSON(w, r, http.StatusOK, body)
}

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, status int, message string) {
	s.writeJSON(w, r, status, errorBody{Error: message})
}

// writeStoreError maps a store or validation error onto a status code.
//
// Ownership failures arrive as ErrNotFound, so a caller poking at another user's
// ids gets the same 404 as a caller using an id that never existed: the API
// never confirms that someone else's row is real.
func (s *Server) writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	var (
		invalidRef *store.InvalidRefError
		conflict   *store.ConflictError
		validation *validationError
	)

	switch {
	case errors.Is(err, store.ErrNotFound):
		s.writeError(w, r, http.StatusNotFound, "not found")

	case errors.As(err, &validation):
		s.writeJSON(w, r, http.StatusUnprocessableEntity, errorBody{
			Error:  "validation failed",
			Fields: validation.fields,
		})

	case errors.As(err, &invalidRef):
		s.writeJSON(w, r, http.StatusUnprocessableEntity, errorBody{
			Error:  "validation failed",
			Fields: map[string]string{invalidRef.Field: "not found"},
		})

	case errors.As(err, &conflict):
		s.writeJSON(w, r, http.StatusUnprocessableEntity, errorBody{
			Error:  "validation failed",
			Fields: map[string]string{conflict.Field: conflict.Detail},
		})

	default:
		s.log.Error("unhandled store error",
			slog.Any("error", err),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
		)
		s.writeError(w, r, http.StatusInternalServerError, "internal server error")
	}
}

// decodeBody reads a JSON request body into dst.
//
// Unknown fields are rejected: a client that misspells "planned_on" should be
// told, not silently ignored, which matters most for PATCH where an ignored key
// looks exactly like a successful no-op.
func (s *Server) decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	if ct := r.Header.Get("Content-Type"); ct != "" && !isJSONContentType(ct) {
		s.writeError(w, r, http.StatusUnsupportedMediaType, "expected application/json")

		return errHandled
	}

	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		var (
			syntaxErr *json.SyntaxError
			typeErr   *json.UnmarshalTypeError
			maxErr    *http.MaxBytesError
		)

		switch {
		case errors.Is(err, io.EOF):
			s.writeError(w, r, http.StatusBadRequest, "request body is empty")
		case errors.As(err, &maxErr):
			s.writeError(w, r, http.StatusRequestEntityTooLarge, "request body is too large")
		case errors.As(err, &syntaxErr):
			s.writeError(w, r, http.StatusBadRequest,
				fmt.Sprintf("malformed JSON at byte %d", syntaxErr.Offset))
		case errors.As(err, &typeErr):
			s.writeJSON(w, r, http.StatusBadRequest, errorBody{
				Error:  "malformed JSON",
				Fields: map[string]string{typeErr.Field: "expected " + typeErr.Type.String()},
			})
		default:
			s.writeError(w, r, http.StatusBadRequest, err.Error())
		}

		return errHandled
	}

	// A second value in the stream means the client sent two JSON documents.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		s.writeError(w, r, http.StatusBadRequest, "request body must contain a single JSON object")

		return errHandled
	}

	return nil
}

// errHandled signals that a response has already been written, so the handler
// should return without writing another.
var errHandled = errors.New("httpapi: response already written")

func isJSONContentType(ct string) bool {
	// Drop any ";charset=utf-8" parameter before comparing.
	mediaType, _, _ := strings.Cut(ct, ";")

	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "application/json", "application/merge-patch+json", "text/json":
		return true
	default:
		return false
	}
}
