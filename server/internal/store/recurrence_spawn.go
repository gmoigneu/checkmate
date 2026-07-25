package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/nls/checkmate/server/internal/id"
)

// DueRecurrence is a template the spawner needs to materialize from.
//
// Deliberately not model.Recurrence: this carries user_id, which the API type
// omits because a caller never needs to be told their own id.
type DueRecurrence struct {
	ID               string
	UserID           string
	ContextID        string
	ProjectID        *string
	SourceKey        *string
	Title            string
	Details          *string
	RRule            string
	Timezone         string
	EstimateMinutes  *int64
	DelegatedToID    *string
	LeadDays         int64
	StartsOn         string
	EndsOn           *string
	NextOccurrenceOn *string
	LastSpawnedOn    *string
}

// ListDueRecurrences returns every active template that may owe an occurrence.
//
// Not scoped to a user, unlike the rest of this package: the spawner is a system
// process that walks every account. The rows it goes on to write take their
// user_id from the template, so ownership is still carried through.
func (s *Store) ListDueRecurrences(ctx context.Context, horizon string) ([]DueRecurrence, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, context_id, project_id, source_key, title, details, rrule,
		       timezone, estimate_minutes, delegated_to_id, lead_days, starts_on,
		       ends_on, next_occurrence_on, last_spawned_on
		FROM recurrences
		WHERE active = 1
		  AND deleted_at IS NULL
		  AND (next_occurrence_on IS NULL OR next_occurrence_on <= ?)
		ORDER BY id`,
		horizon,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list due recurrences: %w", err)
	}
	defer rows.Close()

	var out []DueRecurrence

	for rows.Next() {
		var r DueRecurrence

		err := rows.Scan(&r.ID, &r.UserID, &r.ContextID, &r.ProjectID, &r.SourceKey,
			&r.Title, &r.Details, &r.RRule, &r.Timezone, &r.EstimateMinutes,
			&r.DelegatedToID, &r.LeadDays, &r.StartsOn, &r.EndsOn,
			&r.NextOccurrenceOn, &r.LastSpawnedOn)
		if err != nil {
			return nil, fmt.Errorf("store: scan due recurrence: %w", err)
		}

		out = append(out, r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list due recurrences: %w", err)
	}

	return out, nil
}

// GetDueRecurrence loads one template for spawning, scoped to its owner.
//
// Used when a template has just been created or edited, so its occurrences appear
// immediately instead of at the next scheduler tick.
func (s *Store) GetDueRecurrence(ctx context.Context, userID, recurrenceID string) (DueRecurrence, error) {
	var r DueRecurrence

	err := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, context_id, project_id, source_key, title, details, rrule,
		       timezone, estimate_minutes, delegated_to_id, lead_days, starts_on,
		       ends_on, next_occurrence_on, last_spawned_on
		FROM recurrences
		WHERE id = ? AND user_id = ? AND active = 1 AND deleted_at IS NULL`,
		recurrenceID, userID,
	).Scan(&r.ID, &r.UserID, &r.ContextID, &r.ProjectID, &r.SourceKey,
		&r.Title, &r.Details, &r.RRule, &r.Timezone, &r.EstimateMinutes,
		&r.DelegatedToID, &r.LeadDays, &r.StartsOn, &r.EndsOn,
		&r.NextOccurrenceOn, &r.LastSpawnedOn)
	if err != nil {
		return DueRecurrence{}, notFoundOr(err, "get due recurrence")
	}

	return r, nil
}

// SpawnOccurrence materializes one occurrence, reporting whether it created a
// row.
//
// ON CONFLICT DO NOTHING against the unique (recurrence_id, occurrence_on) index
// is what makes the spawner idempotent: two passes racing, or a re-run after a
// crash, cannot produce a duplicate task.
//
// That index is partial, and sqlite requires the conflict target to repeat the
// index predicate to match it. Omitting the WHERE does not fall back to a plain
// upsert -- it matches no constraint at all and the statement fails outright.
func (s *Store) SpawnOccurrence(ctx context.Context, template DueRecurrence, occurrenceOn string) (bool, error) {
	// A delegated occurrence needs its delegate, or the status CHECK on tasks
	// would reject the row.
	status := "todo"
	if template.DelegatedToID != nil {
		status = "delegated"
	}

	res, err := s.db.ExecContext(ctx,
		`INSERT INTO tasks (id, user_id, context_id, project_id, source_key, capture_method,
			title, details, status, due_on, estimate_minutes, delegated_to_id,
			recurrence_id, occurrence_on)
		 VALUES (?, ?, ?, ?, ?, 'recurrence', ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (recurrence_id, occurrence_on)
		   WHERE recurrence_id IS NOT NULL AND occurrence_on IS NOT NULL
		 DO NOTHING`,
		id.New(), template.UserID, template.ContextID, template.ProjectID, template.SourceKey,
		template.Title, template.Details, status, occurrenceOn, template.EstimateMinutes,
		template.DelegatedToID, template.ID, occurrenceOn,
	)
	if err != nil {
		return false, fmt.Errorf("store: spawn occurrence: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: spawn occurrence: %w", err)
	}

	return affected > 0, nil
}

// AdvanceRecurrence records where a series got to.
//
// nextOn is nil when the series is finished. deactivate flips active off rather
// than deleting the row, because the occurrences it already spawned are real
// tasks with real history and a deleted template would orphan them.
//
// Deactivating here always means the series ran out -- the spawner only looks at
// active templates, so it never sees a paused one -- and that is what stamps
// completed_at. A person pausing a series leaves completed_at null, which is how
// the two become distinguishable.
func (s *Store) AdvanceRecurrence(
	ctx context.Context,
	recurrenceID string,
	nextOn, lastSpawnedOn *string,
	deactivate bool,
) error {
	active := 1
	completedAt := "completed_at"

	if deactivate {
		active = 0

		// coalesce so a series retired twice keeps the first timestamp.
		completedAt = "coalesce(completed_at, " + nowExpr + ")"
	}

	_, err := s.db.ExecContext(ctx,
		`UPDATE recurrences
		 SET next_occurrence_on = ?,
		     last_spawned_on = coalesce(?, last_spawned_on),
		     active = ?,
		     completed_at = `+completedAt+`,
		     updated_at = `+nowExpr+`
		 WHERE id = ?`,
		nextOn, lastSpawnedOn, active, recurrenceID,
	)
	if err != nil {
		return fmt.Errorf("store: advance recurrence: %w", err)
	}

	return nil
}

// CountOccurrences reports how many tasks a series has spawned, for tests and for
// the API's view of a template.
func (s *Store) CountOccurrences(ctx context.Context, userID, recurrenceID string) (int, error) {
	var n int

	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM tasks
		 WHERE user_id = ? AND recurrence_id = ? AND deleted_at IS NULL`,
		userID, recurrenceID,
	).Scan(&n)

	switch {
	case err == nil:
		return n, nil
	case err == sql.ErrNoRows:
		return 0, nil
	default:
		return 0, fmt.Errorf("store: count occurrences: %w", err)
	}
}
