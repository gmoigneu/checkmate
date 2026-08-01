package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	reportservice "github.com/nls/checkmate/server/internal/report"
	"github.com/nls/checkmate/server/internal/store"
)

type reportRequestBody struct {
	StartOn      string   `json:"start_on"`
	EndOn        string   `json:"end_on"`
	ContextIDs   []string `json:"context_ids"`
	IncludeInbox bool     `json:"include_inbox"`
	Focus        string   `json:"focus"`
}

func (b reportRequestBody) storeRequest() store.ReportRequest {
	ids := make([]string, 0, len(b.ContextIDs))
	for _, id := range b.ContextIDs {
		id = strings.TrimSpace(id)
		if id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return store.ReportRequest{
		StartOn: strings.TrimSpace(b.StartOn), EndOn: strings.TrimSpace(b.EndOn),
		ContextIDs: ids, IncludeInbox: b.IncludeInbox, Focus: strings.TrimSpace(b.Focus),
	}
}

func (s *Server) handleReportConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.caller(w, r); !ok {
		return
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"configured": s.reports.Configured(), "model": reportservice.Model,
	})
}

func (s *Server) handlePreviewReport(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}
	var body reportRequestBody
	if err := s.decodeBody(w, r, &body); err != nil {
		return
	}
	_, preview, err := s.reports.Preview(r.Context(), ident.UserID, body.storeRequest())
	if err != nil {
		s.writeReportError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, preview)
}

func (s *Server) handleGenerateReport(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}
	var body reportRequestBody
	if err := s.decodeBody(w, r, &body); err != nil {
		return
	}
	report, err := s.reports.Generate(r.Context(), ident.UserID, body.storeRequest())
	if err != nil {
		s.writeReportError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusCreated, report)
}

func (s *Server) handleListReports(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}
	reports, err := s.store.ListReports(r.Context(), ident.UserID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	writeList(s, w, r, reports, "")
}

func (s *Server) handleGetReport(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}
	report, err := s.store.GetReport(r.Context(), ident.UserID, r.PathValue("id"))
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, report)
}

type reportUpdateBody struct {
	Title           *string `json:"title"`
	ContentMarkdown *string `json:"content_markdown"`
	VersionNumber   *int64  `json:"version_number"`
}

func (s *Server) handleUpdateReport(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}
	var body reportUpdateBody
	if err := s.decodeBody(w, r, &body); err != nil {
		return
	}
	if body.Title == nil && body.ContentMarkdown == nil {
		s.writeJSON(w, r, http.StatusUnprocessableEntity, errorBody{
			Error: "validation failed", Fields: map[string]string{"body": "provide title or content_markdown"},
		})
		return
	}
	report, err := s.store.UpdateReportDraft(
		r.Context(), ident.UserID, r.PathValue("id"), body.Title, body.ContentMarkdown,
		body.VersionNumber)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, report)
}

func (s *Server) handleDeleteReport(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteReport(r.Context(), ident.UserID, r.PathValue("id")); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusNoContent, nil)
}

func (s *Server) handleRegenerateReport(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}
	report, err := s.reports.Regenerate(r.Context(), ident.UserID, r.PathValue("id"))
	if err != nil {
		s.writeReportError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusCreated, report)
}

func (s *Server) writeReportError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, reportservice.ErrNotConfigured):
		s.writeError(w, r, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, reportservice.ErrInFlight):
		s.writeError(w, r, http.StatusConflict, err.Error())
	case errors.Is(err, reportservice.ErrNoData):
		s.writeJSON(w, r, http.StatusUnprocessableEntity, errorBody{
			Error: "validation failed", Fields: map[string]string{"date_range": err.Error()},
		})
	case errors.Is(err, context.DeadlineExceeded):
		s.writeError(w, r, http.StatusGatewayTimeout, "report generation timed out")
	case errors.Is(err, reportservice.ErrInvalidOutput):
		s.writeError(w, r, http.StatusBadGateway, "the report provider returned an invalid response")
	default:
		var conflict *store.ConflictError
		var invalidRef *store.InvalidRefError
		if errors.As(err, &conflict) || errors.As(err, &invalidRef) ||
			errors.Is(err, store.ErrNotFound) {
			s.writeStoreError(w, r, err)
			return
		}
		s.log.Error("report generation failed", slog.Any("error", err))
		s.writeError(w, r, http.StatusBadGateway, "report generation failed")
	}
}
