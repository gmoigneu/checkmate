package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/nls/checkmate/server/internal/id"
	"github.com/nls/checkmate/server/internal/model"
	"github.com/nls/checkmate/server/internal/patch"
)

const recurrenceColumns = `id, kind, context_id, project_id, source_key, title, details,
	day_slot, slot_order, rrule, timezone, estimate_minutes, delegated_to_id, lead_days, starts_on,
	ends_on, next_occurrence_on, last_spawned_on, active, completed_at,
	created_at, updated_at, deleted_at, rev`

// RecurrenceCreate is the input for creating a recurrence template.
type RecurrenceCreate struct {
	Kind            string
	ContextID       string
	ProjectID       *string
	Source          *string
	Title           string
	Details         *string
	DaySlot         *string
	SlotOrder       *int64
	RRule           string
	Timezone        string
	EstimateMinutes *int64
	DelegatedToID   *string
	LeadDays        *int64
	StartsOn        string
	EndsOn          *string
	Active          *bool
}

// RecurrenceUpdate is the set of recurrence fields a PATCH may change.
type RecurrenceUpdate struct {
	ContextID       patch.Field[string]
	ProjectID       patch.Field[string]
	Source          patch.Field[string]
	Title           patch.Field[string]
	Details         patch.Field[string]
	DaySlot         patch.Field[string]
	SlotOrder       patch.Field[int64]
	RRule           patch.Field[string]
	Timezone        patch.Field[string]
	EstimateMinutes patch.Field[int64]
	DelegatedToID   patch.Field[string]
	LeadDays        patch.Field[int64]
	StartsOn        patch.Field[string]
	EndsOn          patch.Field[string]
	Active          patch.Field[bool]
}

// RecurrenceFilter narrows a recurrence listing.
type RecurrenceFilter struct {
	listOptions

	ContextID string
	Active    *bool
	Kind      string

	// State filters on the derived three-way state. Validate against
	// model.RecurrenceStates before calling.
	State string
}

// ListRecurrences returns the caller's recurrence templates, newest first.
func (s *Store) ListRecurrences(ctx context.Context, userID string, f RecurrenceFilter) ([]model.Recurrence, string, error) {
	c := &conditions{}
	c.add("user_id = ?", userID)

	if !f.IncludeDeleted {
		c.add("deleted_at IS NULL")
	}

	if f.ContextID != "" {
		c.add("context_id = ?", f.ContextID)
	}

	if f.Kind != "" {
		c.add("kind = ?", f.Kind)
	}

	if f.Active != nil {
		c.add("active = ?", boolToInt(*f.Active))
	}

	// The derived state, expressed as the stored pair it comes from.
	switch f.State {
	case model.RecurrenceActive:
		c.add("active = 1")
	case model.RecurrencePaused:
		c.add("active = 0 AND completed_at IS NULL")
	case model.RecurrenceFinished:
		c.add("active = 0 AND completed_at IS NOT NULL")
	}

	if f.Cursor != "" {
		c.add("id < ?", f.Cursor)
	}

	limit := f.normalize()

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+recurrenceColumns+` FROM recurrences`+c.clause()+` ORDER BY id DESC LIMIT ?`,
		append(c.args, limit+1)...,
	)
	if err != nil {
		return nil, "", fmt.Errorf("store: list recurrences: %w", err)
	}
	defer rows.Close()

	var out []model.Recurrence

	for rows.Next() {
		v, err := scanRecurrence(rows)
		if err != nil {
			return nil, "", err
		}

		out = append(out, v)
	}

	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("store: list recurrences: %w", err)
	}

	return page(out, limit, func(v model.Recurrence) string { return v.ID })
}

// GetRecurrence returns one recurrence owned by userID.
func (s *Store) GetRecurrence(ctx context.Context, userID, recurrenceID string) (model.Recurrence, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+recurrenceColumns+` FROM recurrences WHERE id = ? AND user_id = ? AND deleted_at IS NULL`,
		recurrenceID, userID,
	)

	return scanRecurrence(row)
}

// CreateRecurrence inserts a recurrence template.
func (s *Store) CreateRecurrence(ctx context.Context, userID string, in RecurrenceCreate) (model.Recurrence, error) {
	newID := id.New()

	kind := in.Kind
	if kind == "" {
		kind = model.RecurrenceClassic
	}

	timezone := in.Timezone
	if timezone == "" {
		timezone = "UTC"
	}

	var slotOrder int64
	if in.SlotOrder != nil {
		slotOrder = *in.SlotOrder
	}

	var leadDays int64
	if in.LeadDays != nil {
		leadDays = *in.LeadDays
	}

	active := int64(1)
	if in.Active != nil {
		active = boolToInt(*in.Active)
	}

	if kind == model.RecurrenceRoutine && in.DaySlot == nil {
		return model.Recurrence{}, &ConflictError{Field: "day_slot", Detail: "is required for a routine"}
	}
	if kind == model.RecurrenceRoutine && !validRoutineRRule(in.RRule) {
		return model.Recurrence{}, &ConflictError{
			Field:  "rrule",
			Detail: "a routine must select weekdays with FREQ=DAILY or FREQ=WEEKLY;BYDAY=…",
		}
	}

	err := s.tx(ctx, func(tx *sql.Tx) error {
		if err := assertOwned(ctx, tx, "context_id", in.ContextID, userID); err != nil {
			return err
		}

		if err := assertRefsOwned(ctx, tx, userID, map[string]*string{
			"project_id":      in.ProjectID,
			"delegated_to_id": in.DelegatedToID,
		}); err != nil {
			return err
		}

		if in.ProjectID != nil {
			if err := assertProjectInContext(ctx, tx, userID, *in.ProjectID, in.ContextID); err != nil {
				return err
			}
		}

		_, err := tx.ExecContext(ctx,
			`INSERT INTO recurrences (id, user_id, kind, context_id, project_id, source_key, title,
				details, day_slot, slot_order, rrule, timezone, estimate_minutes, delegated_to_id,
				lead_days, starts_on, ends_on, next_occurrence_on, active)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			newID, userID, kind, in.ContextID, in.ProjectID, in.Source, in.Title,
			in.Details, in.DaySlot, slotOrder, in.RRule, timezone, in.EstimateMinutes,
			in.DelegatedToID, leadDays, in.StartsOn, in.EndsOn, in.StartsOn, active,
		)
		if err != nil {
			return fmt.Errorf("store: insert recurrence: %w", err)
		}

		return nil
	})
	if err != nil {
		return model.Recurrence{}, err
	}

	return s.GetRecurrence(ctx, userID, newID)
}

// UpdateRecurrence applies a partial update.
func (s *Store) UpdateRecurrence(ctx context.Context, userID, recurrenceID string, in RecurrenceUpdate) (model.Recurrence, error) {
	err := s.tx(ctx, func(tx *sql.Tx) error {
		current, err := getRecurrenceTx(ctx, tx, userID, recurrenceID)
		if err != nil {
			return err
		}

		nextSlot := current.DaySlot
		if in.DaySlot.Set {
			if in.DaySlot.Null {
				nextSlot = nil
			} else {
				nextSlot = &in.DaySlot.Value
			}
		}

		if current.Kind == model.RecurrenceRoutine && nextSlot == nil {
			return &ConflictError{Field: "day_slot", Detail: "is required for a routine"}
		}
		if current.Kind == model.RecurrenceRoutine && in.RRule.Present() &&
			!validRoutineRRule(in.RRule.Value) {
			return &ConflictError{
				Field:  "rrule",
				Detail: "a routine must select weekdays with FREQ=DAILY or FREQ=WEEKLY;BYDAY=…",
			}
		}

		if current.Kind == model.RecurrenceRoutine && in.Active.Present() && !in.Active.Value {
			if err := expireOpenRoutineOccurrencesTx(ctx, tx, userID, recurrenceID); err != nil {
				return err
			}
		}

		b := &updateBuilder{}

		applyField(b, "title", in.Title)
		applyField(b, "details", in.Details)
		applyField(b, "day_slot", in.DaySlot)
		applyField(b, "slot_order", in.SlotOrder)
		applyField(b, "rrule", in.RRule)
		applyField(b, "timezone", in.Timezone)
		applyField(b, "estimate_minutes", in.EstimateMinutes)
		applyField(b, "lead_days", in.LeadDays)
		applyField(b, "starts_on", in.StartsOn)
		applyField(b, "ends_on", in.EndsOn)
		applyField(b, "source_key", in.Source)
		applyBoolField(b, "active", in.Active)

		// Resuming clears the retirement marker, so the spawner re-decides. If the
		// rule really is spent it will retire the series again on the next pass --
		// which is the honest outcome, and immediate, because updating a template
		// runs the spawner inline.
		if in.Active.Present() && in.Active.Value {
			b.set("completed_at", nil)
		}

		contextID := current.ContextID
		if in.ContextID.Present() {
			if err := assertOwned(ctx, tx, "context_id", in.ContextID.Value, userID); err != nil {
				return err
			}

			contextID = in.ContextID.Value
			b.set("context_id", contextID)
		}

		projectID := current.ProjectID

		if in.ProjectID.Set {
			if in.ProjectID.Null {
				projectID = nil
				b.set("project_id", nil)
			} else {
				if err := assertOwned(ctx, tx, "project_id", in.ProjectID.Value, userID); err != nil {
					return err
				}

				projectID = &in.ProjectID.Value
				b.set("project_id", in.ProjectID.Value)
			}
		}

		if projectID != nil {
			if err := assertProjectInContext(ctx, tx, userID, *projectID, contextID); err != nil {
				return err
			}
		}

		if in.DelegatedToID.Set {
			if in.DelegatedToID.Null {
				b.set("delegated_to_id", nil)
			} else {
				if err := assertOwned(ctx, tx, "delegated_to_id", in.DelegatedToID.Value, userID); err != nil {
					return err
				}

				b.set("delegated_to_id", in.DelegatedToID.Value)
			}
		}

		if b.empty() {
			return nil
		}

		_, err = tx.ExecContext(ctx,
			`UPDATE recurrences SET `+b.clause()+` WHERE id = ? AND user_id = ?`,
			append(b.args, recurrenceID, userID)...,
		)
		if err != nil {
			return fmt.Errorf("store: update recurrence: %w", err)
		}

		return nil
	})
	if err != nil {
		return model.Recurrence{}, err
	}

	return s.GetRecurrence(ctx, userID, recurrenceID)
}

// validRoutineRRule keeps the Routine editor's promise: each item selects the
// weekdays it applies to, rather than exposing the full classic RRULE language.
func validRoutineRRule(value string) bool {
	rule := strings.ToUpper(strings.TrimSpace(value))
	if rule == "FREQ=DAILY" {
		return true
	}

	const prefix = "FREQ=WEEKLY;BYDAY="
	if !strings.HasPrefix(rule, prefix) {
		return false
	}

	days := strings.Split(strings.TrimPrefix(rule, prefix), ",")
	if len(days) == 0 || len(days) > 7 {
		return false
	}

	seen := map[string]bool{}
	for _, day := range days {
		switch day {
		case "MO", "TU", "WE", "TH", "FR", "SA", "SU":
			if seen[day] {
				return false
			}
			seen[day] = true
		default:
			return false
		}
	}

	return true
}

// DeleteRecurrence tombstones a template.
//
// Occurrences already spawned from it are left alone: they are real tasks with
// real history, and deleting a series should not rewrite the past. Their derived
// kind remains available through the tombstoned template row.
func (s *Store) DeleteRecurrence(ctx context.Context, userID, recurrenceID string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		current, err := getRecurrenceTx(ctx, tx, userID, recurrenceID)
		if err != nil {
			return err
		}

		if current.Kind == model.RecurrenceRoutine {
			if err := expireOpenRoutineOccurrencesTx(ctx, tx, userID, recurrenceID); err != nil {
				return err
			}
		}

		_, err = tx.ExecContext(ctx,
			`UPDATE recurrences SET deleted_at = `+nowExpr+`, active = 0, updated_at = `+nowExpr+`
			 WHERE user_id = ? AND id = ? AND deleted_at IS NULL`,
			userID, recurrenceID,
		)
		if err != nil {
			return fmt.Errorf("store: delete recurrence: %w", err)
		}

		return nil
	})
}

func getRecurrenceTx(ctx context.Context, tx *sql.Tx, userID, recurrenceID string) (model.Recurrence, error) {
	row := tx.QueryRowContext(ctx,
		`SELECT `+recurrenceColumns+` FROM recurrences WHERE id = ? AND user_id = ? AND deleted_at IS NULL`,
		recurrenceID, userID,
	)

	return scanRecurrence(row)
}

// assertRefsOwned checks a batch of optional references in one place.
func assertRefsOwned(ctx context.Context, q querier, userID string, refs map[string]*string) error {
	for field, value := range refs {
		if value == nil {
			continue
		}

		if err := assertOwned(ctx, q, field, *value, userID); err != nil {
			return err
		}
	}

	return nil
}

// assertProjectInContext rejects a project that lives in a different context
// than the row referencing it, which would otherwise be silently incoherent.
func assertProjectInContext(ctx context.Context, q querier, userID, projectID, contextID string) error {
	var actual string

	err := q.QueryRowContext(ctx,
		`SELECT context_id FROM projects WHERE id = ? AND user_id = ? AND deleted_at IS NULL`,
		projectID, userID,
	).Scan(&actual)
	if err != nil {
		return notFoundOr(err, "read project context")
	}

	if actual != contextID {
		return &ConflictError{
			Field:  "project_id",
			Detail: "belongs to a different context",
		}
	}

	return nil
}

func scanRecurrence(sc scanner) (model.Recurrence, error) {
	var (
		v      model.Recurrence
		active int64
	)

	err := sc.Scan(&v.ID, &v.Kind, &v.ContextID, &v.ProjectID, &v.Source, &v.Title, &v.Details,
		&v.DaySlot, &v.SlotOrder, &v.RRule, &v.Timezone, &v.EstimateMinutes, &v.DelegatedToID, &v.LeadDays,
		&v.StartsOn, &v.EndsOn, &v.NextOccurrenceOn, &v.LastSpawnedOn, &active,
		&v.CompletedAt,
		&v.CreatedAt, &v.UpdatedAt, &v.DeletedAt, &v.Rev)
	if err != nil {
		return model.Recurrence{}, notFoundOr(err, "scan recurrence")
	}

	v.Active = active != 0
	v.State = model.DeriveRecurrenceState(v.Active, v.CompletedAt)

	return v, nil
}
