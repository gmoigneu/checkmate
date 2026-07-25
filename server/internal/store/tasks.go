package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/nls/checkmate/server/internal/id"
	"github.com/nls/checkmate/server/internal/model"
	"github.com/nls/checkmate/server/internal/patch"
)

// Read through the view so kind comes back derived rather than stored.
const taskColumns = `id, context_id, project_id, parent_id, recurrence_id, occurrence_on,
	source_key, capture_method, title, details, status, due_on, planned_on,
	estimate_minutes, delegated_to_id, blocked_by_id, reference_url, reference_label,
	kind, completed_at, cancelled_at, created_at, updated_at, deleted_at, rev`

// TaskCreate is the input for creating a task.
type TaskCreate struct {
	ContextID       *string
	ProjectID       *string
	ParentID        *string
	Source          *string
	CaptureMethod   string
	Title           string
	Details         *string
	Status          string
	DueOn           *string
	PlannedOn       *string
	EstimateMinutes *int64
	DelegatedToID   *string
	BlockedByID     *string
	ReferenceURL    *string
	ReferenceLabel  *string
}

// TaskUpdate is the set of task fields a PATCH may change.
//
// recurrence_id and occurrence_on are deliberately absent: they are set by the
// spawner when it materializes an occurrence, and letting a client rewrite them
// would break the idempotency of spawning.
type TaskUpdate struct {
	ContextID       patch.Field[string]
	ProjectID       patch.Field[string]
	ParentID        patch.Field[string]
	Source          patch.Field[string]
	Title           patch.Field[string]
	Details         patch.Field[string]
	Status          patch.Field[string]
	DueOn           patch.Field[string]
	PlannedOn       patch.Field[string]
	EstimateMinutes patch.Field[int64]
	DelegatedToID   patch.Field[string]
	BlockedByID     patch.Field[string]
	ReferenceURL    patch.Field[string]
	ReferenceLabel  patch.Field[string]
}

// TaskFilter narrows a task listing.
type TaskFilter struct {
	listOptions

	Status []string
	Kind   []string

	// ContextID filters to one context; ContextIsNull selects the inbox, where
	// quick-captured tasks land before triage.
	ContextID     string
	ContextIsNull bool

	ProjectID     string
	ProjectIsNull bool

	// ParentID filters to the subtasks of one task; TopLevelOnly excludes every
	// subtask, which is what a normal list wants.
	ParentID     string
	TopLevelOnly bool

	DelegatedToID string
	RecurrenceID  string

	PlannedOn     string
	PlannedBefore string
	PlannedAfter  string
	DueOn         string
	DueBefore     string
	DueAfter      string

	Search string
}

// ListTasks returns the caller's tasks, newest first.
func (s *Store) ListTasks(ctx context.Context, userID string, f TaskFilter) ([]model.Task, string, error) {
	c := &conditions{}
	c.add("user_id = ?", userID)

	if !f.IncludeDeleted {
		c.add("deleted_at IS NULL")
	}

	c.addIn("status", f.Status)
	c.addIn("kind", f.Kind)

	switch {
	case f.ContextIsNull:
		c.add("context_id IS NULL")
	case f.ContextID != "":
		c.add("context_id = ?", f.ContextID)
	}

	switch {
	case f.ProjectIsNull:
		c.add("project_id IS NULL")
	case f.ProjectID != "":
		c.add("project_id = ?", f.ProjectID)
	}

	switch {
	case f.TopLevelOnly:
		c.add("parent_id IS NULL")
	case f.ParentID != "":
		c.add("parent_id = ?", f.ParentID)
	}

	if f.DelegatedToID != "" {
		c.add("delegated_to_id = ?", f.DelegatedToID)
	}

	if f.RecurrenceID != "" {
		c.add("recurrence_id = ?", f.RecurrenceID)
	}

	for _, r := range []struct {
		column string
		op     string
		value  string
	}{
		{"planned_on", "=", f.PlannedOn},
		{"planned_on", "<=", f.PlannedBefore},
		{"planned_on", ">=", f.PlannedAfter},
		{"due_on", "=", f.DueOn},
		{"due_on", "<=", f.DueBefore},
		{"due_on", ">=", f.DueAfter},
	} {
		if r.value != "" {
			c.add(r.column+" "+r.op+" ?", r.value)
		}
	}

	if f.Search != "" {
		c.add(`(title LIKE ? ESCAPE '\' OR coalesce(details, '') LIKE ? ESCAPE '\')`,
			likeArg(f.Search), likeArg(f.Search))
	}

	if f.Cursor != "" {
		c.add("id < ?", f.Cursor)
	}

	limit := f.normalize()

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+taskColumns+` FROM tasks_with_kind`+c.clause()+` ORDER BY id DESC LIMIT ?`,
		append(c.args, limit+1)...,
	)
	if err != nil {
		return nil, "", fmt.Errorf("store: list tasks: %w", err)
	}
	defer rows.Close()

	var out []model.Task

	for rows.Next() {
		v, err := scanTask(rows)
		if err != nil {
			return nil, "", err
		}

		out = append(out, v)
	}

	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("store: list tasks: %w", err)
	}

	return page(out, limit, func(v model.Task) string { return v.ID })
}

// GetTask returns one task owned by userID.
func (s *Store) GetTask(ctx context.Context, userID, taskID string) (model.Task, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+taskColumns+` FROM tasks_with_kind
		 WHERE id = ? AND user_id = ? AND deleted_at IS NULL`,
		taskID, userID,
	)

	return scanTask(row)
}

// CreateTask inserts a task, validating every reference it carries.
func (s *Store) CreateTask(ctx context.Context, userID string, in TaskCreate) (model.Task, error) {
	newID := id.New()

	captureMethod := in.CaptureMethod
	if captureMethod == "" {
		captureMethod = "api"
	}

	status := in.Status
	if status == "" {
		// No context means it has not been triaged yet, so it belongs in the inbox.
		if in.ContextID == nil {
			status = model.StatusInbox
		} else {
			status = model.StatusTodo
		}
	}

	err := s.tx(ctx, func(tx *sql.Tx) error {
		if err := assertRefsOwned(ctx, tx, userID, map[string]*string{
			"context_id":      in.ContextID,
			"project_id":      in.ProjectID,
			"parent_id":       in.ParentID,
			"delegated_to_id": in.DelegatedToID,
			"blocked_by_id":   in.BlockedByID,
		}); err != nil {
			return err
		}

		if in.ProjectID != nil {
			if in.ContextID == nil {
				return &ConflictError{Field: "project_id", Detail: "requires a context_id"}
			}

			if err := assertProjectInContext(ctx, tx, userID, *in.ProjectID, *in.ContextID); err != nil {
				return err
			}
		}

		if in.Source != nil {
			if err := assertSourceExists(ctx, tx, *in.Source); err != nil {
				return err
			}
		}

		if status == model.StatusDelegated && in.DelegatedToID == nil {
			return &ConflictError{Field: "delegated_to_id", Detail: "is required when status is delegated"}
		}

		// Both timestamps come from sqlite's clock so every row in a request
		// shares one instant; the expressions are literals, not input.
		completedAt, cancelledAt := "NULL", "NULL"

		if status == model.StatusDone {
			completedAt = nowExpr
		}

		if status == model.StatusCancelled {
			cancelledAt = nowExpr
		}

		_, err := tx.ExecContext(ctx,
			`INSERT INTO tasks (id, user_id, context_id, project_id, parent_id, source_key,
				capture_method, title, details, status, due_on, planned_on, estimate_minutes,
				delegated_to_id, blocked_by_id, reference_url, reference_label,
				completed_at, cancelled_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, `+
				completedAt+`, `+cancelledAt+`)`,
			newID, userID, in.ContextID, in.ProjectID, in.ParentID, in.Source,
			captureMethod, in.Title, in.Details, status, in.DueOn, in.PlannedOn, in.EstimateMinutes,
			in.DelegatedToID, in.BlockedByID, in.ReferenceURL, in.ReferenceLabel,
		)
		if err != nil {
			return fmt.Errorf("store: insert task: %w", err)
		}

		return nil
	})
	if err != nil {
		return model.Task{}, err
	}

	return s.GetTask(ctx, userID, newID)
}

// UpdateTask applies a partial update, re-validating every reference it touches.
func (s *Store) UpdateTask(ctx context.Context, userID, taskID string, in TaskUpdate) (model.Task, error) {
	err := s.tx(ctx, func(tx *sql.Tx) error {
		current, err := getTaskTx(ctx, tx, userID, taskID)
		if err != nil {
			return err
		}

		b := &updateBuilder{}

		applyField(b, "title", in.Title)
		applyField(b, "details", in.Details)
		applyField(b, "due_on", in.DueOn)
		applyField(b, "planned_on", in.PlannedOn)
		applyField(b, "estimate_minutes", in.EstimateMinutes)
		applyField(b, "reference_url", in.ReferenceURL)
		applyField(b, "reference_label", in.ReferenceLabel)

		if in.Source.Set {
			if in.Source.Null {
				b.set("source_key", nil)
			} else {
				if err := assertSourceExists(ctx, tx, in.Source.Value); err != nil {
					return err
				}

				b.set("source_key", in.Source.Value)
			}
		}

		// Resolve context and project together: the pair has to stay coherent
		// whichever of the two the caller actually changed.
		contextID := current.ContextID
		if in.ContextID.Set {
			if in.ContextID.Null {
				contextID = nil
				b.set("context_id", nil)
			} else {
				if err := assertOwned(ctx, tx, "context_id", in.ContextID.Value, userID); err != nil {
					return err
				}

				contextID = &in.ContextID.Value
				b.set("context_id", in.ContextID.Value)
			}
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
			if contextID == nil {
				return &ConflictError{Field: "project_id", Detail: "requires a context_id"}
			}

			if err := assertProjectInContext(ctx, tx, userID, *projectID, *contextID); err != nil {
				return err
			}
		}

		// Clearing the context sends a task back to the inbox, so its project
		// has to go with it.
		if in.ContextID.Set && in.ContextID.Null && !in.ProjectID.Set {
			b.set("project_id", nil)
		}

		if err := applyGraphEdge(ctx, tx, b, userID, taskID, "parent_id", in.ParentID); err != nil {
			return err
		}

		if err := applyGraphEdge(ctx, tx, b, userID, taskID, "blocked_by_id", in.BlockedByID); err != nil {
			return err
		}

		delegatedTo := current.DelegatedToID
		if in.DelegatedToID.Set {
			if in.DelegatedToID.Null {
				delegatedTo = nil
				b.set("delegated_to_id", nil)
			} else {
				if err := assertOwned(ctx, tx, "delegated_to_id", in.DelegatedToID.Value, userID); err != nil {
					return err
				}

				delegatedTo = &in.DelegatedToID.Value
				b.set("delegated_to_id", in.DelegatedToID.Value)
			}
		}

		if err := applyStatus(b, in.Status, current.Status, delegatedTo); err != nil {
			return err
		}

		if b.empty() {
			return nil
		}

		_, err = tx.ExecContext(ctx,
			`UPDATE tasks SET `+b.clause()+` WHERE id = ? AND user_id = ?`,
			append(b.args, taskID, userID)...,
		)
		if err != nil {
			return fmt.Errorf("store: update task: %w", err)
		}

		return nil
	})
	if err != nil {
		return model.Task{}, err
	}

	return s.GetTask(ctx, userID, taskID)
}

// applyGraphEdge validates and stages parent_id or blocked_by_id, rejecting
// references that are not owned and edges that would close a cycle.
func applyGraphEdge(
	ctx context.Context,
	tx *sql.Tx,
	b *updateBuilder,
	userID, taskID, column string,
	f patch.Field[string],
) error {
	if !f.Set {
		return nil
	}

	if f.Null {
		b.set(column, nil)

		return nil
	}

	if f.Value == taskID {
		return &ConflictError{Field: column, Detail: "cannot reference itself"}
	}

	if err := assertOwned(ctx, tx, column, f.Value, userID); err != nil {
		return err
	}

	if err := assertNoCycle(ctx, tx, column, taskID, f.Value); err != nil {
		return err
	}

	b.set(column, f.Value)

	return nil
}

// applyStatus stages a status change and keeps completed_at / cancelled_at in
// step with it, so those timestamps never drift from the status they describe.
//
// delegatedTo is the delegate the task will have after this request, which may
// differ from the stored one. Checking against the post-update value catches
// both "become delegated with nobody named" and "clear the delegate but stay
// delegated" — the second would otherwise surface as a raw CHECK violation.
func applyStatus(b *updateBuilder, in patch.Field[string], currentStatus string, delegatedTo *string) error {
	if in.Set && in.Null {
		return &ConflictError{Field: "status", Detail: "cannot be null"}
	}

	next := currentStatus
	if in.Set {
		next = in.Value
	}

	if next == model.StatusDelegated && delegatedTo == nil {
		return &ConflictError{
			Field:  "delegated_to_id",
			Detail: "is required while status is delegated",
		}
	}

	if !in.Set {
		return nil
	}

	b.set("status", next)

	switch {
	case next == model.StatusDone && currentStatus != model.StatusDone:
		b.setExpr("completed_at", nowExpr)
	case next != model.StatusDone && currentStatus == model.StatusDone:
		b.set("completed_at", nil)
	}

	switch {
	case next == model.StatusCancelled && currentStatus != model.StatusCancelled:
		b.setExpr("cancelled_at", nowExpr)
	case next != model.StatusCancelled && currentStatus == model.StatusCancelled:
		b.set("cancelled_at", nil)
	}

	return nil
}

// DeleteTask tombstones a task and its whole subtree.
//
// Anything blocked by a deleted task is unblocked, otherwise those tasks would
// wait forever on something that no longer exists.
func (s *Store) DeleteTask(ctx context.Context, userID, taskID string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if err := assertRowOwned(ctx, tx, "tasks", "id", taskID, userID); err != nil {
			return ErrNotFound
		}

		// Collect the subtree first: the tombstone has to reach descendants at
		// every depth, not just direct children.
		_, err := tx.ExecContext(ctx, `
			WITH RECURSIVE subtree(id) AS (
				SELECT ?
				UNION
				SELECT t.id FROM tasks t JOIN subtree ON t.parent_id = subtree.id
			)
			UPDATE tasks
			SET deleted_at = `+nowExpr+`, updated_at = `+nowExpr+`
			WHERE user_id = ? AND deleted_at IS NULL AND id IN (SELECT id FROM subtree)`,
			taskID, userID,
		)
		if err != nil {
			return fmt.Errorf("store: delete task subtree: %w", err)
		}

		_, err = tx.ExecContext(ctx, `
			WITH RECURSIVE subtree(id) AS (
				SELECT ?
				UNION
				SELECT t.id FROM tasks t JOIN subtree ON t.parent_id = subtree.id
			)
			UPDATE tasks
			SET blocked_by_id = NULL,
			    status = CASE WHEN status = 'blocked' THEN 'todo' ELSE status END,
			    updated_at = `+nowExpr+`
			WHERE user_id = ? AND deleted_at IS NULL
			  AND blocked_by_id IN (SELECT id FROM subtree)`,
			taskID, userID,
		)
		if err != nil {
			return fmt.Errorf("store: unblock dependents: %w", err)
		}

		return nil
	})
}

// ListSources returns the global source lookup.
func (s *Store) ListSources(ctx context.Context) ([]model.Source, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT key, label, sort_order FROM sources ORDER BY sort_order`)
	if err != nil {
		return nil, fmt.Errorf("store: list sources: %w", err)
	}
	defer rows.Close()

	out := []model.Source{}

	for rows.Next() {
		var v model.Source

		if err := rows.Scan(&v.Key, &v.Label, &v.SortOrder); err != nil {
			return nil, fmt.Errorf("store: scan source: %w", err)
		}

		out = append(out, v)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list sources: %w", err)
	}

	return out, nil
}

func getTaskTx(ctx context.Context, tx *sql.Tx, userID, taskID string) (model.Task, error) {
	row := tx.QueryRowContext(ctx,
		`SELECT `+taskColumns+` FROM tasks_with_kind
		 WHERE id = ? AND user_id = ? AND deleted_at IS NULL`,
		taskID, userID,
	)

	return scanTask(row)
}

func scanTask(sc scanner) (model.Task, error) {
	var v model.Task

	err := sc.Scan(&v.ID, &v.ContextID, &v.ProjectID, &v.ParentID, &v.RecurrenceID, &v.OccurrenceOn,
		&v.Source, &v.CaptureMethod, &v.Title, &v.Details, &v.Status, &v.DueOn, &v.PlannedOn,
		&v.EstimateMinutes, &v.DelegatedToID, &v.BlockedByID, &v.ReferenceURL, &v.ReferenceLabel,
		&v.Kind, &v.CompletedAt, &v.CancelledAt, &v.CreatedAt, &v.UpdatedAt, &v.DeletedAt, &v.Rev)
	if err != nil {
		return model.Task{}, notFoundOr(err, "scan task")
	}

	return v, nil
}
