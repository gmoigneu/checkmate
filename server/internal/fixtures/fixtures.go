// Package fixtures builds a representative local-development dataset.
package fixtures

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/nls/checkmate/server/internal/database"
	"github.com/nls/checkmate/server/internal/id"
	"github.com/nls/checkmate/server/internal/model"
)

// Options controls the relative dates used by Load.
type Options struct {
	Now      time.Time
	Timezone string
}

// Summary reports the rows created by Load.
type Summary struct {
	Contexts    int
	Projects    int
	People      int
	Recurrences int
	Tasks       int
	HistoryFrom string
}

type loader struct {
	ctx      context.Context
	tx       *sql.Tx
	userID   string
	now      time.Time
	today    time.Time
	start    time.Time
	location *time.Location
	summary  Summary
}

type contextSpec struct {
	ID         string
	Name       string
	Slug       string
	Color      string
	SortOrder  int
	ArchivedAt *string
}

type projectSpec struct {
	ID          string
	ContextID   string
	Name        string
	Description string
	Status      string
}

type personSpec struct {
	ID        string
	Name      string
	Email     string
	ContextID *string
	Notes     string
}

type recurrenceSpec struct {
	ID               string
	ContextID        string
	ProjectID        *string
	Source           string
	Title            string
	Details          string
	RRule            string
	EstimateMinutes  int
	DelegatedToID    *string
	LeadDays         int
	StartsOn         time.Time
	EndsOn           *time.Time
	NextOccurrenceOn *time.Time
	LastSpawnedOn    *time.Time
	Active           bool
	CompletedAt      *string
}

type taskSpec struct {
	ID              string
	ContextID       *string
	ProjectID       *string
	ParentID        *string
	RecurrenceID    *string
	OccurrenceOn    *time.Time
	Source          string
	CaptureMethod   string
	Title           string
	Details         string
	Status          string
	Priority        *string
	DueOn           *time.Time
	PlannedOn       *time.Time
	EstimateMinutes *int
	DelegatedToID   *string
	BlockedByID     *string
	ReferenceURL    string
	ReferenceLabel  string
	CompletedAt     *string
	CancelledAt     *string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DeletedAt       *string
}

// Load inserts a broad, internally coherent dataset for userID.
//
// The caller is expected to provide an otherwise empty fixture account. All
// inserts happen in one transaction, so a failed load leaves that account with
// no partial task data.
func Load(ctx context.Context, db *sql.DB, userID string, options Options) (Summary, error) {
	now := options.Now
	if now.IsZero() {
		now = time.Now()
	}

	timezone := options.Timezone
	if timezone == "" {
		timezone = "UTC"
	}

	location, err := time.LoadLocation(timezone)
	if err != nil {
		return Summary{}, fmt.Errorf("fixtures: load timezone %q: %w", timezone, err)
	}

	localNow := now.In(location)
	today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location)
	start := today.AddDate(0, -3, 0)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Summary{}, fmt.Errorf("fixtures: begin: %w", err)
	}
	defer tx.Rollback()

	l := &loader{
		ctx:      ctx,
		tx:       tx,
		userID:   userID,
		now:      now,
		today:    today,
		start:    start,
		location: location,
		summary:  Summary{HistoryFrom: start.Format(database.DateOnly)},
	}

	if err := l.load(); err != nil {
		return Summary{}, err
	}

	if err := tx.Commit(); err != nil {
		return Summary{}, fmt.Errorf("fixtures: commit: %w", err)
	}

	return l.summary, nil
}

func (l *loader) load() error {
	contexts, err := l.loadContexts()
	if err != nil {
		return err
	}

	projects, err := l.loadProjects(contexts)
	if err != nil {
		return err
	}

	people, err := l.loadPeople(contexts)
	if err != nil {
		return err
	}

	recurrences, err := l.loadRecurrences(contexts, projects, people)
	if err != nil {
		return err
	}

	if err := l.loadRecurrenceHistory(recurrences); err != nil {
		return err
	}

	if err := l.loadHistoricalTasks(contexts, projects, people); err != nil {
		return err
	}

	if err := l.loadCurrentTasks(contexts, projects, people); err != nil {
		return err
	}

	return l.loadTombstone(contexts)
}

func (l *loader) loadContexts() (map[string]string, error) {
	createdAt := l.timestamp(l.start.AddDate(0, 0, -14), 9)
	archivedAt := l.timestamp(l.today.AddDate(0, 0, -40), 17)
	specs := []contextSpec{
		{ID: id.New(), Name: "Work", Slug: "work", Color: "#5B8DEF", SortOrder: 10},
		{ID: id.New(), Name: "Personal", Slug: "personal", Color: "#E58A45", SortOrder: 20},
		{ID: id.New(), Name: "Health", Slug: "health", Color: "#55A868", SortOrder: 30},
		{ID: id.New(), Name: "Someday", Slug: "someday", Color: "#8C8C8C", SortOrder: 40, ArchivedAt: &archivedAt},
	}

	out := make(map[string]string, len(specs))
	for _, spec := range specs {
		_, err := l.tx.ExecContext(l.ctx, `
			INSERT INTO contexts
				(id, user_id, name, slug, color, sort_order, archived_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			spec.ID, l.userID, spec.Name, spec.Slug, spec.Color, spec.SortOrder,
			spec.ArchivedAt, createdAt, valueOr(spec.ArchivedAt, createdAt),
		)
		if err != nil {
			return nil, fmt.Errorf("fixtures: insert context %q: %w", spec.Name, err)
		}

		out[spec.Slug] = spec.ID
		l.summary.Contexts++
	}

	return out, nil
}

func (l *loader) loadProjects(contexts map[string]string) (map[string]string, error) {
	specs := []projectSpec{
		{ID: id.New(), ContextID: contexts["work"], Name: "Product launch", Description: "Ship the next Checkmate release.", Status: "active"},
		{ID: id.New(), ContextID: contexts["work"], Name: "Operations", Description: "Recurring team and company operations.", Status: "paused"},
		{ID: id.New(), ContextID: contexts["work"], Name: "Q2 planning", Description: "Completed quarterly planning cycle.", Status: "done"},
		{ID: id.New(), ContextID: contexts["personal"], Name: "Home office", Description: "Improve the desk and recording setup.", Status: "active"},
		{ID: id.New(), ContextID: contexts["personal"], Name: "Japan trip", Description: "Archived travel planning example.", Status: "archived"},
		{ID: id.New(), ContextID: contexts["health"], Name: "10K training", Description: "Three runs and one mobility session per week.", Status: "done"},
	}

	out := make(map[string]string, len(specs))
	for i, spec := range specs {
		createdAt := l.timestamp(l.start.AddDate(0, 0, i), 10)
		_, err := l.tx.ExecContext(l.ctx, `
			INSERT INTO projects
				(id, user_id, context_id, name, description, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			spec.ID, l.userID, spec.ContextID, spec.Name, spec.Description, spec.Status,
			createdAt, l.timestamp(l.today.AddDate(0, 0, -i), 16),
		)
		if err != nil {
			return nil, fmt.Errorf("fixtures: insert project %q: %w", spec.Name, err)
		}

		out[spec.Name] = spec.ID
		l.summary.Projects++
	}

	return out, nil
}

func (l *loader) loadPeople(contexts map[string]string) (map[string]string, error) {
	work := contexts["work"]
	personal := contexts["personal"]
	specs := []personSpec{
		{ID: id.New(), Name: "Maya Chen", Email: "maya@example.com", ContextID: &work, Notes: "Product lead; prefers concise written updates."},
		{ID: id.New(), Name: "Luca Martin", Email: "luca@example.com", ContextID: &work, Notes: "Engineering partner for launch work."},
		{ID: id.New(), Name: "Priya Shah", Email: "priya@example.com", ContextID: &work, Notes: "Finance contact for purchase approvals."},
		{ID: id.New(), Name: "Alex Morgan", Email: "alex@example.com", ContextID: &personal, Notes: "Contractor helping with the home office."},
		{ID: id.New(), Name: "Sam Rivera", Email: "sam@example.com", Notes: "Cross-context collaborator."},
	}

	out := make(map[string]string, len(specs))
	for i, spec := range specs {
		createdAt := l.timestamp(l.start.AddDate(0, 0, i+2), 11)
		_, err := l.tx.ExecContext(l.ctx, `
			INSERT INTO people
				(id, user_id, name, email, context_id, notes, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			spec.ID, l.userID, spec.Name, spec.Email, spec.ContextID, spec.Notes,
			createdAt, createdAt,
		)
		if err != nil {
			return nil, fmt.Errorf("fixtures: insert person %q: %w", spec.Name, err)
		}

		out[spec.Name] = spec.ID
		l.summary.People++
	}

	return out, nil
}

func (l *loader) loadRecurrences(
	contexts, projects, people map[string]string,
) (map[string]recurrenceSpec, error) {
	dailyStart := l.today.AddDate(0, 0, -7)
	nextDaily := l.today.AddDate(0, 0, 1)
	weeklyStart := onOrAfter(l.start, time.Friday)
	nextFriday := onOrAfter(l.today.AddDate(0, 0, 1), time.Friday)
	lastFriday := nextFriday.AddDate(0, 0, -7)
	monthlyStart := firstOfMonth(l.start)
	nextMonth := firstOfMonth(l.today.AddDate(0, 1, 0))
	lastMonth := firstOfMonth(l.today)
	finishedStart := onOrAfter(l.start, time.Wednesday)
	finishedEnd := finishedStart.AddDate(0, 0, 35)
	finishedAt := l.timestamp(finishedEnd, 18)

	specs := []recurrenceSpec{
		{
			ID: id.New(), ContextID: contexts["work"], ProjectID: ptr(projects["Operations"]),
			Source: "self", Title: "Daily planning pass", Details: "Choose the day's three most important outcomes.",
			RRule: "FREQ=DAILY", EstimateMinutes: 10, StartsOn: dailyStart,
			NextOccurrenceOn: &nextDaily, LastSpawnedOn: &l.today, Active: true,
		},
		{
			ID: id.New(), ContextID: contexts["work"], ProjectID: ptr(projects["Product launch"]),
			Source: "meeting", Title: "Send weekly launch update", Details: "Summarize progress, risks, and next decisions.",
			RRule: "FREQ=WEEKLY;BYDAY=FR", EstimateMinutes: 30, DelegatedToID: ptr(people["Maya Chen"]),
			StartsOn: weeklyStart, NextOccurrenceOn: &nextFriday, LastSpawnedOn: &lastFriday, Active: true,
		},
		{
			ID: id.New(), ContextID: contexts["personal"], ProjectID: ptr(projects["Home office"]),
			Source: "email", Title: "Reconcile household expenses", Details: "Paused monthly routine.",
			RRule: "FREQ=MONTHLY;BYMONTHDAY=1", EstimateMinutes: 25, StartsOn: monthlyStart,
			NextOccurrenceOn: &nextMonth, LastSpawnedOn: &lastMonth, Active: false,
		},
		{
			ID: id.New(), ContextID: contexts["health"], ProjectID: ptr(projects["10K training"]),
			Source: "self", Title: "Wednesday tempo run", Details: "Finished six-week training block.",
			RRule: "FREQ=WEEKLY;BYDAY=WE;COUNT=6", EstimateMinutes: 45, StartsOn: finishedStart,
			EndsOn: &finishedEnd, LastSpawnedOn: &finishedEnd, Active: false, CompletedAt: &finishedAt,
		},
	}

	out := make(map[string]recurrenceSpec, len(specs))
	for _, spec := range specs {
		active := 0
		if spec.Active {
			active = 1
		}

		createdAt := l.timestamp(spec.StartsOn.AddDate(0, 0, -1), 9)
		_, err := l.tx.ExecContext(l.ctx, `
			INSERT INTO recurrences (
				id, user_id, context_id, project_id, source_key, title, details, rrule,
				timezone, estimate_minutes, delegated_to_id, lead_days, starts_on, ends_on,
				next_occurrence_on, last_spawned_on, active, completed_at, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			spec.ID, l.userID, spec.ContextID, spec.ProjectID, spec.Source, spec.Title,
			spec.Details, spec.RRule, l.location.String(), spec.EstimateMinutes,
			spec.DelegatedToID, spec.LeadDays, dateValue(spec.StartsOn), dateValue(spec.EndsOn),
			dateValue(spec.NextOccurrenceOn), dateValue(spec.LastSpawnedOn), active,
			spec.CompletedAt, createdAt, valueOr(spec.CompletedAt, createdAt),
		)
		if err != nil {
			return nil, fmt.Errorf("fixtures: insert recurrence %q: %w", spec.Title, err)
		}

		out[spec.Title] = spec
		l.summary.Recurrences++
	}

	return out, nil
}

func (l *loader) loadRecurrenceHistory(recurrences map[string]recurrenceSpec) error {
	daily := recurrences["Daily planning pass"]
	for day := daily.StartsOn; !day.After(l.today); day = day.AddDate(0, 0, 1) {
		status := model.StatusDone
		var completedAt *string
		if day.Equal(l.today) {
			status = model.StatusTodo
		} else {
			completedAt = ptr(l.timestamp(day, 8))
		}

		if err := l.insertTask(taskSpec{
			ID: id.New(), ContextID: &daily.ContextID, ProjectID: daily.ProjectID,
			RecurrenceID: &daily.ID, OccurrenceOn: &day, Source: daily.Source,
			CaptureMethod: "recurrence", Title: daily.Title, Details: daily.Details,
			Status: status, Priority: ptr(model.PriorityMedium), DueOn: &day, PlannedOn: &day,
			EstimateMinutes: &daily.EstimateMinutes, CompletedAt: completedAt,
			CreatedAt: day.AddDate(0, 0, -1), UpdatedAt: day,
		}); err != nil {
			return err
		}
	}

	weekly := recurrences["Send weekly launch update"]
	for day := weekly.StartsOn; !day.After(l.today); day = day.AddDate(0, 0, 7) {
		completedAt := l.timestamp(day, 16)
		if err := l.insertTask(taskSpec{
			ID: id.New(), ContextID: &weekly.ContextID, ProjectID: weekly.ProjectID,
			RecurrenceID: &weekly.ID, OccurrenceOn: &day, Source: weekly.Source,
			CaptureMethod: "recurrence", Title: weekly.Title, Details: weekly.Details,
			Status: model.StatusDone, Priority: ptr(model.PriorityHigh), DueOn: &day,
			PlannedOn: &day, EstimateMinutes: &weekly.EstimateMinutes,
			DelegatedToID: weekly.DelegatedToID, CompletedAt: &completedAt,
			CreatedAt: day.AddDate(0, 0, -2), UpdatedAt: day,
		}); err != nil {
			return err
		}
	}

	monthly := recurrences["Reconcile household expenses"]
	for day := monthly.StartsOn; !day.After(l.today); day = day.AddDate(0, 1, 0) {
		completedAt := l.timestamp(day.AddDate(0, 0, 2), 19)
		if err := l.insertTask(taskSpec{
			ID: id.New(), ContextID: &monthly.ContextID, ProjectID: monthly.ProjectID,
			RecurrenceID: &monthly.ID, OccurrenceOn: &day, Source: monthly.Source,
			CaptureMethod: "recurrence", Title: monthly.Title, Details: monthly.Details,
			Status: model.StatusDone, Priority: ptr(model.PriorityLow), DueOn: &day,
			EstimateMinutes: &monthly.EstimateMinutes, CompletedAt: &completedAt,
			CreatedAt: day, UpdatedAt: day.AddDate(0, 0, 2),
		}); err != nil {
			return err
		}
	}

	finished := recurrences["Wednesday tempo run"]
	for day := finished.StartsOn; !day.After(*finished.EndsOn); day = day.AddDate(0, 0, 7) {
		completedAt := l.timestamp(day, 7)
		if err := l.insertTask(taskSpec{
			ID: id.New(), ContextID: &finished.ContextID, ProjectID: finished.ProjectID,
			RecurrenceID: &finished.ID, OccurrenceOn: &day, Source: finished.Source,
			CaptureMethod: "recurrence", Title: finished.Title, Details: finished.Details,
			Status: model.StatusDone, Priority: ptr(model.PriorityMedium), DueOn: &day,
			EstimateMinutes: &finished.EstimateMinutes, CompletedAt: &completedAt,
			CreatedAt: day.AddDate(0, 0, -1), UpdatedAt: day,
		}); err != nil {
			return err
		}
	}

	return nil
}

func (l *loader) loadHistoricalTasks(
	contexts, projects, people map[string]string,
) error {
	sources := []string{"self", "email", "slack", "google_chat", "meeting", "phone"}
	captures := []string{"form", "api", "hermes", "chrome_ext", "ios_widget", "voice"}
	priorities := []*string{
		ptr(model.PriorityUrgent), ptr(model.PriorityHigh), ptr(model.PriorityMedium),
		ptr(model.PriorityLow), nil,
	}
	projectIDs := []string{
		projects["Product launch"], projects["Operations"], projects["Q2 planning"],
		projects["Home office"], projects["Japan trip"], projects["10K training"],
	}
	contextIDs := []string{
		contexts["work"], contexts["work"], contexts["work"],
		contexts["personal"], contexts["personal"], contexts["health"],
	}
	completedTitles := []string{
		"Publish release notes", "Review customer feedback", "Prepare planning agenda",
		"Order desk accessories", "Book train tickets", "Complete interval session",
	}
	cancelledTitles := []string{
		"Attend optional webinar", "Try alternate analytics tool", "Compare standing mats",
	}

	i := 0
	for day := l.start; day.Before(l.today.AddDate(0, 0, -6)); day = day.AddDate(0, 0, 7) {
		slot := i % len(projectIDs)
		completedAt := l.timestamp(day.AddDate(0, 0, 2), 17)
		estimate := 20 + (i%4)*15
		if err := l.insertTask(taskSpec{
			ID: id.New(), ContextID: &contextIDs[slot], ProjectID: &projectIDs[slot],
			Source: sources[i%len(sources)], CaptureMethod: captures[i%len(captures)],
			Title:   completedTitles[i%len(completedTitles)],
			Details: "Historical fixture completed during the prior three months.",
			Status:  model.StatusDone, Priority: priorities[i%len(priorities)],
			DueOn: ptr(day.AddDate(0, 0, 2)), PlannedOn: ptr(day.AddDate(0, 0, 1)),
			EstimateMinutes: &estimate, DelegatedToID: optionalDelegate(i, people),
			CompletedAt: &completedAt, CreatedAt: day, UpdatedAt: day.AddDate(0, 0, 2),
		}); err != nil {
			return err
		}

		if i%2 == 0 {
			cancelledAt := l.timestamp(day.AddDate(0, 0, 4), 12)
			cancelEstimate := 30
			if err := l.insertTask(taskSpec{
				ID: id.New(), ContextID: &contextIDs[slot], ProjectID: &projectIDs[slot],
				Source: sources[(i+1)%len(sources)], CaptureMethod: captures[(i+3)%len(captures)],
				Title:   cancelledTitles[i%len(cancelledTitles)],
				Details: "Historical fixture cancelled after priorities changed.",
				Status:  model.StatusCancelled, Priority: priorities[(i+2)%len(priorities)],
				DueOn: ptr(day.AddDate(0, 0, 5)), EstimateMinutes: &cancelEstimate,
				CancelledAt: &cancelledAt, CreatedAt: day.AddDate(0, 0, 3),
				UpdatedAt: day.AddDate(0, 0, 4),
			}); err != nil {
				return err
			}
		}

		i++
	}

	return nil
}

func (l *loader) loadCurrentTasks(
	contexts, projects, people map[string]string,
) error {
	work := contexts["work"]
	personal := contexts["personal"]
	health := contexts["health"]
	product := projects["Product launch"]
	home := projects["Home office"]
	operations := projects["Operations"]
	yesterday := l.today.AddDate(0, 0, -1)
	tomorrow := l.today.AddDate(0, 0, 1)
	nextWeek := l.today.AddDate(0, 0, 7)

	current := []taskSpec{
		{
			ID: id.New(), Source: "self", CaptureMethod: "voice",
			Title: "Turn voice note into an action", Details: "Inbox item without a context.",
			Status: model.StatusInbox, Priority: ptr(model.PriorityUrgent),
			CreatedAt: l.today, UpdatedAt: l.today,
		},
		{
			ID: id.New(), Source: "email", CaptureMethod: "ios_widget",
			Title: "Reply to the forwarded introduction", Status: model.StatusInbox,
			CreatedAt: yesterday, UpdatedAt: yesterday,
		},
		{
			ID: id.New(), ContextID: &work, ProjectID: &product, Source: "slack",
			CaptureMethod: "chrome_ext", Title: "Fix launch checklist gaps",
			Details: "Overdue short task with an external reference.", Status: model.StatusTodo,
			Priority: ptr(model.PriorityUrgent), DueOn: &yesterday, EstimateMinutes: ptr(45),
			ReferenceURL: "https://example.com/launch-checklist", ReferenceLabel: "Launch checklist",
			CreatedAt: yesterday.AddDate(0, 0, -3), UpdatedAt: yesterday,
		},
		{
			ID: id.New(), ContextID: &work, ProjectID: &product, Source: "meeting",
			CaptureMethod: "hermes", Title: "Draft launch announcement",
			Status: model.StatusInProgress, Priority: ptr(model.PriorityHigh),
			PlannedOn: &l.today, DueOn: &tomorrow, EstimateMinutes: ptr(90),
			CreatedAt: yesterday.AddDate(0, 0, -2), UpdatedAt: l.today,
		},
		{
			ID: id.New(), ContextID: &health, Source: "self", CaptureMethod: "form",
			Title: "Book annual health check", Status: model.StatusTodo,
			Priority: ptr(model.PriorityMedium), PlannedOn: &l.today, DueOn: &nextWeek,
			EstimateMinutes: ptr(15), CreatedAt: yesterday, UpdatedAt: yesterday,
		},
		{
			ID: id.New(), ContextID: &personal, ProjectID: &home, Source: "phone",
			CaptureMethod: "api", Title: "Finish the home office refresh",
			Details: "Long task represented by a parent and three subtasks.",
			Status:  model.StatusTodo, Priority: ptr(model.PriorityHigh), DueOn: &nextWeek,
			EstimateMinutes: ptr(180), CreatedAt: l.today.AddDate(0, 0, -8), UpdatedAt: l.today,
		},
	}

	for i := range current {
		if err := l.insertTask(current[i]); err != nil {
			return err
		}
	}

	parentID := current[len(current)-1].ID
	completedAt := l.timestamp(yesterday, 18)
	children := []taskSpec{
		{
			ID: id.New(), ContextID: &personal, ProjectID: &home, ParentID: &parentID,
			Source: "self", CaptureMethod: "form", Title: "Measure the desk wall",
			Status: model.StatusDone, Priority: ptr(model.PriorityLow), EstimateMinutes: ptr(20),
			CompletedAt: &completedAt, CreatedAt: yesterday.AddDate(0, 0, -5), UpdatedAt: yesterday,
		},
		{
			ID: id.New(), ContextID: &personal, ProjectID: &home, ParentID: &parentID,
			Source: "email", CaptureMethod: "api", Title: "Choose monitor arm",
			Status: model.StatusInProgress, Priority: ptr(model.PriorityMedium),
			EstimateMinutes: ptr(40), CreatedAt: yesterday.AddDate(0, 0, -2), UpdatedAt: l.today,
		},
		{
			ID: id.New(), ContextID: &personal, ProjectID: &home, ParentID: &parentID,
			Source: "self", CaptureMethod: "form", Title: "Route the power cables",
			Status: model.StatusTodo, Priority: ptr(model.PriorityLow), PlannedOn: &tomorrow,
			EstimateMinutes: ptr(30), CreatedAt: yesterday, UpdatedAt: yesterday,
		},
	}
	for _, child := range children {
		if err := l.insertTask(child); err != nil {
			return err
		}
	}

	blockerID := id.New()
	if err := l.insertTask(taskSpec{
		ID: blockerID, ContextID: &work, ProjectID: &product, Source: "email",
		CaptureMethod: "api", Title: "Receive legal approval", Status: model.StatusTodo,
		Priority: ptr(model.PriorityHigh), DueOn: &tomorrow, DelegatedToID: ptr(people["Priya Shah"]),
		CreatedAt: yesterday.AddDate(0, 0, -2), UpdatedAt: yesterday,
	}); err != nil {
		return err
	}

	more := []taskSpec{
		{
			ID: id.New(), ContextID: &work, ProjectID: &product, Source: "slack",
			CaptureMethod: "hermes", Title: "Publish pricing page", Status: model.StatusBlocked,
			Priority: ptr(model.PriorityUrgent), BlockedByID: &blockerID, DueOn: &tomorrow,
			EstimateMinutes: ptr(60), CreatedAt: yesterday.AddDate(0, 0, -2), UpdatedAt: l.today,
		},
		{
			ID: id.New(), ContextID: &work, ProjectID: &operations, Source: "google_chat",
			CaptureMethod: "chrome_ext", Title: "Decide offsite location", Status: model.StatusBlocked,
			Priority: ptr(model.PriorityMedium), Details: "Blocked without a specific task dependency.",
			CreatedAt: yesterday.AddDate(0, 0, -4), UpdatedAt: yesterday,
		},
		{
			ID: id.New(), ContextID: &work, ProjectID: &product, Source: "meeting",
			CaptureMethod: "form", Title: "Get final screenshots from design",
			Status: model.StatusDelegated, Priority: ptr(model.PriorityHigh),
			DelegatedToID: ptr(people["Maya Chen"]), DueOn: &tomorrow,
			CreatedAt: yesterday.AddDate(0, 0, -3), UpdatedAt: yesterday,
		},
		{
			ID: id.New(), ContextID: &personal, Source: "phone", CaptureMethod: "ios_widget",
			Title: "Cancel duplicate furniture delivery", Status: model.StatusCancelled,
			Priority: ptr(model.PriorityLow), CancelledAt: ptr(l.timestamp(yesterday, 14)),
			CreatedAt: yesterday.AddDate(0, 0, -1), UpdatedAt: yesterday,
		},
		{
			ID: id.New(), ContextID: &work, ProjectID: &product, Source: "self",
			CaptureMethod: "form", Title: "Confirm today's launch owners", Status: model.StatusDone,
			Priority: ptr(model.PriorityHigh), CompletedAt: ptr(l.timestamp(l.today, 10)),
			CreatedAt: yesterday, UpdatedAt: l.today,
		},
	}

	for _, spec := range more {
		if err := l.insertTask(spec); err != nil {
			return err
		}
	}

	return nil
}

func (l *loader) loadTombstone(contexts map[string]string) error {
	deletedAt := l.timestamp(l.today.AddDate(0, 0, -10), 15)
	created := l.today.AddDate(0, 0, -20)
	work := contexts["work"]

	return l.insertTask(taskSpec{
		ID: id.New(), ContextID: &work, Source: "self", CaptureMethod: "api",
		Title: "Deleted fixture task", Details: "Tombstone used to exercise sync deletion handling.",
		Status: model.StatusTodo, CreatedAt: created, UpdatedAt: l.today.AddDate(0, 0, -10),
		DeletedAt: &deletedAt,
	})
}

func (l *loader) insertTask(spec taskSpec) error {
	createdAt := l.timestamp(spec.CreatedAt, 9)
	updatedAt := l.timestamp(spec.UpdatedAt, 17)
	if spec.CreatedAt.IsZero() {
		createdAt = l.timestamp(l.today, 9)
	}
	if spec.UpdatedAt.IsZero() {
		updatedAt = createdAt
	}

	_, err := l.tx.ExecContext(l.ctx, `
		INSERT INTO tasks (
			id, user_id, context_id, project_id, parent_id, recurrence_id, occurrence_on,
			source_key, capture_method, title, details, status, priority, due_on, planned_on,
			estimate_minutes, delegated_to_id, blocked_by_id, reference_url, reference_label,
			completed_at, cancelled_at, created_at, updated_at, deleted_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		spec.ID, l.userID, spec.ContextID, spec.ProjectID, spec.ParentID,
		spec.RecurrenceID, dateValue(spec.OccurrenceOn), emptyAsNil(spec.Source),
		spec.CaptureMethod, spec.Title, emptyAsNil(spec.Details), spec.Status, spec.Priority,
		dateValue(spec.DueOn), dateValue(spec.PlannedOn), spec.EstimateMinutes,
		spec.DelegatedToID, spec.BlockedByID, emptyAsNil(spec.ReferenceURL),
		emptyAsNil(spec.ReferenceLabel), spec.CompletedAt, spec.CancelledAt,
		createdAt, updatedAt, spec.DeletedAt,
	)
	if err != nil {
		return fmt.Errorf("fixtures: insert task %q: %w", spec.Title, err)
	}

	l.summary.Tasks++

	return nil
}

func (l *loader) timestamp(day time.Time, hour int) string {
	if day.IsZero() {
		day = l.today
	}

	local := time.Date(day.Year(), day.Month(), day.Day(), hour, 0, 0, 0, l.location)
	timestamp := local.UTC()
	if timestamp.After(l.now) && day.Format(database.DateOnly) == l.today.Format(database.DateOnly) {
		timestamp = l.now
	}

	return timestamp.Format(database.Timestamp)
}

func onOrAfter(day time.Time, weekday time.Weekday) time.Time {
	offset := (int(weekday) - int(day.Weekday()) + 7) % 7

	return day.AddDate(0, 0, offset)
}

func firstOfMonth(day time.Time) time.Time {
	return time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, day.Location())
}

func optionalDelegate(i int, people map[string]string) *string {
	if i%3 == 0 {
		return ptr(people["Luca Martin"])
	}

	return nil
}

func emptyAsNil(value string) any {
	if value == "" {
		return nil
	}

	return value
}

func dateValue(value any) any {
	switch v := value.(type) {
	case time.Time:
		return v.Format(database.DateOnly)
	case *time.Time:
		if v == nil {
			return nil
		}

		return v.Format(database.DateOnly)
	default:
		return nil
	}
}

func valueOr(value *string, fallback string) string {
	if value == nil {
		return fallback
	}

	return *value
}

func ptr[T any](value T) *T {
	return &value
}
