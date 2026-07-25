package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nls/checkmate/server/internal/database"
	"github.com/nls/checkmate/server/internal/model"
	"github.com/nls/checkmate/server/internal/store"
)

// The tool surface.
//
// Kept deliberately small. Every tool costs context in the model's prompt and
// adds another thing for it to choose wrongly between, so each one here earns its
// place by being a distinct action a person actually asks for. complete_task
// exists separately from update_task, for instance, because finishing something
// is the most common single action and "update the status field to done" is a lot
// of ceremony for it.
//
// Two things the tools deliberately do NOT expose:
//
//   - Creating or deleting contexts. Contexts are the four stable areas of the
//     user's life; a model inventing a fifth is far more likely to be a mistake
//     than an intention. They are managed through the app.
//   - The sync feed. That is for device clients replicating state, and would only
//     flood a model's context.
//
// Every handler resolves the caller from the token and passes that user id to the
// store, so a tool cannot reach another account's data even if a model asks it to.

// registerTools wires every tool onto the server.
func (h *Handler) registerTools(server *mcp.Server) {
	h.addReadTools(server)
	h.addWriteTools(server)
}

// ---------------------------------------------------------------------------
// Shared shapes
// ---------------------------------------------------------------------------

// noInput is for tools that take no arguments.
type noInput struct{}

// taskView is how a task is presented to a model.
//
// A trimmed projection rather than the full row: internal bookkeeping like rev
// and updated_at is noise in a model's context, and ids are only included where
// the model needs them to act.
type taskView struct {
	ID              string `json:"id" jsonschema:"the task's id, used to update or complete it"`
	Title           string `json:"title"`
	Status          string `json:"status" jsonschema:"one of inbox, todo, in_progress, blocked, delegated, done, cancelled"`
	Kind            string `json:"kind" jsonschema:"short, long, recurring, delegated or blocked"`
	Details         string `json:"details,omitempty"`
	ContextID       string `json:"context_id,omitempty"`
	ContextName     string `json:"context_name,omitempty"`
	ProjectID       string `json:"project_id,omitempty"`
	ProjectName     string `json:"project_name,omitempty"`
	DueOn           string `json:"due_on,omitempty" jsonschema:"YYYY-MM-DD, when the task is due"`
	PlannedOn       string `json:"planned_on,omitempty" jsonschema:"YYYY-MM-DD, the day the user intends to work on it"`
	EstimateMinutes int64  `json:"estimate_minutes,omitempty"`
	DelegatedTo     string `json:"delegated_to,omitempty" jsonschema:"the name of the person this is waiting on"`
	BlockedBy       string `json:"blocked_by,omitempty" jsonschema:"the id of the task blocking this one"`
	ReferenceURL    string `json:"reference_url,omitempty" jsonschema:"a link to where this came from"`
	ParentID        string `json:"parent_id,omitempty"`
	CompletedAt     string `json:"completed_at,omitempty"`
	Source          string `json:"source,omitempty" jsonschema:"where the task came from: self, email, slack, google_chat, meeting or phone"`
	OccurrenceOn    string `json:"occurrence_on,omitempty" jsonschema:"for a recurring task, the date this occurrence stands for"`
}

// lookups resolves ids to names so a model does not have to make a second call to
// understand what it is looking at.
type lookups struct {
	contexts map[string]string
	projects map[string]string
	people   map[string]string
}

func (h *Handler) loadLookups(ctx context.Context, userID string) (lookups, error) {
	out := lookups{
		contexts: map[string]string{},
		projects: map[string]string{},
		people:   map[string]string{},
	}

	contexts, _, err := h.store.ListContexts(ctx, userID, store.ContextFilter{})
	if err != nil {
		return lookups{}, err
	}

	for _, c := range contexts {
		out.contexts[c.ID] = c.Name
	}

	projects, _, err := h.store.ListProjects(ctx, userID, store.ProjectFilter{})
	if err != nil {
		return lookups{}, err
	}

	for _, p := range projects {
		out.projects[p.ID] = p.Name
	}

	people, _, err := h.store.ListPeople(ctx, userID, store.PersonFilter{})
	if err != nil {
		return lookups{}, err
	}

	for _, p := range people {
		out.people[p.ID] = p.Name
	}

	return out, nil
}

func toView(task model.Task, l lookups) taskView {
	v := taskView{
		ID:     task.ID,
		Title:  task.Title,
		Status: task.Status,
		Kind:   task.Kind,
	}

	deref := func(p *string) string {
		if p == nil {
			return ""
		}

		return *p
	}

	v.Details = deref(task.Details)
	v.ContextID = deref(task.ContextID)
	v.ProjectID = deref(task.ProjectID)
	v.DueOn = deref(task.DueOn)
	v.PlannedOn = deref(task.PlannedOn)
	v.BlockedBy = deref(task.BlockedByID)
	v.ReferenceURL = deref(task.ReferenceURL)
	v.ParentID = deref(task.ParentID)
	v.CompletedAt = deref(task.CompletedAt)
	v.Source = deref(task.Source)
	v.OccurrenceOn = deref(task.OccurrenceOn)

	if task.EstimateMinutes != nil {
		v.EstimateMinutes = *task.EstimateMinutes
	}

	if v.ContextID != "" {
		v.ContextName = l.contexts[v.ContextID]
	}

	if v.ProjectID != "" {
		v.ProjectName = l.projects[v.ProjectID]
	}

	if task.DelegatedToID != nil {
		v.DelegatedTo = l.people[*task.DelegatedToID]
	}

	return v
}

func (h *Handler) viewTasks(ctx context.Context, userID string, tasks []model.Task) ([]taskView, error) {
	l, err := h.loadLookups(ctx, userID)
	if err != nil {
		return nil, err
	}

	out := make([]taskView, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, toView(task, l))
	}

	return out, nil
}

// toolError builds a tool execution error.
//
// Distinct from a protocol error on purpose: the spec says validation and business
// failures belong in the result with isError set, because a model can read them
// and correct itself, whereas a JSON-RPC error suggests the request was malformed.
func toolError(format string, args ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, args...)}},
	}
}

// summary attaches a human-readable line alongside structured output. The SDK
// would otherwise serialize the struct as JSON text, which a model can read but a
// person auditing a transcript cannot.
func summary(text string) []mcp.Content {
	return []mcp.Content{&mcp.TextContent{Text: text}}
}

// ---------------------------------------------------------------------------
// Read tools
// ---------------------------------------------------------------------------

type briefOutput struct {
	Date       string            `json:"date"`
	Timezone   string            `json:"timezone"`
	Totals     store.BriefTotals `json:"totals"`
	Overdue    []taskView        `json:"overdue"`
	DueToday   []taskView        `json:"due_today"`
	Planned    []taskView        `json:"planned"`
	InProgress []taskView        `json:"in_progress"`
	Inbox      []taskView        `json:"inbox"`
	Blocked    []taskView        `json:"blocked"`
	WaitingOn  []waitingOnView   `json:"waiting_on"`
}

type waitingOnView struct {
	Person string     `json:"person"`
	Tasks  []taskView `json:"tasks"`
}

type briefInput struct {
	Date      string `json:"date,omitempty" jsonschema:"the day to brief on as YYYY-MM-DD; defaults to today in the user's timezone"`
	ContextID string `json:"context_id,omitempty" jsonschema:"limit the brief to one context"`
}

func (h *Handler) addReadTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:  "daily_brief",
		Title: "Daily brief",
		Description: "Get an overview of the user's day: what is overdue, due today, " +
			"planned for today, in progress, sitting untriaged in the inbox, blocked, " +
			"and what they are waiting on from other people. Start here when asked " +
			"anything open-ended about the day, the week, or what to work on.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in briefInput) (*mcp.CallToolResult, briefOutput, error) {
		userID, timezone, _, err := caller(ctx)
		if err != nil {
			return nil, briefOutput{}, err
		}

		location, err := time.LoadLocation(timezone)
		if err != nil {
			location = time.UTC
		}

		date := in.Date
		if date == "" {
			date = time.Now().In(location).Format(database.DateOnly)
		} else if _, err := time.Parse(database.DateOnly, date); err != nil {
			return toolError("date %q is not a valid YYYY-MM-DD date", in.Date), briefOutput{}, nil
		}

		brief, err := h.store.Brief(ctx, userID, store.BriefFilter{
			Date:      date,
			Timezone:  location.String(),
			ContextID: in.ContextID,
		})
		if err != nil {
			return nil, briefOutput{}, err
		}

		l, err := h.loadLookups(ctx, userID)
		if err != nil {
			return nil, briefOutput{}, err
		}

		views := func(tasks []model.Task) []taskView {
			out := make([]taskView, 0, len(tasks))
			for _, t := range tasks {
				out = append(out, toView(t, l))
			}

			return out
		}

		out := briefOutput{
			Date:       brief.Date,
			Timezone:   brief.Timezone,
			Totals:     brief.Totals,
			Overdue:    views(brief.Overdue),
			DueToday:   views(brief.DueToday),
			Planned:    views(brief.Planned),
			InProgress: views(brief.InProgress),
			Inbox:      views(brief.Inbox),
			Blocked:    views(brief.Blocked),
			WaitingOn:  make([]waitingOnView, 0, len(brief.WaitingOn)),
		}

		for _, group := range brief.WaitingOn {
			out.WaitingOn = append(out.WaitingOn, waitingOnView{
				Person: group.PersonName,
				Tasks:  views(group.Tasks),
			})
		}

		return &mcp.CallToolResult{Content: summary(briefSummary(out))}, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:  "list_tasks",
		Title: "List tasks",
		Description: "Search and filter the user's tasks. Every filter is optional; " +
			"with none, returns the most recently created open tasks. Use context_id " +
			"from list_contexts to narrow by area, inbox_only for untriaged captures, " +
			"and query to search titles and details.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, h.listTasks)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_task",
		Title:       "Get a task",
		Description: "Fetch one task by id, together with its subtasks.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, h.getTask)

	mcp.AddTool(server, &mcp.Tool{
		Name:  "list_contexts",
		Title: "List contexts",
		Description: "List the user's contexts, the top-level areas a task can belong " +
			"to. Call this before creating or moving a task, since context ids differ " +
			"per user and cannot be guessed.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, h.listContexts)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_projects",
		Title:       "List projects",
		Description: "List the user's projects, optionally limited to one context.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, h.listProjects)

	mcp.AddTool(server, &mcp.Tool{
		Name:  "list_people",
		Title: "List people",
		Description: "List the people the user delegates work to and follows up with. " +
			"Useful before delegating, though delegate_task also accepts a plain name.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, h.listPeople)

	mcp.AddTool(server, &mcp.Tool{
		Name:  "list_recurrences",
		Title: "List recurring task templates",
		Description: "List the user's recurring task templates and when each one next " +
			"produces a task.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, h.listRecurrences)
}

func briefSummary(b briefOutput) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "Brief for %s (%s):\n", b.Date, b.Timezone)
	fmt.Fprintf(&sb, "- %d overdue, %d due today, %d planned for today\n",
		b.Totals.Overdue, b.Totals.DueToday, b.Totals.Planned)
	fmt.Fprintf(&sb, "- %d in progress, %d blocked, %d waiting on other people\n",
		b.Totals.InProgress, b.Totals.Blocked, b.Totals.WaitingOn)
	fmt.Fprintf(&sb, "- %d untriaged in the inbox, %d completed today\n",
		b.Totals.Inbox, b.Totals.CompletedToday)

	if b.Totals.Planned > 0 {
		fmt.Fprintf(&sb, "- %d minutes estimated for today", b.Totals.PlannedMinutes)

		if b.Totals.PlannedWithoutEstimate > 0 {
			fmt.Fprintf(&sb, ", with %d planned task(s) carrying no estimate",
				b.Totals.PlannedWithoutEstimate)
		}

		sb.WriteString("\n")
	}

	return strings.TrimRight(sb.String(), "\n")
}

type listTasksInput struct {
	Status        []string `json:"status,omitempty" jsonschema:"filter by status: inbox, todo, in_progress, blocked, delegated, done, cancelled"`
	Kind          []string `json:"kind,omitempty" jsonschema:"filter by kind: short, long, recurring, delegated, blocked"`
	ContextID     string   `json:"context_id,omitempty" jsonschema:"only tasks in this context"`
	ProjectID     string   `json:"project_id,omitempty" jsonschema:"only tasks in this project"`
	InboxOnly     bool     `json:"inbox_only,omitempty" jsonschema:"only tasks still awaiting triage, meaning status inbox"`
	DelegatedToID string   `json:"delegated_to_id,omitempty" jsonschema:"only tasks delegated to this person"`
	PlannedOn     string   `json:"planned_on,omitempty" jsonschema:"only tasks planned for this YYYY-MM-DD date"`
	DueBefore     string   `json:"due_before,omitempty" jsonschema:"only tasks due on or before this YYYY-MM-DD date"`
	DueAfter      string   `json:"due_after,omitempty" jsonschema:"only tasks due on or after this YYYY-MM-DD date"`
	Query         string   `json:"query,omitempty" jsonschema:"free text searched in titles and details"`
	IncludeDone   bool     `json:"include_done,omitempty" jsonschema:"include completed and cancelled tasks; they are excluded by default"`
	Limit         int      `json:"limit,omitempty" jsonschema:"maximum tasks to return, 1 to 200, default 50"`
}

type listTasksOutput struct {
	Tasks []taskView `json:"tasks"`
	Count int        `json:"count"`

	// Truncated tells the model the answer is partial, so it narrows the filter
	// rather than assuming it has seen everything.
	Truncated bool `json:"truncated"`
}

func (h *Handler) listTasks(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in listTasksInput,
) (*mcp.CallToolResult, listTasksOutput, error) {
	userID, _, _, err := caller(ctx)
	if err != nil {
		return nil, listTasksOutput{}, err
	}

	for field, value := range map[string]string{
		"planned_on": in.PlannedOn,
		"due_before": in.DueBefore,
		"due_after":  in.DueAfter,
	} {
		if value == "" {
			continue
		}

		if _, err := time.Parse(database.DateOnly, value); err != nil {
			return toolError("%s %q is not a valid YYYY-MM-DD date", field, value),
				listTasksOutput{}, nil
		}
	}

	for _, status := range in.Status {
		if !containsStr(model.TaskStatuses, status) {
			return toolError("status %q is not valid; use one of: %s",
				status, strings.Join(model.TaskStatuses, ", ")), listTasksOutput{}, nil
		}
	}

	for _, kind := range in.Kind {
		if !containsStr(model.TaskKinds, kind) {
			return toolError("kind %q is not valid; use one of: %s",
				kind, strings.Join(model.TaskKinds, ", ")), listTasksOutput{}, nil
		}
	}

	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}

	filter := store.TaskFilter{
		Kind:          in.Kind,
		ContextID:     in.ContextID,
		ProjectID:     in.ProjectID,
		DelegatedToID: in.DelegatedToID,
		PlannedOn:     in.PlannedOn,
		DueBefore:     in.DueBefore,
		DueAfter:      in.DueAfter,
		Search:        in.Query,
	}
	filter.Limit = limit

	switch {
	case in.InboxOnly:
		// "Inbox" means awaiting triage, which is the status, not merely the
		// absence of a context. The two diverge: delegating an untriaged capture
		// leaves it without a context while it is plainly no longer waiting to be
		// sorted. Matching on status keeps this filter and the daily brief
		// answering the same question.
		filter.Status = []string{model.StatusInbox}

	case len(in.Status) > 0:
		filter.Status = in.Status

	case !in.IncludeDone:
		// Done and cancelled are excluded unless asked for: a model reasoning
		// about what is left should not have to filter history out itself.
		filter.Status = []string{
			model.StatusInbox, model.StatusTodo, model.StatusInProgress,
			model.StatusBlocked, model.StatusDelegated,
		}
	}

	tasks, cursor, err := h.store.ListTasks(ctx, userID, filter)
	if err != nil {
		return nil, listTasksOutput{}, err
	}

	views, err := h.viewTasks(ctx, userID, tasks)
	if err != nil {
		return nil, listTasksOutput{}, err
	}

	out := listTasksOutput{Tasks: views, Count: len(views), Truncated: cursor != ""}

	text := fmt.Sprintf("%d task(s)", out.Count)
	if out.Truncated {
		text += " (more available; narrow the filter or raise the limit)"
	}

	return &mcp.CallToolResult{Content: summary(text)}, out, nil
}

type getTaskInput struct {
	TaskID string `json:"task_id" jsonschema:"the id of the task to fetch"`
}

type getTaskOutput struct {
	Task     taskView   `json:"task"`
	Subtasks []taskView `json:"subtasks"`
}

func (h *Handler) getTask(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in getTaskInput,
) (*mcp.CallToolResult, getTaskOutput, error) {
	userID, _, _, err := caller(ctx)
	if err != nil {
		return nil, getTaskOutput{}, err
	}

	task, err := h.store.GetTask(ctx, userID, in.TaskID)
	if err != nil {
		if isNotFound(err) {
			return toolError("no task with id %q", in.TaskID), getTaskOutput{}, nil
		}

		return nil, getTaskOutput{}, err
	}

	children, _, err := h.store.ListTasks(ctx, userID, store.TaskFilter{ParentID: task.ID})
	if err != nil {
		return nil, getTaskOutput{}, err
	}

	l, err := h.loadLookups(ctx, userID)
	if err != nil {
		return nil, getTaskOutput{}, err
	}

	out := getTaskOutput{Task: toView(task, l), Subtasks: make([]taskView, 0, len(children))}
	for _, child := range children {
		out.Subtasks = append(out.Subtasks, toView(child, l))
	}

	return &mcp.CallToolResult{
		Content: summary(fmt.Sprintf("%s [%s]", task.Title, task.Status)),
	}, out, nil
}

type contextView struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	Archived bool   `json:"archived"`
}

type listContextsOutput struct {
	Contexts []contextView `json:"contexts"`
}

func (h *Handler) listContexts(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	_ noInput,
) (*mcp.CallToolResult, listContextsOutput, error) {
	userID, _, _, err := caller(ctx)
	if err != nil {
		return nil, listContextsOutput{}, err
	}

	contexts, _, err := h.store.ListContexts(ctx, userID, store.ContextFilter{IncludeArchived: true})
	if err != nil {
		return nil, listContextsOutput{}, err
	}

	out := listContextsOutput{Contexts: make([]contextView, 0, len(contexts))}

	names := make([]string, 0, len(contexts))

	for _, c := range contexts {
		out.Contexts = append(out.Contexts, contextView{
			ID:       c.ID,
			Name:     c.Name,
			Slug:     c.Slug,
			Archived: c.ArchivedAt != nil,
		})
		names = append(names, c.Name)
	}

	return &mcp.CallToolResult{Content: summary(strings.Join(names, ", "))}, out, nil
}

type listProjectsInput struct {
	ContextID string `json:"context_id,omitempty" jsonschema:"only projects in this context"`
}

type projectView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ContextID   string `json:"context_id"`
	ContextName string `json:"context_name,omitempty"`
	Status      string `json:"status"`
	Description string `json:"description,omitempty"`
}

type listProjectsOutput struct {
	Projects []projectView `json:"projects"`
}

func (h *Handler) listProjects(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in listProjectsInput,
) (*mcp.CallToolResult, listProjectsOutput, error) {
	userID, _, _, err := caller(ctx)
	if err != nil {
		return nil, listProjectsOutput{}, err
	}

	projects, _, err := h.store.ListProjects(ctx, userID, store.ProjectFilter{ContextID: in.ContextID})
	if err != nil {
		return nil, listProjectsOutput{}, err
	}

	l, err := h.loadLookups(ctx, userID)
	if err != nil {
		return nil, listProjectsOutput{}, err
	}

	out := listProjectsOutput{Projects: make([]projectView, 0, len(projects))}

	for _, p := range projects {
		view := projectView{
			ID:          p.ID,
			Name:        p.Name,
			ContextID:   p.ContextID,
			ContextName: l.contexts[p.ContextID],
			Status:      p.Status,
		}

		if p.Description != nil {
			view.Description = *p.Description
		}

		out.Projects = append(out.Projects, view)
	}

	return &mcp.CallToolResult{
		Content: summary(fmt.Sprintf("%d project(s)", len(out.Projects))),
	}, out, nil
}

type personView struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
}

type listPeopleOutput struct {
	People []personView `json:"people"`
}

func (h *Handler) listPeople(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	_ noInput,
) (*mcp.CallToolResult, listPeopleOutput, error) {
	userID, _, _, err := caller(ctx)
	if err != nil {
		return nil, listPeopleOutput{}, err
	}

	people, _, err := h.store.ListPeople(ctx, userID, store.PersonFilter{})
	if err != nil {
		return nil, listPeopleOutput{}, err
	}

	out := listPeopleOutput{People: make([]personView, 0, len(people))}

	for _, p := range people {
		view := personView{ID: p.ID, Name: p.Name}
		if p.Email != nil {
			view.Email = *p.Email
		}

		out.People = append(out.People, view)
	}

	return &mcp.CallToolResult{
		Content: summary(fmt.Sprintf("%d person/people", len(out.People))),
	}, out, nil
}

type recurrenceView struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	RRule            string `json:"rrule"`
	ContextID        string `json:"context_id"`
	ContextName      string `json:"context_name,omitempty"`
	Timezone         string `json:"timezone"`
	NextOccurrenceOn string `json:"next_occurrence_on,omitempty"`
	Active           bool   `json:"active"`
}

type listRecurrencesOutput struct {
	Recurrences []recurrenceView `json:"recurrences"`
}

func (h *Handler) listRecurrences(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	_ noInput,
) (*mcp.CallToolResult, listRecurrencesOutput, error) {
	userID, _, _, err := caller(ctx)
	if err != nil {
		return nil, listRecurrencesOutput{}, err
	}

	items, _, err := h.store.ListRecurrences(ctx, userID, store.RecurrenceFilter{})
	if err != nil {
		return nil, listRecurrencesOutput{}, err
	}

	l, err := h.loadLookups(ctx, userID)
	if err != nil {
		return nil, listRecurrencesOutput{}, err
	}

	out := listRecurrencesOutput{Recurrences: make([]recurrenceView, 0, len(items))}

	for _, r := range items {
		view := recurrenceView{
			ID:          r.ID,
			Title:       r.Title,
			RRule:       r.RRule,
			ContextID:   r.ContextID,
			ContextName: l.contexts[r.ContextID],
			Timezone:    r.Timezone,
			Active:      r.Active,
		}

		if r.NextOccurrenceOn != nil {
			view.NextOccurrenceOn = *r.NextOccurrenceOn
		}

		out.Recurrences = append(out.Recurrences, view)
	}

	return &mcp.CallToolResult{
		Content: summary(fmt.Sprintf("%d recurring template(s)", len(out.Recurrences))),
	}, out, nil
}

// containsStr reports membership, used to validate enum-ish inputs before they
// reach the store so a model gets a list of valid values rather than a
// constraint violation.
func containsStr(haystack []string, needle string) bool {
	return slices.Contains(haystack, needle)
}

// isNotFound reports a missing row, checked with errors.Is rather than by
// matching message text.
func isNotFound(err error) bool {
	return errors.Is(err, store.ErrNotFound)
}
