package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/nls/checkmate/server/internal/database"
	"github.com/nls/checkmate/server/internal/model"
)

const routineOpenStatusesSQL = "('" + model.StatusTodo + "', '" +
	model.StatusInProgress + "', '" + model.StatusBlocked + "', '" +
	model.StatusDelegated + "')"

var routineOpenStatuses = []string{
	model.StatusTodo,
	model.StatusInProgress,
	model.StatusBlocked,
	model.StatusDelegated,
}

// ExpireRoutineTasks closes routine occurrences whose account-local day has
// ended. Expiration is terminal: the stored cancelled status satisfies the
// original table constraint, while expired_at makes the API expose "expired".
func (s *Store) ExpireRoutineTasks(ctx context.Context, now time.Time) (int, error) {
	return s.expireRoutineTasks(ctx, "", now)
}

// ExpireRoutineTasksForUser is the request-scoped form used by the Brief. It
// avoids making one person's read walk or mutate every other account.
func (s *Store) ExpireRoutineTasksForUser(
	ctx context.Context,
	userID string,
	now time.Time,
) (int, error) {
	return s.expireRoutineTasks(ctx, userID, now)
}

func (s *Store) expireRoutineTasks(
	ctx context.Context,
	userID string,
	now time.Time,
) (int, error) {
	in, args := inClause(routineOpenStatuses)
	userClause := ""
	if userID != "" {
		userClause = " AND t.user_id = ?"
		args = append(args, userID)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.occurrence_on, u.timezone
		FROM tasks t
		JOIN recurrences r ON r.id = t.recurrence_id
		JOIN users u ON u.id = t.user_id
		WHERE r.kind = 'routine'
		  AND t.deleted_at IS NULL
		  AND t.expired_at IS NULL
		  AND t.occurrence_on IS NOT NULL
		  AND t.status IN `+in+userClause,
		args...,
	)
	if err != nil {
		return 0, fmt.Errorf("store: list routine tasks to expire: %w", err)
	}

	var ids []string
	for rows.Next() {
		var id, occurrenceOn, timezone string
		if err := rows.Scan(&id, &occurrenceOn, &timezone); err != nil {
			rows.Close()

			return 0, fmt.Errorf("store: scan routine task to expire: %w", err)
		}

		location, err := time.LoadLocation(timezone)
		if err != nil {
			location = time.UTC
		}

		if occurrenceOn < now.In(location).Format(database.DateOnly) {
			ids = append(ids, id)
		}
	}

	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("store: close routine expiration rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("store: read routine tasks to expire: %w", err)
	}

	return s.expireRoutineTaskIDs(ctx, ids, now)
}

func (s *Store) expireRoutineTaskIDs(ctx context.Context, ids []string, now time.Time) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: begin routine expiration: %w", err)
	}
	defer tx.Rollback()

	expiredAt := now.UTC().Format(database.Timestamp)
	expired := 0

	for _, taskID := range ids {
		res, err := tx.ExecContext(ctx, `
			UPDATE tasks
			SET status = 'cancelled',
			    completed_at = NULL,
			    cancelled_at = NULL,
			    expired_at = ?,
			    updated_at = ?
			WHERE id = ?
			  AND deleted_at IS NULL
			  AND expired_at IS NULL
			  AND status IN `+routineOpenStatusesSQL,
			expiredAt, expiredAt, taskID,
		)
		if err != nil {
			return 0, fmt.Errorf("store: expire routine task: %w", err)
		}

		affected, err := res.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("store: count expired routine task: %w", err)
		}
		expired += int(affected)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: commit routine expiration: %w", err)
	}

	return expired, nil
}

// PrepareRoutineRespawn rewinds a routine template to the account's current
// day. This lets a weekday edit add today's occurrence even when the previous
// cursor had already advanced beyond it.
func (s *Store) PrepareRoutineRespawn(
	ctx context.Context,
	userID, recurrenceID string,
	now time.Time,
) error {
	var timezone string
	err := s.db.QueryRowContext(ctx,
		`SELECT timezone FROM users WHERE id = ?`,
		userID,
	).Scan(&timezone)
	if err != nil {
		return notFoundOr(err, "read routine timezone")
	}

	location, err := time.LoadLocation(timezone)
	if err != nil {
		location = time.UTC
	}

	today := now.In(location).Format(database.DateOnly)
	res, err := s.db.ExecContext(ctx, `
		UPDATE recurrences
		SET next_occurrence_on = CASE WHEN starts_on > ? THEN starts_on ELSE ? END,
		    timezone = ?,
		    updated_at = `+nowExpr+`
		WHERE id = ? AND user_id = ? AND kind = 'routine' AND deleted_at IS NULL`,
		today, today, timezone, recurrenceID, userID,
	)
	if err != nil {
		return fmt.Errorf("store: prepare routine respawn: %w", err)
	}

	if affected, err := res.RowsAffected(); err != nil || affected == 0 {
		if err != nil {
			return fmt.Errorf("store: prepare routine respawn: %w", err)
		}

		return ErrNotFound
	}

	return nil
}

// ReconcileRoutineOccurrence applies a template edit to today's still-open
// occurrence, or expires it when the edited weekday rule no longer includes
// today. Completed, cancelled, and expired history is never rewritten.
func (s *Store) ReconcileRoutineOccurrence(
	ctx context.Context,
	template DueRecurrence,
	occurrenceOn string,
	applies bool,
	now time.Time,
) error {
	if !applies {
		_, err := s.db.ExecContext(ctx, `
			UPDATE tasks
			SET status = 'cancelled', completed_at = NULL, cancelled_at = NULL,
			    expired_at = ?, updated_at = ?
			WHERE user_id = ? AND recurrence_id = ? AND occurrence_on = ?
			  AND deleted_at IS NULL AND expired_at IS NULL
			  AND status IN `+routineOpenStatusesSQL,
			now.UTC().Format(database.Timestamp), now.UTC().Format(database.Timestamp),
			template.UserID, template.ID, occurrenceOn,
		)
		if err != nil {
			return fmt.Errorf("store: expire removed routine occurrence: %w", err)
		}

		return nil
	}

	statusExpr := `CASE
		WHEN status = 'delegated' AND ? IS NULL THEN 'todo'
		ELSE status
	END`
	_, err := s.db.ExecContext(ctx, `
		UPDATE tasks
		SET context_id = ?, project_id = ?, source_key = ?, title = ?, details = ?,
		    planned_on = occurrence_on, day_slot = ?, slot_order = ?,
		    estimate_minutes = ?, delegated_to_id = ?,
		    status = `+statusExpr+`, updated_at = `+nowExpr+`
		WHERE user_id = ? AND recurrence_id = ? AND occurrence_on = ?
		  AND deleted_at IS NULL AND expired_at IS NULL
		  AND status IN `+routineOpenStatusesSQL,
		template.ContextID, template.ProjectID, template.SourceKey, template.Title, template.Details,
		template.DaySlot, template.SlotOrder, template.EstimateMinutes, template.DelegatedToID,
		template.DelegatedToID,
		template.UserID, template.ID, occurrenceOn,
	)
	if err != nil {
		return fmt.Errorf("store: reconcile routine occurrence: %w", err)
	}

	return nil
}

func expireOpenRoutineOccurrencesTx(
	ctx context.Context,
	tx *sql.Tx,
	userID, recurrenceID string,
) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE tasks
		SET status = 'cancelled', completed_at = NULL, cancelled_at = NULL,
		    expired_at = `+nowExpr+`, updated_at = `+nowExpr+`
		WHERE user_id = ? AND recurrence_id = ?
		  AND deleted_at IS NULL AND expired_at IS NULL
		  AND status IN `+routineOpenStatusesSQL,
		userID, recurrenceID,
	)
	if err != nil {
		return fmt.Errorf("store: expire open routine occurrences: %w", err)
	}

	return nil
}
