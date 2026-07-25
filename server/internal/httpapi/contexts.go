package httpapi

import (
	"net/http"
	"strings"

	"github.com/nls/checkmate/server/internal/patch"
	"github.com/nls/checkmate/server/internal/store"
)

type contextCreateRequest struct {
	Name      string  `json:"name"`
	Slug      string  `json:"slug"`
	Color     *string `json:"color"`
	SortOrder *int64  `json:"sort_order"`
}

type contextUpdateRequest struct {
	Name      patch.Field[string] `json:"name"`
	Slug      patch.Field[string] `json:"slug"`
	Color     patch.Field[string] `json:"color"`
	SortOrder patch.Field[int64]  `json:"sort_order"`
	Archived  patch.Field[bool]   `json:"archived"`
}

func (s *Server) handleListContexts(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	p := newParams(r)

	f := store.ContextFilter{IncludeArchived: p.boolean("include_archived")}
	f.IncludeDeleted, f.Limit, f.Cursor = p.listOptions()

	if err := p.done(); err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	items, cursor, err := s.store.ListContexts(r.Context(), ident.UserID, f)
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	writeList(s, w, r, items, cursor)
}

func (s *Server) handleGetContext(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	v, err := s.store.GetContext(r.Context(), ident.UserID, r.PathValue("id"))
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	s.writeJSON(w, r, http.StatusOK, v)
}

func (s *Server) handleCreateContext(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	var req contextCreateRequest
	if err := s.decodeBody(w, r, &req); err != nil {
		return
	}

	v := newValidationError()

	name := strings.TrimSpace(req.Name)
	if name == "" {
		v.add("name", "is required")
	}

	if req.Slug != "" && store.Slugify(req.Slug) == "" {
		v.add("slug", "must contain at least one letter or digit")
	}

	checkColor(v, "color", req.Color)

	if v.any() {
		s.writeStoreError(w, r, v)

		return
	}

	created, err := s.store.CreateContext(r.Context(), ident.UserID, store.ContextCreate{
		Name:      name,
		Slug:      req.Slug,
		Color:     req.Color,
		SortOrder: req.SortOrder,
	})
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	w.Header().Set("Location", "/v1/contexts/"+created.ID)
	s.writeJSON(w, r, http.StatusCreated, created)
}

func (s *Server) handleUpdateContext(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	var req contextUpdateRequest
	if err := s.decodeBody(w, r, &req); err != nil {
		return
	}

	v := newValidationError()
	req.Name = checkRequiredStringPatch(v, "name", req.Name)
	req.Slug = checkRequiredStringPatch(v, "slug", req.Slug)

	if req.Archived.Set && req.Archived.Null {
		v.add("archived", "cannot be null")
	}

	checkColorPatch(v, "color", req.Color)

	// sort_order is NOT NULL in the schema, so a null here would reach sqlite as
	// a constraint violation and surface as a 500. A client mistake has to be
	// reported as a client mistake.
	if req.SortOrder.Set && req.SortOrder.Null {
		v.add("sort_order", "cannot be null")
	}

	if v.any() {
		s.writeStoreError(w, r, v)

		return
	}

	updated, err := s.store.UpdateContext(r.Context(), ident.UserID, r.PathValue("id"), store.ContextUpdate{
		Name:      req.Name,
		Slug:      req.Slug,
		Color:     req.Color,
		SortOrder: req.SortOrder,
		Archived:  req.Archived,
	})
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	s.writeJSON(w, r, http.StatusOK, updated)
}

func (s *Server) handleDeleteContext(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	if err := s.store.DeleteContext(r.Context(), ident.UserID, r.PathValue("id")); err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListSources(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListSources(r.Context())
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	writeList(s, w, r, items, "")
}
