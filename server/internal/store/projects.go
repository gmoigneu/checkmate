package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/nls/checkmate/server/internal/id"
	"github.com/nls/checkmate/server/internal/model"
	"github.com/nls/checkmate/server/internal/patch"
)

const projectColumns = `id, context_id, name, description, status,
	created_at, updated_at, deleted_at, rev`

// ProjectCreate is the input for creating a project.
type ProjectCreate struct {
	ContextID   string
	Name        string
	Description *string
	Status      string
}

// ProjectUpdate is the set of project fields a PATCH may change.
type ProjectUpdate struct {
	ContextID   patch.Field[string]
	Name        patch.Field[string]
	Description patch.Field[string]
	Status      patch.Field[string]
}

// ProjectFilter narrows a project listing.
type ProjectFilter struct {
	listOptions

	ContextID string
	Status    []string
}

// ListProjects returns the caller's projects, newest first.
func (s *Store) ListProjects(ctx context.Context, userID string, f ProjectFilter) ([]model.Project, string, error) {
	c := &conditions{}
	c.add("user_id = ?", userID)

	if !f.IncludeDeleted {
		c.add("deleted_at IS NULL")
	}

	if f.ContextID != "" {
		c.add("context_id = ?", f.ContextID)
	}

	c.addIn("status", f.Status)

	if f.Cursor != "" {
		c.add("id < ?", f.Cursor)
	}

	limit := f.normalize()

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+projectColumns+` FROM projects`+c.clause()+` ORDER BY id DESC LIMIT ?`,
		append(c.args, limit+1)...,
	)
	if err != nil {
		return nil, "", fmt.Errorf("store: list projects: %w", err)
	}
	defer rows.Close()

	var out []model.Project

	for rows.Next() {
		v, err := scanProject(rows)
		if err != nil {
			return nil, "", err
		}

		out = append(out, v)
	}

	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("store: list projects: %w", err)
	}

	return page(out, limit, func(v model.Project) string { return v.ID })
}

// GetProject returns one project owned by userID.
func (s *Store) GetProject(ctx context.Context, userID, projectID string) (model.Project, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+projectColumns+` FROM projects WHERE id = ? AND user_id = ? AND deleted_at IS NULL`,
		projectID, userID,
	)

	return scanProject(row)
}

// CreateProject inserts a project after checking the context belongs to userID.
func (s *Store) CreateProject(ctx context.Context, userID string, in ProjectCreate) (model.Project, error) {
	newID := id.New()

	status := in.Status
	if status == "" {
		status = "active"
	}

	err := s.tx(ctx, func(tx *sql.Tx) error {
		if err := assertOwned(ctx, tx, "context_id", in.ContextID, userID); err != nil {
			return err
		}

		_, err := tx.ExecContext(ctx,
			`INSERT INTO projects (id, user_id, context_id, name, description, status)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			newID, userID, in.ContextID, in.Name, in.Description, status,
		)
		if err != nil {
			return fmt.Errorf("store: insert project: %w", err)
		}

		return nil
	})
	if err != nil {
		return model.Project{}, err
	}

	return s.GetProject(ctx, userID, newID)
}

// UpdateProject applies a partial update.
//
// Moving a project between contexts drags its tasks along, because a task whose
// project sits in another context would be incoherent.
func (s *Store) UpdateProject(ctx context.Context, userID, projectID string, in ProjectUpdate) (model.Project, error) {
	err := s.tx(ctx, func(tx *sql.Tx) error {
		if err := assertRowOwned(ctx, tx, "projects", "id", projectID, userID); err != nil {
			return ErrNotFound
		}

		b := &updateBuilder{}

		applyField(b, "name", in.Name)
		applyField(b, "description", in.Description)
		applyField(b, "status", in.Status)

		if in.ContextID.Present() {
			if err := assertOwned(ctx, tx, "context_id", in.ContextID.Value, userID); err != nil {
				return err
			}

			b.set("context_id", in.ContextID.Value)
		}

		if b.empty() {
			return nil
		}

		_, err := tx.ExecContext(ctx,
			`UPDATE projects SET `+b.clause()+` WHERE id = ? AND user_id = ?`,
			append(b.args, projectID, userID)...,
		)
		if err != nil {
			return fmt.Errorf("store: update project: %w", err)
		}

		if in.ContextID.Present() {
			_, err := tx.ExecContext(ctx,
				`UPDATE tasks SET context_id = ?, updated_at = `+nowExpr+`
				 WHERE user_id = ? AND project_id = ? AND deleted_at IS NULL`,
				in.ContextID.Value, userID, projectID,
			)
			if err != nil {
				return fmt.Errorf("store: move project tasks: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		return model.Project{}, err
	}

	return s.GetProject(ctx, userID, projectID)
}

// DeleteProject tombstones a project and detaches its tasks, which stay in
// their context but lose the grouping.
func (s *Store) DeleteProject(ctx context.Context, userID, projectID string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if err := assertRowOwned(ctx, tx, "projects", "id", projectID, userID); err != nil {
			return ErrNotFound
		}

		stmts := []string{
			`UPDATE tasks SET project_id = NULL, updated_at = ` + nowExpr + `
			 WHERE user_id = ? AND project_id = ? AND deleted_at IS NULL`,
			`UPDATE recurrences SET project_id = NULL, updated_at = ` + nowExpr + `
			 WHERE user_id = ? AND project_id = ? AND deleted_at IS NULL`,
			`UPDATE projects SET deleted_at = ` + nowExpr + `, updated_at = ` + nowExpr + `
			 WHERE user_id = ? AND id = ? AND deleted_at IS NULL`,
		}

		for _, q := range stmts {
			if _, err := tx.ExecContext(ctx, q, userID, projectID); err != nil {
				return fmt.Errorf("store: delete project: %w", err)
			}
		}

		return nil
	})
}

func scanProject(sc scanner) (model.Project, error) {
	var v model.Project

	err := sc.Scan(&v.ID, &v.ContextID, &v.Name, &v.Description, &v.Status,
		&v.CreatedAt, &v.UpdatedAt, &v.DeletedAt, &v.Rev)
	if err != nil {
		return model.Project{}, notFoundOr(err, "scan project")
	}

	return v, nil
}
