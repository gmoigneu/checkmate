package httpapi

import (
	"net/http"
	"strings"

	"github.com/nls/checkmate/server/internal/model"
	"github.com/nls/checkmate/server/internal/patch"
	"github.com/nls/checkmate/server/internal/store"
)

type taskCreateRequest struct {
	ContextID       *string `json:"context_id"`
	ProjectID       *string `json:"project_id"`
	ParentID        *string `json:"parent_id"`
	Source          *string `json:"source"`
	CaptureMethod   string  `json:"capture_method"`
	Title           string  `json:"title"`
	Details         *string `json:"details"`
	Status          string  `json:"status"`
	DueOn           *string `json:"due_on"`
	PlannedOn       *string `json:"planned_on"`
	EstimateMinutes *int64  `json:"estimate_minutes"`
	DelegatedToID   *string `json:"delegated_to_id"`

	// DelegatedTo names a person instead of referencing one, so a capture client
	// can delegate in a single call. Resolved to an existing person by name, or
	// created on the spot.
	DelegatedTo *string `json:"delegated_to"`

	BlockedByID    *string `json:"blocked_by_id"`
	ReferenceURL   *string `json:"reference_url"`
	ReferenceLabel *string `json:"reference_label"`
}

type taskUpdateRequest struct {
	ContextID       patch.Field[string] `json:"context_id"`
	ProjectID       patch.Field[string] `json:"project_id"`
	ParentID        patch.Field[string] `json:"parent_id"`
	Source          patch.Field[string] `json:"source"`
	Title           patch.Field[string] `json:"title"`
	Details         patch.Field[string] `json:"details"`
	Status          patch.Field[string] `json:"status"`
	DueOn           patch.Field[string] `json:"due_on"`
	PlannedOn       patch.Field[string] `json:"planned_on"`
	EstimateMinutes patch.Field[int64]  `json:"estimate_minutes"`
	DelegatedToID   patch.Field[string] `json:"delegated_to_id"`
	BlockedByID     patch.Field[string] `json:"blocked_by_id"`
	ReferenceURL    patch.Field[string] `json:"reference_url"`
	ReferenceLabel  patch.Field[string] `json:"reference_label"`
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	p := newParams(r)

	f := store.TaskFilter{
		Status:        p.csv("status", model.TaskStatuses),
		Kind:          p.csv("kind", model.TaskKinds),
		DelegatedToID: p.str("delegated_to_id"),
		RecurrenceID:  p.str("recurrence_id"),
		PlannedOn:     p.date("planned_on"),
		PlannedBefore: p.date("planned_before"),
		PlannedAfter:  p.date("planned_after"),
		DueOn:         p.date("due_on"),
		DueBefore:     p.date("due_before"),
		DueAfter:      p.date("due_after"),
		Search:        p.str("q"),
		TopLevelOnly:  p.boolean("top_level"),
		Sort:          p.enum("sort", store.SortFields),
		Order:         p.enum("order", store.SortOrders),
	}

	f.BlockedByID, f.BlockedByIsNull = p.nullableID("blocked_by_id")
	f.ContextID, f.ContextIsNull = p.nullableID("context_id")
	f.ProjectID, f.ProjectIsNull = p.nullableID("project_id")
	f.ParentID, _ = p.nullableID("parent_id")

	// ?parent_id=null and ?top_level=true ask the same question.
	if _, isNull := p.nullableID("parent_id"); isNull {
		f.TopLevelOnly = true
	}

	f.IncludeDeleted, f.Limit, f.Cursor = p.listOptions()

	if err := p.done(); err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	items, cursor, err := s.store.ListTasks(r.Context(), ident.UserID, f)
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	writeList(s, w, r, items, cursor)
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	v, err := s.store.GetTask(r.Context(), ident.UserID, r.PathValue("id"))
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	s.writeJSON(w, r, http.StatusOK, v)
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	var req taskCreateRequest
	if err := s.decodeBody(w, r, &req); err != nil {
		return
	}

	v := newValidationError()

	title := requireTitle(v, req.Title)

	checkEnum(v, "status", req.Status, model.TaskStatuses)
	checkEnum(v, "capture_method", req.CaptureMethod, model.CaptureMethods)
	checkDate(v, "due_on", req.DueOn)
	checkDate(v, "planned_on", req.PlannedOn)
	checkPositive(v, "estimate_minutes", req.EstimateMinutes)
	checkURL(v, "reference_url", req.ReferenceURL)

	if req.DelegatedTo != nil && req.DelegatedToID != nil {
		v.add("delegated_to", "cannot be combined with delegated_to_id")
	}

	if v.any() {
		s.writeStoreError(w, r, v)

		return
	}

	delegatedToID := req.DelegatedToID

	// Resolving the name before the insert keeps "delegate to Marc" a single
	// request even the first time Marc is mentioned.
	if req.DelegatedTo != nil && strings.TrimSpace(*req.DelegatedTo) != "" {
		person, err := s.store.FindOrCreatePerson(r.Context(), ident.UserID, *req.DelegatedTo)
		if err != nil {
			s.writeStoreError(w, r, err)

			return
		}

		delegatedToID = &person.ID
	}

	created, err := s.store.CreateTask(r.Context(), ident.UserID, store.TaskCreate{
		ContextID:       req.ContextID,
		ProjectID:       req.ProjectID,
		ParentID:        req.ParentID,
		Source:          req.Source,
		CaptureMethod:   req.CaptureMethod,
		Title:           title,
		Details:         req.Details,
		Status:          req.Status,
		DueOn:           req.DueOn,
		PlannedOn:       req.PlannedOn,
		EstimateMinutes: req.EstimateMinutes,
		DelegatedToID:   delegatedToID,
		BlockedByID:     req.BlockedByID,
		ReferenceURL:    req.ReferenceURL,
		ReferenceLabel:  req.ReferenceLabel,
	})
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	w.Header().Set("Location", "/v1/tasks/"+created.ID)
	s.writeJSON(w, r, http.StatusCreated, created)
}

func (s *Server) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	var req taskUpdateRequest
	if err := s.decodeBody(w, r, &req); err != nil {
		return
	}

	v := newValidationError()

	req.Title = checkTitlePatch(v, req.Title)
	checkEnumPatch(v, "status", req.Status, model.TaskStatuses)
	checkDatePatch(v, "due_on", req.DueOn)
	checkDatePatch(v, "planned_on", req.PlannedOn)
	checkPositivePatch(v, "estimate_minutes", req.EstimateMinutes)
	checkURLPatch(v, "reference_url", req.ReferenceURL)

	if v.any() {
		s.writeStoreError(w, r, v)

		return
	}

	updated, err := s.store.UpdateTask(r.Context(), ident.UserID, r.PathValue("id"), store.TaskUpdate{
		ContextID:       req.ContextID,
		ProjectID:       req.ProjectID,
		ParentID:        req.ParentID,
		Source:          req.Source,
		Title:           req.Title,
		Details:         req.Details,
		Status:          req.Status,
		DueOn:           req.DueOn,
		PlannedOn:       req.PlannedOn,
		EstimateMinutes: req.EstimateMinutes,
		DelegatedToID:   req.DelegatedToID,
		BlockedByID:     req.BlockedByID,
		ReferenceURL:    req.ReferenceURL,
		ReferenceLabel:  req.ReferenceLabel,
	})
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	s.writeJSON(w, r, http.StatusOK, updated)
}

func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	if err := s.store.DeleteTask(r.Context(), ident.UserID, r.PathValue("id")); err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}
