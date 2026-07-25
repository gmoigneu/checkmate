package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/nls/checkmate/server/internal/id"
	"github.com/nls/checkmate/server/internal/model"
	"github.com/nls/checkmate/server/internal/patch"
)

const recurrenceColumns = `id, context_id, project_id, source_key, title, details,
	rrule, timezone, estimate_minutes, delegated_to_id, lead_days, starts_on,
	ends_on, next_occurrence_on, last_spawned_on, active,
	created_at, updated_at, deleted_at, rev`

// RecurrenceCreate is the input for creating a recurrence template.
type RecurrenceCreate struct {
	ContextID       string
	ProjectID       *string
	Source          *string
	Title           string
	Details         *string
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

	if f.Active != nil {
		c.add("active = ?", boolToInt(*f.Active))
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

	timezone := in.Timezone
	if timezone == "" {
		timezone = "UTC"
	}

	var leadDays int64
	if in.LeadDays != nil {
		leadDays = *in.LeadDays
	}

	active := int64(1)
	if in.Active != nil {
		active = boolToInt(*in.Active)
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
			`INSERT INTO recurrences (id, user_id, context_id, project_id, source_key, title,
				details, rrule, timezone, estimate_minutes, delegated_to_id, lead_days,
				starts_on, ends_on, next_occurrence_on, active)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			newID, userID, in.ContextID, in.ProjectID, in.Source, in.Title,
			in.Details, in.RRule, timezone, in.EstimateMinutes, in.DelegatedToID, leadDays,
			in.StartsOn, in.EndsOn, in.StartsOn, active,
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

		b := &updateBuilder{}

		applyField(b, "title", in.Title)
		applyField(b, "details", in.Details)
		applyField(b, "rrule", in.RRule)
		applyField(b, "timezone", in.Timezone)
		applyField(b, "estimate_minutes", in.EstimateMinutes)
		applyField(b, "lead_days", in.LeadDays)
		applyField(b, "starts_on", in.StartsOn)
		applyField(b, "ends_on", in.EndsOn)
		applyField(b, "source_key", in.Source)
		applyBoolField(b, "active", in.Active)

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

// DeleteRecurrence tombstones a template.
//
// Occurrences already spawned from it are left alone: they are real tasks with
// real history, and deleting a series should not rewrite the past. They keep
// reporting as kind "recurring" via their recurrence_id.
func (s *Store) DeleteRecurrence(ctx context.Context, userID, recurrenceID string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if err := assertRowOwned(ctx, tx, "recurrences", "id", recurrenceID, userID); err != nil {
			return ErrNotFound
		}

		_, err := tx.ExecContext(ctx,
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

	err := sc.Scan(&v.ID, &v.ContextID, &v.ProjectID, &v.Source, &v.Title, &v.Details,
		&v.RRule, &v.Timezone, &v.EstimateMinutes, &v.DelegatedToID, &v.LeadDays,
		&v.StartsOn, &v.EndsOn, &v.NextOccurrenceOn, &v.LastSpawnedOn, &active,
		&v.CreatedAt, &v.UpdatedAt, &v.DeletedAt, &v.Rev)
	if err != nil {
		return model.Recurrence{}, notFoundOr(err, "scan recurrence")
	}

	v.Active = active != 0

	return v, nil
}
