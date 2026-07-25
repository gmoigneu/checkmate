package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/nls/checkmate/server/internal/model"
	"github.com/nls/checkmate/server/internal/patch"
)

// UserProfile is the editable part of an account.
//
// Email is deliberately absent: it is the join key for a federated identity, so
// changing it is a security-relevant operation that deserves its own deliberate
// flow rather than sitting in a profile form.
type UserProfile struct {
	ID       string `json:"user_id"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Timezone string `json:"timezone"`
}

// UserUpdate is what a profile PATCH may change.
type UserUpdate struct {
	Name     patch.Field[string]
	Timezone patch.Field[string]
}

// GetUserProfile reads the account.
func (s *Store) GetUserProfile(ctx context.Context, userID string) (UserProfile, error) {
	var p UserProfile

	err := s.db.QueryRowContext(ctx,
		`SELECT id, email, coalesce(name, email), timezone FROM users WHERE id = ?`,
		userID,
	).Scan(&p.ID, &p.Email, &p.Name, &p.Timezone)
	if err != nil {
		return UserProfile{}, notFoundOr(err, "get user profile")
	}

	return p, nil
}

// UpdateUserProfile applies a partial update to the account.
//
// Timezone matters more than it looks: it decides which day "today" is for the
// daily brief and for every recurrence that inherits it, so an account provisioned
// with the wrong default was previously stuck with it.
//
// The users table carries no rev, so a change here does not appear in the sync
// feed. Clients should re-read /v1/me when they foreground or sync rather than
// caching the profile indefinitely.
func (s *Store) UpdateUserProfile(ctx context.Context, userID string, in UserUpdate) (UserProfile, error) {
	b := &updateBuilder{}

	if in.Name.Present() {
		b.set("name", strings.TrimSpace(in.Name.Value))
	}

	if in.Timezone.Present() {
		b.set("timezone", in.Timezone.Value)
	}

	if b.empty() {
		return s.GetUserProfile(ctx, userID)
	}

	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET `+b.clause()+` WHERE id = ?`,
		append(b.args, userID)...,
	)
	if err != nil {
		return UserProfile{}, fmt.Errorf("store: update user profile: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return UserProfile{}, fmt.Errorf("store: update user profile: %w", err)
	}

	if affected == 0 {
		return UserProfile{}, ErrNotFound
	}

	return s.GetUserProfile(ctx, userID)
}

// MergePeople folds one person into another.
//
// Delegating by name creates a person on the spot, which is the right ergonomics
// for fast capture and guarantees that "Marc", "marc" and "Marc D." accumulate.
// This is the way back: every task and recurrence pointing at sourceID is repointed
// at targetID, and the source is tombstoned.
//
// The target keeps its own name, email and notes. Merging is not reversible beyond
// re-delegating by hand, so the caller should confirm.
func (s *Store) MergePeople(ctx context.Context, userID, sourceID, targetID string) (int64, error) {
	if sourceID == targetID {
		return 0, &ConflictError{Field: "into", Detail: "cannot merge a person into themselves"}
	}

	var moved int64

	err := s.tx(ctx, func(tx *sql.Tx) error {
		// Both ends must exist and belong to the caller. Checked inside the
		// transaction so a concurrent delete cannot land between check and write.
		if err := assertRowOwned(ctx, tx, "people", "id", sourceID, userID); err != nil {
			return ErrNotFound
		}

		if err := assertRowOwned(ctx, tx, "people", "id", targetID, userID); err != nil {
			return &InvalidRefError{Field: "into", ID: targetID}
		}

		// Repoint the work. A delegated task stays valid throughout: it never has
		// a moment with no delegate, so the status CHECK is never violated.
		res, err := tx.ExecContext(ctx,
			`UPDATE tasks SET delegated_to_id = ?, updated_at = `+nowExpr+`
			 WHERE user_id = ? AND delegated_to_id = ? AND deleted_at IS NULL`,
			targetID, userID, sourceID,
		)
		if err != nil {
			return fmt.Errorf("store: repoint tasks: %w", err)
		}

		if n, err := res.RowsAffected(); err == nil {
			moved = n
		}

		_, err = tx.ExecContext(ctx,
			`UPDATE recurrences SET delegated_to_id = ?, updated_at = `+nowExpr+`
			 WHERE user_id = ? AND delegated_to_id = ? AND deleted_at IS NULL`,
			targetID, userID, sourceID,
		)
		if err != nil {
			return fmt.Errorf("store: repoint recurrences: %w", err)
		}

		// Tombstone the source directly rather than calling DeletePerson, which
		// would un-delegate the very tasks just repointed.
		_, err = tx.ExecContext(ctx,
			`UPDATE people SET deleted_at = `+nowExpr+`, updated_at = `+nowExpr+`
			 WHERE user_id = ? AND id = ? AND deleted_at IS NULL`,
			userID, sourceID,
		)
		if err != nil {
			return fmt.Errorf("store: tombstone merged person: %w", err)
		}

		return nil
	})
	if err != nil {
		return 0, err
	}

	return moved, nil
}

// ErrNotDeleted means a restore was asked for something that is not tombstoned.
var ErrNotDeleted = errors.New("store: that task is not deleted")

// RestoreTask brings a tombstoned task and its subtree back.
//
// Scoped to one delete, by the batch id that delete stamped on every row it
// tombstoned. So a child deleted separately and earlier stays deleted, which matters
// because a restore that resurrected it would undo a decision the user made
// deliberately. Grouping by deleted_at instead was the first attempt and was wrong:
// the timestamps carry milliseconds, so two deletes in quick succession are
// indistinguishable.
//
// A tombstone with no batch predates that column, so only the named row is restored.
//
// Three references may have gone stale while the task was away, and each is
// repaired rather than restored into an incoherent state:
//
//   - a deleted context or project is dropped, returning the task to the inbox
//   - a parent that is still deleted is dropped, promoting the task to top level
//   - a deleted delegate is dropped, and a delegated task returns to todo
//
// What restore cannot do is re-establish edges that pointed *at* this task: the
// deletion cleared blocked_by_id on its dependents and that information is gone.
func (s *Store) RestoreTask(ctx context.Context, userID, taskID string) (model.Task, error) {
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var deletedAt, batch sql.NullString

		err := tx.QueryRowContext(ctx,
			`SELECT deleted_at, deleted_batch FROM tasks WHERE id = ? AND user_id = ?`,
			taskID, userID,
		).Scan(&deletedAt, &batch)

		switch {
		case errors.Is(err, sql.ErrNoRows):
			return ErrNotFound
		case err != nil:
			return fmt.Errorf("store: read task for restore: %w", err)
		}

		if !deletedAt.Valid {
			return ErrNotDeleted
		}

		// Clear the tombstone across everything that went down together.
		if batch.Valid && batch.String != "" {
			_, err = tx.ExecContext(ctx,
				`UPDATE tasks SET deleted_at = NULL, deleted_batch = NULL, updated_at = `+nowExpr+`
				 WHERE user_id = ? AND deleted_batch = ?`,
				userID, batch.String,
			)
		} else {
			_, err = tx.ExecContext(ctx,
				`UPDATE tasks SET deleted_at = NULL, updated_at = `+nowExpr+`
				 WHERE user_id = ? AND id = ?`,
				userID, taskID,
			)
		}

		if err != nil {
			return fmt.Errorf("store: restore subtree: %w", err)
		}

		// Repair references that died while these rows were tombstoned. Each
		// UPDATE names the restored set again rather than trusting a temp table,
		// which keeps the whole repair inside one transaction.
		repairs := []string{
			// A project whose context is gone was itself tombstoned, so checking
			// the project alone is enough to catch both.
			`UPDATE tasks SET project_id = NULL, updated_at = ` + nowExpr + `
			 WHERE user_id = ? AND deleted_at IS NULL AND project_id IS NOT NULL
			   AND project_id NOT IN (SELECT id FROM projects WHERE deleted_at IS NULL)`,

			`UPDATE tasks SET context_id = NULL, updated_at = ` + nowExpr + `
			 WHERE user_id = ? AND deleted_at IS NULL AND context_id IS NOT NULL
			   AND context_id NOT IN (SELECT id FROM contexts WHERE deleted_at IS NULL)`,

			`UPDATE tasks SET parent_id = NULL, updated_at = ` + nowExpr + `
			 WHERE user_id = ? AND deleted_at IS NULL AND parent_id IS NOT NULL
			   AND parent_id NOT IN (SELECT id FROM tasks WHERE deleted_at IS NULL)`,

			`UPDATE tasks SET blocked_by_id = NULL,
			                  status = CASE WHEN status = 'blocked' THEN 'todo' ELSE status END,
			                  updated_at = ` + nowExpr + `
			 WHERE user_id = ? AND deleted_at IS NULL AND blocked_by_id IS NOT NULL
			   AND blocked_by_id NOT IN (SELECT id FROM tasks WHERE deleted_at IS NULL)`,

			// Order matters here: clearing the delegate without moving the status
			// would violate the delegated-needs-a-delegate CHECK.
			`UPDATE tasks SET delegated_to_id = NULL,
			                  status = CASE WHEN status = 'delegated' THEN 'todo' ELSE status END,
			                  updated_at = ` + nowExpr + `
			 WHERE user_id = ? AND deleted_at IS NULL AND delegated_to_id IS NOT NULL
			   AND delegated_to_id NOT IN (SELECT id FROM people WHERE deleted_at IS NULL)`,
		}

		for _, query := range repairs {
			if _, err := tx.ExecContext(ctx, query, userID); err != nil {
				return fmt.Errorf("store: repair restored task references: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		return model.Task{}, err
	}

	return s.GetTask(ctx, userID, taskID)
}
