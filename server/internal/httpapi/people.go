package httpapi

import (
	"net/http"
	"strings"

	"github.com/nls/checkmate/server/internal/patch"
	"github.com/nls/checkmate/server/internal/store"
)

type personCreateRequest struct {
	Name      string  `json:"name"`
	Email     *string `json:"email"`
	ContextID *string `json:"context_id"`
	Notes     *string `json:"notes"`
}

type personUpdateRequest struct {
	Name      patch.Field[string] `json:"name"`
	Email     patch.Field[string] `json:"email"`
	ContextID patch.Field[string] `json:"context_id"`
	Notes     patch.Field[string] `json:"notes"`
}

func (s *Server) handleListPeople(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	p := newParams(r)

	f := store.PersonFilter{
		ContextID: p.str("context_id"),
		Search:    p.str("q"),
	}
	f.IncludeDeleted, f.Limit, f.Cursor = p.listOptions()

	if err := p.done(); err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	items, cursor, err := s.store.ListPeople(r.Context(), ident.UserID, f)
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	writeList(s, w, r, items, cursor)
}

func (s *Server) handleGetPerson(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	v, err := s.store.GetPerson(r.Context(), ident.UserID, r.PathValue("id"))
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	s.writeJSON(w, r, http.StatusOK, v)
}

func (s *Server) handleCreatePerson(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	var req personCreateRequest
	if err := s.decodeBody(w, r, &req); err != nil {
		return
	}

	v := newValidationError()

	name := strings.TrimSpace(req.Name)
	if name == "" {
		v.add("name", "is required")
	}

	if v.any() {
		s.writeStoreError(w, r, v)

		return
	}

	created, err := s.store.CreatePerson(r.Context(), ident.UserID, store.PersonCreate{
		Name:      name,
		Email:     req.Email,
		ContextID: req.ContextID,
		Notes:     req.Notes,
	})
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	w.Header().Set("Location", "/v1/people/"+created.ID)
	s.writeJSON(w, r, http.StatusCreated, created)
}

func (s *Server) handleUpdatePerson(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	var req personUpdateRequest
	if err := s.decodeBody(w, r, &req); err != nil {
		return
	}

	v := newValidationError()
	req.Name = checkRequiredStringPatch(v, "name", req.Name)

	if v.any() {
		s.writeStoreError(w, r, v)

		return
	}

	updated, err := s.store.UpdatePerson(r.Context(), ident.UserID, r.PathValue("id"), store.PersonUpdate{
		Name:      req.Name,
		Email:     req.Email,
		ContextID: req.ContextID,
		Notes:     req.Notes,
	})
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	s.writeJSON(w, r, http.StatusOK, updated)
}

func (s *Server) handleDeletePerson(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	if err := s.store.DeletePerson(r.Context(), ident.UserID, r.PathValue("id")); err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}
