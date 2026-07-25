package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/nls/checkmate/server/internal/id"
	"github.com/nls/checkmate/server/internal/model"
	"github.com/nls/checkmate/server/internal/patch"
)

const contextColumns = `id, name, slug, color, sort_order, archived_at,
	created_at, updated_at, deleted_at, rev`

// ContextCreate is the input for creating a context.
type ContextCreate struct {
	Name      string
	Slug      string
	Color     *string
	SortOrder *int64
}

// ContextUpdate is the set of context fields a PATCH may change.
type ContextUpdate struct {
	Name      patch.Field[string]
	Slug      patch.Field[string]
	Color     patch.Field[string]
	SortOrder patch.Field[int64]
	Archived  patch.Field[bool]
}

// ContextFilter narrows a context listing.
type ContextFilter struct {
	listOptions

	// IncludeArchived adds archived contexts to the result.
	IncludeArchived bool
}

// ListContexts returns the caller's contexts, newest first.
func (s *Store) ListContexts(ctx context.Context, userID string, f ContextFilter) ([]model.Context, string, error) {
	c := &conditions{}
	c.add("user_id = ?", userID)

	if !f.IncludeDeleted {
		c.add("deleted_at IS NULL")
	}

	if !f.IncludeArchived {
		c.add("archived_at IS NULL")
	}

	if f.Cursor != "" {
		c.add("id < ?", f.Cursor)
	}

	limit := f.normalize()

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+contextColumns+` FROM contexts`+c.clause()+` ORDER BY id DESC LIMIT ?`,
		append(c.args, limit+1)...,
	)
	if err != nil {
		return nil, "", fmt.Errorf("store: list contexts: %w", err)
	}
	defer rows.Close()

	var out []model.Context

	for rows.Next() {
		v, err := scanContext(rows)
		if err != nil {
			return nil, "", err
		}

		out = append(out, v)
	}

	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("store: list contexts: %w", err)
	}

	return page(out, limit, func(v model.Context) string { return v.ID })
}

// GetContext returns one context owned by userID.
func (s *Store) GetContext(ctx context.Context, userID, contextID string) (model.Context, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+contextColumns+` FROM contexts WHERE id = ? AND user_id = ? AND deleted_at IS NULL`,
		contextID, userID,
	)

	v, err := scanContext(row)
	if err != nil {
		return model.Context{}, err
	}

	return v, nil
}

// CreateContext inserts a context. An empty slug is derived from the name.
func (s *Store) CreateContext(ctx context.Context, userID string, in ContextCreate) (model.Context, error) {
	slug := in.Slug
	if slug == "" {
		slug = Slugify(in.Name)
	}

	if slug == "" {
		return model.Context{}, &ConflictError{Field: "slug", Detail: "could not be derived from the name"}
	}

	newID := id.New()

	var sortOrder int64
	if in.SortOrder != nil {
		sortOrder = *in.SortOrder
	}

	err := s.tx(ctx, func(tx *sql.Tx) error {
		if err := assertSlugFree(ctx, tx, userID, slug, ""); err != nil {
			return err
		}

		if in.SortOrder == nil {
			// Append to the end of the caller's existing contexts.
			err := tx.QueryRowContext(ctx,
				`SELECT coalesce(max(sort_order), 0) + 10 FROM contexts WHERE user_id = ? AND deleted_at IS NULL`,
				userID,
			).Scan(&sortOrder)
			if err != nil {
				return fmt.Errorf("store: next sort_order: %w", err)
			}
		}

		_, err := tx.ExecContext(ctx,
			`INSERT INTO contexts (id, user_id, name, slug, color, sort_order)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			newID, userID, in.Name, slug, in.Color, sortOrder,
		)
		if err != nil {
			return fmt.Errorf("store: insert context: %w", err)
		}

		return nil
	})
	if err != nil {
		return model.Context{}, err
	}

	return s.GetContext(ctx, userID, newID)
}

// UpdateContext applies a partial update.
func (s *Store) UpdateContext(ctx context.Context, userID, contextID string, in ContextUpdate) (model.Context, error) {
	err := s.tx(ctx, func(tx *sql.Tx) error {
		if err := assertRowOwned(ctx, tx, "contexts", "id", contextID, userID); err != nil {
			return ErrNotFound
		}

		b := &updateBuilder{}

		applyField(b, "name", in.Name)
		applyField(b, "color", in.Color)
		applyField(b, "sort_order", in.SortOrder)

		if in.Slug.Present() {
			slug := Slugify(in.Slug.Value)
			if slug == "" {
				return &ConflictError{Field: "slug", Detail: "is not a usable slug"}
			}

			if err := assertSlugFree(ctx, tx, userID, slug, contextID); err != nil {
				return err
			}

			b.set("slug", slug)
		}

		if in.Archived.Present() {
			if in.Archived.Value {
				b.setExpr("archived_at", nowExpr)
			} else {
				b.set("archived_at", nil)
			}
		}

		if b.empty() {
			return nil
		}

		_, err := tx.ExecContext(ctx,
			`UPDATE contexts SET `+b.clause()+` WHERE id = ? AND user_id = ?`,
			append(b.args, contextID, userID)...,
		)
		if err != nil {
			return fmt.Errorf("store: update context: %w", err)
		}

		return nil
	})
	if err != nil {
		return model.Context{}, err
	}

	return s.GetContext(ctx, userID, contextID)
}

// DeleteContext tombstones a context.
//
// Its projects and recurrences are tombstoned with it, but its tasks are moved
// to the inbox (context_id and project_id cleared) rather than deleted: losing
// tasks because a bucket was tidied away would be the worse failure.
func (s *Store) DeleteContext(ctx context.Context, userID, contextID string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if err := assertRowOwned(ctx, tx, "contexts", "id", contextID, userID); err != nil {
			return ErrNotFound
		}

		stmts := []string{
			`UPDATE tasks SET context_id = NULL, project_id = NULL, updated_at = ` + nowExpr + `
			 WHERE user_id = ? AND context_id = ? AND deleted_at IS NULL`,
			`UPDATE projects SET deleted_at = ` + nowExpr + `, updated_at = ` + nowExpr + `
			 WHERE user_id = ? AND context_id = ? AND deleted_at IS NULL`,
			`UPDATE recurrences SET deleted_at = ` + nowExpr + `, updated_at = ` + nowExpr + `
			 WHERE user_id = ? AND context_id = ? AND deleted_at IS NULL`,
			`UPDATE people SET context_id = NULL, updated_at = ` + nowExpr + `
			 WHERE user_id = ? AND context_id = ? AND deleted_at IS NULL`,
			`UPDATE contexts SET deleted_at = ` + nowExpr + `, updated_at = ` + nowExpr + `
			 WHERE user_id = ? AND id = ? AND deleted_at IS NULL`,
		}

		for _, q := range stmts {
			if _, err := tx.ExecContext(ctx, q, userID, contextID); err != nil {
				return fmt.Errorf("store: delete context: %w", err)
			}
		}

		return nil
	})
}

// assertSlugFree checks the slug is unused, ignoring the row being updated.
func assertSlugFree(ctx context.Context, q querier, userID, slug, exceptID string) error {
	var found int

	err := q.QueryRowContext(ctx,
		`SELECT 1 FROM contexts
		 WHERE user_id = ? AND slug = ? AND deleted_at IS NULL AND id <> ?`,
		userID, slug, exceptID,
	).Scan(&found)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("store: check slug: %w", err)
	}

	return &ConflictError{Field: "slug", Detail: fmt.Sprintf("%q is already used by another context", slug)}
}

var (
	slugStrip    = regexp.MustCompile(`[^a-z0-9]+`)
	slugTrimDash = regexp.MustCompile(`^-+|-+$`)
)

// Slugify reduces a name to a url-safe slug.
func Slugify(s string) string {
	out := slugStrip.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-")

	return slugTrimDash.ReplaceAllString(out, "")
}

func scanContext(sc scanner) (model.Context, error) {
	var v model.Context

	err := sc.Scan(&v.ID, &v.Name, &v.Slug, &v.Color, &v.SortOrder, &v.ArchivedAt,
		&v.CreatedAt, &v.UpdatedAt, &v.DeletedAt, &v.Rev)
	if err != nil {
		return model.Context{}, notFoundOr(err, "scan context")
	}

	return v, nil
}

// page trims an over-fetched slice to limit and returns the next cursor.
func page[T any](rows []T, limit int, cursorOf func(T) string) ([]T, string, error) {
	if len(rows) <= limit {
		return rows, "", nil
	}

	trimmed := rows[:limit]

	return trimmed, cursorOf(trimmed[len(trimmed)-1]), nil
}
