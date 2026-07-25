package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/nls/checkmate/server/internal/id"
	"github.com/nls/checkmate/server/internal/model"
	"github.com/nls/checkmate/server/internal/patch"
)

const personColumns = `id, name, email, context_id, notes,
	created_at, updated_at, deleted_at, rev`

// PersonCreate is the input for creating a person.
type PersonCreate struct {
	Name      string
	Email     *string
	ContextID *string
	Notes     *string
}

// PersonUpdate is the set of person fields a PATCH may change.
type PersonUpdate struct {
	Name      patch.Field[string]
	Email     patch.Field[string]
	ContextID patch.Field[string]
	Notes     patch.Field[string]
}

// PersonFilter narrows a people listing.
type PersonFilter struct {
	listOptions

	ContextID string
	Search    string
}

// ListPeople returns the caller's people, newest first.
func (s *Store) ListPeople(ctx context.Context, userID string, f PersonFilter) ([]model.Person, string, error) {
	c := &conditions{}
	c.add("user_id = ?", userID)

	if !f.IncludeDeleted {
		c.add("deleted_at IS NULL")
	}

	if f.ContextID != "" {
		c.add("context_id = ?", f.ContextID)
	}

	if f.Search != "" {
		c.add(`(name LIKE ? ESCAPE '\' OR coalesce(email, '') LIKE ? ESCAPE '\')`,
			likeArg(f.Search), likeArg(f.Search))
	}

	if f.Cursor != "" {
		c.add("id < ?", f.Cursor)
	}

	limit := f.normalize()

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+personColumns+` FROM people`+c.clause()+` ORDER BY id DESC LIMIT ?`,
		append(c.args, limit+1)...,
	)
	if err != nil {
		return nil, "", fmt.Errorf("store: list people: %w", err)
	}
	defer rows.Close()

	var out []model.Person

	for rows.Next() {
		v, err := scanPerson(rows)
		if err != nil {
			return nil, "", err
		}

		out = append(out, v)
	}

	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("store: list people: %w", err)
	}

	return page(out, limit, func(v model.Person) string { return v.ID })
}

// GetPerson returns one person owned by userID.
func (s *Store) GetPerson(ctx context.Context, userID, personID string) (model.Person, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+personColumns+` FROM people WHERE id = ? AND user_id = ? AND deleted_at IS NULL`,
		personID, userID,
	)

	return scanPerson(row)
}

// CreatePerson inserts a person.
func (s *Store) CreatePerson(ctx context.Context, userID string, in PersonCreate) (model.Person, error) {
	newID := id.New()

	err := s.tx(ctx, func(tx *sql.Tx) error {
		if in.ContextID != nil {
			if err := assertOwned(ctx, tx, "context_id", *in.ContextID, userID); err != nil {
				return err
			}
		}

		if err := assertPersonNameFree(ctx, tx, userID, in.Name, ""); err != nil {
			return err
		}

		_, err := tx.ExecContext(ctx,
			`INSERT INTO people (id, user_id, name, email, context_id, notes)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			newID, userID, in.Name, in.Email, in.ContextID, in.Notes,
		)
		if err != nil {
			return fmt.Errorf("store: insert person: %w", err)
		}

		return nil
	})
	if err != nil {
		return model.Person{}, err
	}

	return s.GetPerson(ctx, userID, newID)
}

// FindOrCreatePerson resolves a person by name, creating them if new. Quick
// capture uses this so delegating to someone does not require a second call.
func (s *Store) FindOrCreatePerson(ctx context.Context, userID, name string) (model.Person, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return model.Person{}, &ConflictError{Field: "name", Detail: "is required"}
	}

	var personID string

	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM people WHERE user_id = ? AND name = ? COLLATE NOCASE AND deleted_at IS NULL`,
		userID, name,
	).Scan(&personID)

	switch {
	case err == nil:
		return s.GetPerson(ctx, userID, personID)
	case !errors.Is(err, sql.ErrNoRows):
		return model.Person{}, fmt.Errorf("store: find person: %w", err)
	}

	return s.CreatePerson(ctx, userID, PersonCreate{Name: name})
}

// UpdatePerson applies a partial update.
func (s *Store) UpdatePerson(ctx context.Context, userID, personID string, in PersonUpdate) (model.Person, error) {
	err := s.tx(ctx, func(tx *sql.Tx) error {
		if err := assertRowOwned(ctx, tx, "people", "id", personID, userID); err != nil {
			return ErrNotFound
		}

		b := &updateBuilder{}

		applyField(b, "email", in.Email)
		applyField(b, "notes", in.Notes)

		if in.Name.Present() {
			if err := assertPersonNameFree(ctx, tx, userID, in.Name.Value, personID); err != nil {
				return err
			}

			b.set("name", in.Name.Value)
		}

		if in.ContextID.Set {
			if in.ContextID.Null {
				b.set("context_id", nil)
			} else {
				if err := assertOwned(ctx, tx, "context_id", in.ContextID.Value, userID); err != nil {
					return err
				}

				b.set("context_id", in.ContextID.Value)
			}
		}

		if b.empty() {
			return nil
		}

		_, err := tx.ExecContext(ctx,
			`UPDATE people SET `+b.clause()+` WHERE id = ? AND user_id = ?`,
			append(b.args, personID, userID)...,
		)
		if err != nil {
			return fmt.Errorf("store: update person: %w", err)
		}

		return nil
	})
	if err != nil {
		return model.Person{}, err
	}

	return s.GetPerson(ctx, userID, personID)
}

// DeletePerson tombstones a person.
//
// Tasks delegated to them are un-delegated and returned to todo. The schema
// forbids a delegated task without a delegate, so clearing delegated_to_id
// without also moving the status would violate that CHECK.
func (s *Store) DeletePerson(ctx context.Context, userID, personID string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if err := assertRowOwned(ctx, tx, "people", "id", personID, userID); err != nil {
			return ErrNotFound
		}

		_, err := tx.ExecContext(ctx,
			`UPDATE tasks
			 SET delegated_to_id = NULL,
			     status = CASE WHEN status = 'delegated' THEN 'todo' ELSE status END,
			     updated_at = `+nowExpr+`
			 WHERE user_id = ? AND delegated_to_id = ? AND deleted_at IS NULL`,
			userID, personID,
		)
		if err != nil {
			return fmt.Errorf("store: undelegate tasks: %w", err)
		}

		_, err = tx.ExecContext(ctx,
			`UPDATE recurrences SET delegated_to_id = NULL, updated_at = `+nowExpr+`
			 WHERE user_id = ? AND delegated_to_id = ? AND deleted_at IS NULL`,
			userID, personID,
		)
		if err != nil {
			return fmt.Errorf("store: undelegate recurrences: %w", err)
		}

		_, err = tx.ExecContext(ctx,
			`UPDATE people SET deleted_at = `+nowExpr+`, updated_at = `+nowExpr+`
			 WHERE user_id = ? AND id = ? AND deleted_at IS NULL`,
			userID, personID,
		)
		if err != nil {
			return fmt.Errorf("store: delete person: %w", err)
		}

		return nil
	})
}

// assertPersonNameFree enforces the unique (user_id, name) index with a clear
// error instead of a raw constraint violation.
func assertPersonNameFree(ctx context.Context, q querier, userID, name, exceptID string) error {
	var found int

	err := q.QueryRowContext(ctx,
		`SELECT 1 FROM people
		 WHERE user_id = ? AND name = ? COLLATE NOCASE AND deleted_at IS NULL AND id <> ?`,
		userID, name, exceptID,
	).Scan(&found)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("store: check person name: %w", err)
	}

	return &ConflictError{Field: "name", Detail: fmt.Sprintf("%q already exists", name)}
}

// likeArg wraps a search term for a LIKE comparison, escaping the wildcards a
// caller might type so they match literally. Every LIKE using this must carry
// ESCAPE '\', otherwise sqlite treats the backslashes as ordinary characters.
func likeArg(term string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

	return "%" + replacer.Replace(term) + "%"
}

func scanPerson(sc scanner) (model.Person, error) {
	var v model.Person

	err := sc.Scan(&v.ID, &v.Name, &v.Email, &v.ContextID, &v.Notes,
		&v.CreatedAt, &v.UpdatedAt, &v.DeletedAt, &v.Rev)
	if err != nil {
		return model.Person{}, notFoundOr(err, "scan person")
	}

	return v, nil
}
