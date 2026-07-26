package httpapi

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/nls/checkmate/server/internal/model"
	"github.com/nls/checkmate/server/internal/patch"
	"github.com/nls/checkmate/server/internal/store"
)

type recurrenceCreateRequest struct {
	Kind            string  `json:"kind"`
	ContextID       string  `json:"context_id"`
	ProjectID       *string `json:"project_id"`
	Source          *string `json:"source"`
	Title           string  `json:"title"`
	Details         *string `json:"details"`
	DaySlot         *string `json:"day_slot"`
	SlotOrder       *int64  `json:"slot_order"`
	RRule           string  `json:"rrule"`
	Timezone        string  `json:"timezone"`
	EstimateMinutes *int64  `json:"estimate_minutes"`
	DelegatedToID   *string `json:"delegated_to_id"`
	LeadDays        *int64  `json:"lead_days"`
	StartsOn        string  `json:"starts_on"`
	EndsOn          *string `json:"ends_on"`
	Active          *bool   `json:"active"`
}

type recurrenceUpdateRequest struct {
	ContextID       patch.Field[string] `json:"context_id"`
	ProjectID       patch.Field[string] `json:"project_id"`
	Source          patch.Field[string] `json:"source"`
	Title           patch.Field[string] `json:"title"`
	Details         patch.Field[string] `json:"details"`
	DaySlot         patch.Field[string] `json:"day_slot"`
	SlotOrder       patch.Field[int64]  `json:"slot_order"`
	RRule           patch.Field[string] `json:"rrule"`
	Timezone        patch.Field[string] `json:"timezone"`
	EstimateMinutes patch.Field[int64]  `json:"estimate_minutes"`
	DelegatedToID   patch.Field[string] `json:"delegated_to_id"`
	LeadDays        patch.Field[int64]  `json:"lead_days"`
	StartsOn        patch.Field[string] `json:"starts_on"`
	EndsOn          patch.Field[string] `json:"ends_on"`
	Active          patch.Field[bool]   `json:"active"`
}

func (s *Server) handleListRecurrences(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	p := newParams(r)

	f := store.RecurrenceFilter{
		ContextID: p.str("context_id"),
		Active:    p.booleanPtr("active"),
		State:     p.enum("state", model.RecurrenceStates),
		Kind:      p.enum("kind", model.RecurrenceKinds),
	}
	if f.Kind == "" {
		f.Kind = model.RecurrenceClassic
	}
	f.IncludeDeleted, f.Limit, f.Cursor = p.listOptions()

	// active and state describe the same thing at different resolutions, and the
	// store applies both, so together they can contradict: ?active=true&state=finished
	// asks for a series that is running and over at once and would answer with an
	// empty 200. That leaves the caller debugging their own query, so say what is
	// wrong instead. Redundant-but-consistent pairs (active=false&state=paused) are
	// fine, because a UI may well set one from a toggle and the other from a filter.
	if f.Active != nil && f.State != "" && *f.Active != (f.State == model.RecurrenceActive) {
		p.errors.add("state", fmt.Sprintf(
			"contradicts active=%t, because state=%s means active=%t",
			*f.Active, f.State, f.State == model.RecurrenceActive))
	}

	if err := p.done(); err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	items, cursor, err := s.store.ListRecurrences(r.Context(), ident.UserID, f)
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	writeList(s, w, r, items, cursor)
}

func (s *Server) handleGetRecurrence(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	v, err := s.store.GetRecurrence(r.Context(), ident.UserID, r.PathValue("id"))
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	s.writeJSON(w, r, http.StatusOK, v)
}

func (s *Server) handleCreateRecurrence(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	var req recurrenceCreateRequest
	if err := s.decodeBody(w, r, &req); err != nil {
		return
	}

	v := newValidationError()

	title := requireTitle(v, req.Title)
	kind := req.Kind
	if kind == "" {
		kind = model.RecurrenceClassic
	}
	checkEnum(v, "kind", kind, model.RecurrenceKinds)

	if strings.TrimSpace(req.ContextID) == "" {
		v.add("context_id", "is required")
	}

	if req.DaySlot != nil {
		if *req.DaySlot == "" {
			v.add("day_slot", "cannot be empty")
		} else {
			checkEnum(v, "day_slot", *req.DaySlot, model.DaySlots)
		}
	}
	if kind == model.RecurrenceRoutine && req.DaySlot == nil {
		v.add("day_slot", "is required for a routine")
	}
	if req.SlotOrder != nil && *req.SlotOrder < 0 {
		v.add("slot_order", "cannot be negative")
	}
	checkRRule(v, "rrule", req.RRule)
	requireDate(v, "starts_on", req.StartsOn)
	checkDate(v, "ends_on", req.EndsOn)
	checkDateOrder(v, "starts_on", req.StartsOn, "ends_on", req.EndsOn)
	checkTimezone(v, "timezone", req.Timezone)
	checkPositive(v, "estimate_minutes", req.EstimateMinutes)

	if req.LeadDays != nil && *req.LeadDays < 0 {
		v.add("lead_days", "cannot be negative")
	}

	if v.any() {
		s.writeStoreError(w, r, v)

		return
	}

	timezone := req.Timezone
	if kind == model.RecurrenceRoutine {
		timezone = ident.Timezone
	}

	created, err := s.store.CreateRecurrence(r.Context(), ident.UserID, store.RecurrenceCreate{
		Kind:            kind,
		ContextID:       strings.TrimSpace(req.ContextID),
		ProjectID:       req.ProjectID,
		Source:          req.Source,
		Title:           title,
		Details:         req.Details,
		DaySlot:         req.DaySlot,
		SlotOrder:       req.SlotOrder,
		RRule:           strings.TrimSpace(req.RRule),
		Timezone:        timezone,
		EstimateMinutes: req.EstimateMinutes,
		DelegatedToID:   req.DelegatedToID,
		LeadDays:        req.LeadDays,
		StartsOn:        req.StartsOn,
		EndsOn:          req.EndsOn,
		Active:          req.Active,
	})
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	s.spawnNow(r, ident.UserID, created.ID)

	w.Header().Set("Location", "/v1/recurrences/"+created.ID)
	s.writeJSON(w, r, http.StatusCreated, s.afterSpawn(r, ident.UserID, created))
}

// spawnNow materializes a template's due occurrences immediately.
//
// Synchronous, and deliberately best-effort: the scheduler will pick the template
// up within the tick regardless, so a failure here is a delay rather than lost
// work and must not fail the write the caller actually asked for. Doing it inline
// means creating a daily recurrence and listing tasks in the next breath shows
// today's occurrence, instead of nothing for fifteen minutes.
func (s *Server) spawnNow(r *http.Request, userID, recurrenceID string) {
	if s.spawner == nil {
		return
	}

	result, err := s.spawner.RunTemplate(r.Context(), userID, recurrenceID)
	if err != nil {
		s.log.Warn("could not spawn occurrences for a new recurrence",
			slog.String("recurrence_id", recurrenceID),
			slog.Any("error", err))

		return
	}

	if result.Created > 0 {
		s.log.Info("spawned occurrences",
			slog.String("recurrence_id", recurrenceID),
			slog.Int("created", result.Created))
	}
}

// afterSpawn re-reads a template once the spawner has run on it.
//
// The spawner writes to the same row it was handed -- advancing
// next_occurrence_on, stamping last_spawned_on, and retiring the series if the rule
// is spent -- so the copy read before it ran is already stale by the time the
// response is written. Returning that copy would tell a client a series is active
// when the server has just finished it.
//
// Best effort: if the re-read fails the pre-spawn copy is still a truthful
// representation of the write the caller asked for, just missing what happened next.
func (s *Server) afterSpawn(r *http.Request, userID string, fallback model.Recurrence) model.Recurrence {
	fresh, err := s.store.GetRecurrence(r.Context(), userID, fallback.ID)
	if err != nil {
		s.log.Warn("could not re-read a recurrence after spawning",
			slog.String("recurrence_id", fallback.ID), slog.Any("error", err))

		return fallback
	}

	return fresh
}

func (s *Server) handleUpdateRecurrence(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	var req recurrenceUpdateRequest
	if err := s.decodeBody(w, r, &req); err != nil {
		return
	}

	v := newValidationError()

	req.Title = checkTitlePatch(v, req.Title)
	req.ContextID = checkRequiredStringPatch(v, "context_id", req.ContextID)
	checkEnumPatch(v, "day_slot", req.DaySlot, model.DaySlots)
	checkNonNegativePatch(v, "slot_order", req.SlotOrder)
	if req.SlotOrder.Set && req.SlotOrder.Null {
		v.add("slot_order", "cannot be null")
	}
	checkRRulePatch(v, "rrule", req.RRule)
	checkTimezonePatch(v, "timezone", req.Timezone)
	checkPositivePatch(v, "estimate_minutes", req.EstimateMinutes)
	checkNonNegativePatch(v, "lead_days", req.LeadDays)
	checkDatePatch(v, "starts_on", req.StartsOn)
	checkDatePatch(v, "ends_on", req.EndsOn)

	if req.StartsOn.Set && req.StartsOn.Null {
		v.add("starts_on", "cannot be null")
	}

	if req.Active.Set && req.Active.Null {
		v.add("active", "cannot be null")
	}

	// NOT NULL in the schema; see the note on sort_order in contexts.go.
	if req.LeadDays.Set && req.LeadDays.Null {
		v.add("lead_days", "cannot be null")
	}

	if req.StartsOn.Present() && req.EndsOn.Present() {
		checkDateOrder(v, "starts_on", req.StartsOn.Value, "ends_on", &req.EndsOn.Value)
	}

	if v.any() {
		s.writeStoreError(w, r, v)

		return
	}

	updated, err := s.store.UpdateRecurrence(r.Context(), ident.UserID, r.PathValue("id"), store.RecurrenceUpdate{
		ContextID:       req.ContextID,
		ProjectID:       req.ProjectID,
		Source:          req.Source,
		Title:           req.Title,
		Details:         req.Details,
		DaySlot:         req.DaySlot,
		SlotOrder:       req.SlotOrder,
		RRule:           req.RRule,
		Timezone:        req.Timezone,
		EstimateMinutes: req.EstimateMinutes,
		DelegatedToID:   req.DelegatedToID,
		LeadDays:        req.LeadDays,
		StartsOn:        req.StartsOn,
		EndsOn:          req.EndsOn,
		Active:          req.Active,
	})
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	if updated.Kind == model.RecurrenceRoutine && updated.Active {
		if err := s.store.PrepareRoutineRespawn(
			r.Context(), ident.UserID, updated.ID, time.Now().UTC(),
		); err != nil {
			s.log.Warn("could not rewind a routine after editing",
				slog.String("recurrence_id", updated.ID), slog.Any("error", err))
		}
	}

	s.spawnNow(r, ident.UserID, updated.ID)

	s.writeJSON(w, r, http.StatusOK, s.afterSpawn(r, ident.UserID, updated))
}

func (s *Server) handleDeleteRecurrence(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	if err := s.store.DeleteRecurrence(r.Context(), ident.UserID, r.PathValue("id")); err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}
