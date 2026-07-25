package httpapi

import (
	"errors"
	"net/http"

	"github.com/nls/checkmate/server/internal/patch"
	"github.com/nls/checkmate/server/internal/store"
)

type profileUpdateRequest struct {
	Name     patch.Field[string] `json:"name"`
	Timezone patch.Field[string] `json:"timezone"`
}

// handleUpdateMe changes the caller's own profile.
//
// Only name and timezone. Email is the join key for a federated identity, so
// changing it would move which Google account owns the data; that belongs in its
// own deliberate flow, not a profile field.
//
// Timezone is the reason this endpoint exists at all: it decides which day "today"
// is for the brief, and an account provisioned with the wrong default previously
// had no way to correct it from any client.
func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	var req profileUpdateRequest
	if err := s.decodeBody(w, r, &req); err != nil {
		return
	}

	v := newValidationError()
	req.Name = checkRequiredStringPatch(v, "name", req.Name)
	checkTimezonePatch(v, "timezone", req.Timezone)

	if v.any() {
		s.writeStoreError(w, r, v)

		return
	}

	updated, err := s.store.UpdateUserProfile(r.Context(), ident.UserID, store.UserUpdate{
		Name:     req.Name,
		Timezone: req.Timezone,
	})
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	// Same shape as GET /v1/me, so a client can replace what it cached without
	// reconciling two different profile representations.
	via := "token"
	if ident.ViaCookie() {
		via = "session"
	}

	s.writeJSON(w, r, http.StatusOK, meResponse{
		UserID:   updated.ID,
		Email:    updated.Email,
		Name:     updated.Name,
		Timezone: updated.Timezone,
		AuthVia:  via,
		Scopes:   ident.Scopes,
	})
}

type mergePersonRequest struct {
	Into string `json:"into"`
}

// handleMergePerson folds one person into another.
func (s *Server) handleMergePerson(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	var req mergePersonRequest
	if err := s.decodeBody(w, r, &req); err != nil {
		return
	}

	if req.Into == "" {
		v := newValidationError()
		v.add("into", "is required")
		s.writeStoreError(w, r, v)

		return
	}

	sourceID := r.PathValue("id")

	moved, err := s.store.MergePeople(r.Context(), ident.UserID, sourceID, req.Into)
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	person, err := s.store.GetPerson(r.Context(), ident.UserID, req.Into)
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"person": person,

		// How many tasks moved, so a client can say "3 tasks moved to Marc"
		// rather than reporting a silent success.
		"tasks_moved": moved,
		"merged_id":   sourceID,
	})
}

// handleRestoreTask undoes a task deletion.
func (s *Server) handleRestoreTask(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	restored, err := s.store.RestoreTask(r.Context(), ident.UserID, r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrNotDeleted) {
			// Not an error worth failing over: the caller wanted the task alive and
			// it is. Reporting 409 would make an idempotent retry look broken.
			v := newValidationError()
			v.add("id", "that task is not deleted")
			s.writeStoreError(w, r, v)

			return
		}

		s.writeStoreError(w, r, err)

		return
	}

	s.writeJSON(w, r, http.StatusOK, restored)
}

// checkColor validates a context colour.
//
// Enforced here rather than by a CHECK constraint on purpose: adding one would mean
// rebuilding the contexts table, and with foreign keys on, DROP TABLE fires
// ON DELETE CASCADE into projects, recurrences and tasks. The column stays free text
// in the schema and is validated at the edge.
func checkColor(v *validationError, field string, value *string) {
	if value == nil || *value == "" {
		return
	}

	if !validHexColor(*value) {
		v.add(field, "must be a 7-character hex colour such as #6b46c1")
	}
}

// checkColorPatch validates a colour on update, where null clears it.
func checkColorPatch(v *validationError, field string, f patch.Field[string]) {
	if !f.Present() || f.Value == "" {
		return
	}

	if !validHexColor(f.Value) {
		v.add(field, "must be a 7-character hex colour such as #6b46c1")
	}
}

// validHexColor accepts exactly #rrggbb.
//
// Three-digit shorthand and eight-digit alpha are rejected rather than normalised:
// a single canonical form means a client never has to handle three spellings of the
// same colour, and alpha has no meaning for a context dot.
func validHexColor(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}

	for _, c := range value[1:] {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}

	return true
}
