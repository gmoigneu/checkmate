package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/nls/checkmate/server/internal/model"
)

// ActivityFilter controls the keyset-paginated task activity feed.
type ActivityFilter struct {
	Limit  int
	Cursor string
}

// ListTaskActivity returns newest mutations first.
func (s *Store) ListTaskActivity(
	ctx context.Context,
	userID string,
	f ActivityFilter,
) ([]model.TaskActivity, string, error) {
	cursor, err := decodeCursor(f.Cursor)
	if err != nil || (cursor.Sort != "" && cursor.Sort != "activity:desc") {
		return nil, "", &ConflictError{Field: "cursor", Detail: "is not valid for activity"}
	}

	c := &conditions{}
	c.add("user_id = ?", userID)

	if cursor.ID != "" {
		id, parseErr := strconv.ParseInt(cursor.ID, 10, 64)
		if parseErr != nil {
			return nil, "", &ConflictError{Field: "cursor", Detail: "is not valid for activity"}
		}
		c.add("id < ?", id)
	}

	limit := (listOptions{Limit: f.Limit}).normalize()
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_id, task_title, action, changed_fields,
		       status_before, status_after, occurred_at
		FROM task_activity`+c.clause()+`
		ORDER BY id DESC
		LIMIT ?`,
		append(c.args, limit+1)...,
	)
	if err != nil {
		return nil, "", fmt.Errorf("store: list task activity: %w", err)
	}
	defer rows.Close()

	out := []model.TaskActivity{}
	for rows.Next() {
		var (
			item   model.TaskActivity
			fields string
		)
		if err := rows.Scan(
			&item.ID, &item.TaskID, &item.TaskTitle, &item.Action, &fields,
			&item.StatusBefore, &item.StatusAfter, &item.OccurredAt,
		); err != nil {
			return nil, "", fmt.Errorf("store: scan task activity: %w", err)
		}

		if fields != "" {
			item.ChangedFields = strings.Split(fields, ",")
		} else {
			item.ChangedFields = []string{}
		}

		if item.StatusAfter != nil &&
			*item.StatusAfter == model.StatusCancelled &&
			containsTaskStatus(item.ChangedFields, "expired_at") {
			expired := model.StatusExpired
			item.StatusAfter = &expired
		}

		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("store: list task activity: %w", err)
	}

	if len(out) <= limit {
		return out, "", nil
	}

	out = out[:limit]
	next := encodeCursor(keysetCursor{
		ID:   strconv.FormatInt(out[len(out)-1].ID, 10),
		Sort: "activity:desc",
	})

	return out, next, nil
}
