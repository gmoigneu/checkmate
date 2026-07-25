package store

import (
	"context"
	"fmt"

	"github.com/nls/checkmate/server/internal/model"
)

// SyncChanges holds the rows that changed in one delta, tombstones included.
type SyncChanges struct {
	Contexts    []model.Context    `json:"contexts"`
	Projects    []model.Project    `json:"projects"`
	People      []model.Person     `json:"people"`
	Recurrences []model.Recurrence `json:"recurrences"`
	Tasks       []model.Task       `json:"tasks"`
}

// SyncResult is one page of a delta.
type SyncResult struct {
	// Cursor is the rev to pass as `since` next time.
	Cursor int64 `json:"cursor"`

	// HasMore reports that more changes are already waiting, so a client should
	// come straight back rather than waiting for its next poll.
	HasMore bool `json:"has_more"`

	Changes SyncChanges `json:"changes"`

	// Sources is the static lookup table, sent only on a full sync since it has
	// no rev and never changes in normal operation.
	Sources []model.Source `json:"sources,omitempty"`
}

// SyncMaxLimit and SyncDefaultLimit bound how many rows one delta returns per
// table.
const (
	SyncDefaultLimit = 500
	SyncMaxLimit     = 2000
)

// Sync returns every row of the caller's data with rev greater than since.
//
// Tombstones are included: a deleted row is a change like any other, and a client
// that only saw live rows could never learn about a deletion.
//
// The pagination is the interesting part. rev comes from one global counter, so
// it is unique across every table and orders all changes into a single sequence.
// Each table is queried for limit+1 rows to detect truncation, and when any table
// is truncated the returned cursor is pulled back to the lowest safe point across
// all of them, with every table filtered to that cursor. Without that, a table
// with many changes would have its tail skipped: the client would advance past
// rows it never received and never ask for them again.
func (s *Store) Sync(ctx context.Context, userID string, since int64, limit int) (SyncResult, error) {
	switch {
	case limit <= 0:
		limit = SyncDefaultLimit
	case limit > SyncMaxLimit:
		limit = SyncMaxLimit
	}

	var (
		result    SyncResult
		truncated bool

		// safeCursor is the highest rev that can be reported while guaranteeing
		// nothing below it was left behind.
		safeCursor  int64
		haveTruncat bool
		maxRev      = since
	)

	// note records what one table returned and folds it into the cursor maths.
	note := func(revs []int64) {
		if len(revs) > limit {
			truncated = true

			// The last row that will actually be returned from this table.
			boundary := revs[limit-1]

			if !haveTruncat || boundary < safeCursor {
				safeCursor = boundary
				haveTruncat = true
			}

			revs = revs[:limit]
		}

		if len(revs) > 0 && revs[len(revs)-1] > maxRev {
			maxRev = revs[len(revs)-1]
		}
	}

	contexts, contextRevs, err := s.syncContexts(ctx, userID, since, limit+1)
	if err != nil {
		return SyncResult{}, err
	}

	note(contextRevs)

	projects, projectRevs, err := s.syncProjects(ctx, userID, since, limit+1)
	if err != nil {
		return SyncResult{}, err
	}

	note(projectRevs)

	people, peopleRevs, err := s.syncPeople(ctx, userID, since, limit+1)
	if err != nil {
		return SyncResult{}, err
	}

	note(peopleRevs)

	recurrences, recurrenceRevs, err := s.syncRecurrences(ctx, userID, since, limit+1)
	if err != nil {
		return SyncResult{}, err
	}

	note(recurrenceRevs)

	tasks, taskRevs, err := s.syncTasks(ctx, userID, since, limit+1)
	if err != nil {
		return SyncResult{}, err
	}

	note(taskRevs)

	cursor := maxRev
	if haveTruncat {
		cursor = safeCursor
	}

	result.Cursor = cursor
	result.HasMore = truncated

	// Every collection is normalized to an empty slice rather than left nil: a
	// nil slice marshals to JSON null, and a client iterating what it expects to
	// be an array is a needless crash.
	result.Changes = SyncChanges{
		Contexts:    orEmpty(filterByRev(contexts, contextRevs, cursor)),
		Projects:    orEmpty(filterByRev(projects, projectRevs, cursor)),
		People:      orEmpty(filterByRev(people, peopleRevs, cursor)),
		Recurrences: orEmpty(filterByRev(recurrences, recurrenceRevs, cursor)),
		Tasks:       orEmpty(filterByRev(tasks, taskRevs, cursor)),
	}

	// A client starting from scratch needs the source lookup to render anything.
	if since == 0 {
		sources, err := s.ListSources(ctx)
		if err != nil {
			return SyncResult{}, err
		}

		result.Sources = sources
	}

	return result, nil
}

// orEmpty replaces a nil slice with an empty one so it marshals to [] not null.
func orEmpty[T any](rows []T) []T {
	if rows == nil {
		return []T{}
	}

	return rows
}

// filterByRev trims a slice to the rows at or below cursor. The revs slice is
// parallel to rows and already ordered ascending.
func filterByRev[T any](rows []T, revs []int64, cursor int64) []T {
	keep := 0

	for i := range rows {
		if i >= len(revs) || revs[i] > cursor {
			break
		}

		keep++
	}

	return rows[:keep]
}

// The sync queries deliberately omit "deleted_at IS NULL": tombstones are the
// point. They order by rev, which is unique and monotonic, so a page boundary
// can never split two rows that share a position.

func (s *Store) syncContexts(ctx context.Context, userID string, since int64, limit int) ([]model.Context, []int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+contextColumns+` FROM contexts
		 WHERE user_id = ? AND rev > ? ORDER BY rev LIMIT ?`,
		userID, since, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("store: sync contexts: %w", err)
	}
	defer rows.Close()

	var (
		out  []model.Context
		revs []int64
	)

	for rows.Next() {
		v, err := scanContext(rows)
		if err != nil {
			return nil, nil, err
		}

		out = append(out, v)
		revs = append(revs, v.Rev)
	}

	return out, revs, rows.Err()
}

func (s *Store) syncProjects(ctx context.Context, userID string, since int64, limit int) ([]model.Project, []int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+projectColumns+` FROM projects
		 WHERE user_id = ? AND rev > ? ORDER BY rev LIMIT ?`,
		userID, since, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("store: sync projects: %w", err)
	}
	defer rows.Close()

	var (
		out  []model.Project
		revs []int64
	)

	for rows.Next() {
		v, err := scanProject(rows)
		if err != nil {
			return nil, nil, err
		}

		out = append(out, v)
		revs = append(revs, v.Rev)
	}

	return out, revs, rows.Err()
}

func (s *Store) syncPeople(ctx context.Context, userID string, since int64, limit int) ([]model.Person, []int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+personColumns+` FROM people
		 WHERE user_id = ? AND rev > ? ORDER BY rev LIMIT ?`,
		userID, since, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("store: sync people: %w", err)
	}
	defer rows.Close()

	var (
		out  []model.Person
		revs []int64
	)

	for rows.Next() {
		v, err := scanPerson(rows)
		if err != nil {
			return nil, nil, err
		}

		out = append(out, v)
		revs = append(revs, v.Rev)
	}

	return out, revs, rows.Err()
}

func (s *Store) syncRecurrences(ctx context.Context, userID string, since int64, limit int) ([]model.Recurrence, []int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+recurrenceColumns+` FROM recurrences
		 WHERE user_id = ? AND rev > ? ORDER BY rev LIMIT ?`,
		userID, since, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("store: sync recurrences: %w", err)
	}
	defer rows.Close()

	var (
		out  []model.Recurrence
		revs []int64
	)

	for rows.Next() {
		v, err := scanRecurrence(rows)
		if err != nil {
			return nil, nil, err
		}

		out = append(out, v)
		revs = append(revs, v.Rev)
	}

	return out, revs, rows.Err()
}

func (s *Store) syncTasks(ctx context.Context, userID string, since int64, limit int) ([]model.Task, []int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+taskColumns+` FROM tasks_with_kind
		 WHERE user_id = ? AND rev > ? ORDER BY rev LIMIT ?`,
		userID, since, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("store: sync tasks: %w", err)
	}
	defer rows.Close()

	var (
		out  []model.Task
		revs []int64
	)

	for rows.Next() {
		v, err := scanTask(rows)
		if err != nil {
			return nil, nil, err
		}

		out = append(out, v)
		revs = append(revs, v.Rev)
	}

	return out, revs, rows.Err()
}

// CurrentRev returns the global change counter, which is the cursor a client
// would get from a sync that returned everything.
func (s *Store) CurrentRev(ctx context.Context) (int64, error) {
	var rev int64

	if err := s.db.QueryRowContext(ctx, `SELECT value FROM change_seq`).Scan(&rev); err != nil {
		return 0, fmt.Errorf("store: read change_seq: %w", err)
	}

	return rev, nil
}
