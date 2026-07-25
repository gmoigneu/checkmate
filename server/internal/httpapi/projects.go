package httpapi

import (
	"net/http"
	"strings"

	"github.com/nls/checkmate/server/internal/model"
	"github.com/nls/checkmate/server/internal/patch"
	"github.com/nls/checkmate/server/internal/store"
)

type projectCreateRequest struct {
	ContextID   string  `json:"context_id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Status      string  `json:"status"`
}

type projectUpdateRequest struct {
	ContextID   patch.Field[string] `json:"context_id"`
	Name        patch.Field[string] `json:"name"`
	Description patch.Field[string] `json:"description"`
	Status      patch.Field[string] `json:"status"`
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	p := newParams(r)

	f := store.ProjectFilter{
		ContextID: p.str("context_id"),
		Status:    p.csv("status", model.ProjectStatuses),
	}
	f.IncludeDeleted, f.Limit, f.Cursor = p.listOptions()

	if err := p.done(); err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	items, cursor, err := s.store.ListProjects(r.Context(), ident.UserID, f)
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	writeList(s, w, r, items, cursor)
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	v, err := s.store.GetProject(r.Context(), ident.UserID, r.PathValue("id"))
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	s.writeJSON(w, r, http.StatusOK, v)
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	var req projectCreateRequest
	if err := s.decodeBody(w, r, &req); err != nil {
		return
	}

	v := newValidationError()

	name := strings.TrimSpace(req.Name)
	if name == "" {
		v.add("name", "is required")
	}

	if strings.TrimSpace(req.ContextID) == "" {
		v.add("context_id", "is required")
	}

	checkEnum(v, "status", req.Status, model.ProjectStatuses)

	if v.any() {
		s.writeStoreError(w, r, v)

		return
	}

	created, err := s.store.CreateProject(r.Context(), ident.UserID, store.ProjectCreate{
		ContextID:   strings.TrimSpace(req.ContextID),
		Name:        name,
		Description: req.Description,
		Status:      req.Status,
	})
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	w.Header().Set("Location", "/v1/projects/"+created.ID)
	s.writeJSON(w, r, http.StatusCreated, created)
}

func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	var req projectUpdateRequest
	if err := s.decodeBody(w, r, &req); err != nil {
		return
	}

	v := newValidationError()
	req.Name = checkRequiredStringPatch(v, "name", req.Name)
	req.ContextID = checkRequiredStringPatch(v, "context_id", req.ContextID)
	checkEnumPatch(v, "status", req.Status, model.ProjectStatuses)

	if v.any() {
		s.writeStoreError(w, r, v)

		return
	}

	updated, err := s.store.UpdateProject(r.Context(), ident.UserID, r.PathValue("id"), store.ProjectUpdate{
		ContextID:   req.ContextID,
		Name:        req.Name,
		Description: req.Description,
		Status:      req.Status,
	})
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	s.writeJSON(w, r, http.StatusOK, updated)
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	if err := s.store.DeleteProject(r.Context(), ident.UserID, r.PathValue("id")); err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}
