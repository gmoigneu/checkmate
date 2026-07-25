package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/nls/checkmate/server/internal/model"
)

// briefBucketLimit caps each list in a brief. A brief is meant to be read, so an
// unbounded overdue list would be noise rather than information; the counts stay
// accurate even when a list is capped.
const briefBucketLimit = 100

// WaitingOn groups delegated tasks by the person they are waiting on, which is
// how a follow-up actually gets made: per person, not per task.
type WaitingOn struct {
	PersonID   string       `json:"person_id"`
	PersonName string       `json:"person_name"`
	Tasks      []model.Task `json:"tasks"`
}

// BriefTotals are the counts behind the lists, unaffected by list capping.
type BriefTotals struct {
	Overdue        int `json:"overdue"`
	DueToday       int `json:"due_today"`
	Planned        int `json:"planned"`
	Inbox          int `json:"inbox"`
	Blocked        int `json:"blocked"`
	WaitingOn      int `json:"waiting_on"`
	InProgress     int `json:"in_progress"`
	CompletedToday int `json:"completed_today"`

	// PlannedMinutes is the summed estimate of what is planned for the day, so a
	// day that has been over-committed is visible before it starts.
	PlannedMinutes int `json:"planned_minutes"`

	// PlannedWithoutEstimate is how much of the plan has no estimate, without
	// which PlannedMinutes reads as more complete than it is.
	PlannedWithoutEstimate int `json:"planned_without_estimate"`
}

// Brief is the daily brief.
type Brief struct {
	Date     string `json:"date"`
	Timezone string `json:"timezone"`

	Overdue    []model.Task `json:"overdue"`
	DueToday   []model.Task `json:"due_today"`
	Planned    []model.Task `json:"planned"`
	InProgress []model.Task `json:"in_progress"`
	Inbox      []model.Task `json:"inbox"`
	Blocked    []model.Task `json:"blocked"`
	WaitingOn  []WaitingOn  `json:"waiting_on"`

	// CompletedToday gives the day a sense of progress rather than only debt.
	CompletedToday []model.Task `json:"completed_today"`

	Totals BriefTotals `json:"totals"`
}

// BriefFilter narrows a brief to one context.
type BriefFilter struct {
	// Date is the day the brief is for, as YYYY-MM-DD in the caller's timezone.
	Date string

	// Timezone is recorded on the response so a client can tell which day
	// boundary was used.
	Timezone string

	// ContextID limits the brief to one context, for a work-only morning read.
	ContextID string
}

// openStatuses are the statuses a task can be in while still needing action.
// done and cancelled are excluded; inbox is excluded from date-driven buckets
// because an untriaged task has no meaningful plan yet.
var openStatuses = []string{
	model.StatusTodo, model.StatusInProgress, model.StatusBlocked, model.StatusDelegated,
}

// Brief assembles the daily brief for one day.
//
// Every bucket is a separate query rather than one pass bucketed in Go, because
// the buckets overlap: a task can be overdue, planned for today and blocked all
// at once, and each list wants it.
func (s *Store) Brief(ctx context.Context, userID string, f BriefFilter) (Brief, error) {
	brief := Brief{Date: f.Date, Timezone: f.Timezone}

	contextClause, contextArgs := briefContextClause(f.ContextID)

	openIn, openArgs := inClause(openStatuses)

	// Overdue: due before today and still open. Ordered oldest first, because the
	// thing that has been late longest is usually the thing to deal with.
	overdue, err := s.briefTasks(ctx,
		`user_id = ? AND deleted_at IS NULL AND due_on IS NOT NULL AND due_on < ?
		   AND status IN `+openIn+contextClause,
		append(append([]any{userID, f.Date}, openArgs...), contextArgs...),
		"due_on ASC",
	)
	if err != nil {
		return Brief{}, err
	}

	dueToday, err := s.briefTasks(ctx,
		`user_id = ? AND deleted_at IS NULL AND due_on = ? AND status IN `+openIn+contextClause,
		append(append([]any{userID, f.Date}, openArgs...), contextArgs...),
		"coalesce(estimate_minutes, 0) ASC",
	)
	if err != nil {
		return Brief{}, err
	}

	planned, err := s.briefTasks(ctx,
		`user_id = ? AND deleted_at IS NULL AND planned_on = ? AND status IN `+openIn+contextClause,
		append(append([]any{userID, f.Date}, openArgs...), contextArgs...),
		"coalesce(due_on, '9999-12-31') ASC",
	)
	if err != nil {
		return Brief{}, err
	}

	inProgress, err := s.briefTasks(ctx,
		`user_id = ? AND deleted_at IS NULL AND status = 'in_progress'`+contextClause,
		append([]any{userID}, contextArgs...),
		"updated_at DESC",
	)
	if err != nil {
		return Brief{}, err
	}

	// The inbox is context-independent by definition: an untriaged task has no
	// context yet, so a context filter would always empty it.
	inbox, err := s.briefTasks(ctx,
		`user_id = ? AND deleted_at IS NULL AND status = 'inbox'`,
		[]any{userID},
		"created_at ASC",
	)
	if err != nil {
		return Brief{}, err
	}

	blocked, err := s.briefTasks(ctx,
		`user_id = ? AND deleted_at IS NULL AND status = 'blocked'`+contextClause,
		append([]any{userID}, contextArgs...),
		"updated_at ASC",
	)
	if err != nil {
		return Brief{}, err
	}

	delegated, err := s.briefTasks(ctx,
		`user_id = ? AND deleted_at IS NULL AND status = 'delegated'
		   AND delegated_to_id IS NOT NULL`+contextClause,
		append([]any{userID}, contextArgs...),
		"due_on ASC",
	)
	if err != nil {
		return Brief{}, err
	}

	// completed_at is a timestamp, so "today" is a prefix match on the date part.
	completed, err := s.briefTasks(ctx,
		`user_id = ? AND deleted_at IS NULL AND status = 'done'
		   AND completed_at LIKE ? || '%'`+contextClause,
		append([]any{userID, f.Date}, contextArgs...),
		"completed_at DESC",
	)
	if err != nil {
		return Brief{}, err
	}

	waiting, err := s.groupWaitingOn(ctx, userID, delegated)
	if err != nil {
		return Brief{}, err
	}

	brief.Totals = BriefTotals{
		Overdue:        len(overdue),
		DueToday:       len(dueToday),
		Planned:        len(planned),
		Inbox:          len(inbox),
		Blocked:        len(blocked),
		WaitingOn:      len(delegated),
		InProgress:     len(inProgress),
		CompletedToday: len(completed),
	}

	for _, task := range planned {
		if task.EstimateMinutes != nil {
			brief.Totals.PlannedMinutes += int(*task.EstimateMinutes)

			continue
		}

		brief.Totals.PlannedWithoutEstimate++
	}

	brief.Overdue = capBucket(overdue)
	brief.DueToday = capBucket(dueToday)
	brief.Planned = capBucket(planned)
	brief.InProgress = capBucket(inProgress)
	brief.Inbox = capBucket(inbox)
	brief.Blocked = capBucket(blocked)
	brief.CompletedToday = capBucket(completed)
	brief.WaitingOn = waiting

	return brief, nil
}

// cap trims a bucket to the display limit. Totals are computed before this, so a
// capped list still reports an honest count.
func capBucket[T any](rows []T) []T {
	if rows == nil {
		return []T{}
	}

	if len(rows) > briefBucketLimit {
		return rows[:briefBucketLimit]
	}

	return rows
}

// groupWaitingOn buckets delegated tasks by delegate, resolving names in one
// query rather than one per task.
func (s *Store) groupWaitingOn(ctx context.Context, userID string, delegated []model.Task) ([]WaitingOn, error) {
	if len(delegated) == 0 {
		return []WaitingOn{}, nil
	}

	names, err := s.peopleNames(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Preserve first-seen order so the output is stable between calls.
	var (
		order   []string
		grouped = map[string][]model.Task{}
	)

	for _, task := range delegated {
		personID := *task.DelegatedToID

		if _, seen := grouped[personID]; !seen {
			order = append(order, personID)
		}

		grouped[personID] = append(grouped[personID], task)
	}

	out := make([]WaitingOn, 0, len(order))

	for _, personID := range order {
		name := names[personID]
		if name == "" {
			name = "(unknown)"
		}

		out = append(out, WaitingOn{
			PersonID:   personID,
			PersonName: name,
			Tasks:      capBucket(grouped[personID]),
		})
	}

	return out, nil
}

func (s *Store) peopleNames(ctx context.Context, userID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name FROM people WHERE user_id = ?`, userID)
	if err != nil {
		return nil, fmt.Errorf("store: read people names: %w", err)
	}
	defer rows.Close()

	names := map[string]string{}

	for rows.Next() {
		var personID, name string

		if err := rows.Scan(&personID, &name); err != nil {
			return nil, fmt.Errorf("store: scan person name: %w", err)
		}

		names[personID] = name
	}

	return names, rows.Err()
}

// briefTasks runs one bucket query.
func (s *Store) briefTasks(ctx context.Context, where string, args []any, order string) ([]model.Task, error) {
	// where and order are assembled from package literals; only args carry input.
	query := `SELECT ` + taskColumns + ` FROM tasks_with_kind WHERE ` + where +
		` ORDER BY ` + order + `, id DESC LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, append(args, briefBucketLimit*4)...)
	if err != nil {
		return nil, fmt.Errorf("store: brief bucket: %w", err)
	}
	defer rows.Close()

	var out []model.Task

	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, task)
	}

	return out, rows.Err()
}

func briefContextClause(contextID string) (string, []any) {
	if contextID == "" {
		return "", nil
	}

	return " AND context_id = ?", []any{contextID}
}

// inClause renders a fixed set of values as an IN list.
func inClause(values []string) (string, []any) {
	placeholders := make([]string, len(values))
	args := make([]any, len(values))

	for i, v := range values {
		placeholders[i] = "?"
		args[i] = v
	}

	return "(" + strings.Join(placeholders, ", ") + ")", args
}
