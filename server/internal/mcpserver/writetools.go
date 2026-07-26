package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nls/checkmate/server/internal/database"
	"github.com/nls/checkmate/server/internal/model"
	"github.com/nls/checkmate/server/internal/oauth"
	"github.com/nls/checkmate/server/internal/patch"
	"github.com/nls/checkmate/server/internal/store"
)

// captureMethod marks every task an MCP client creates.
//
// The brief separates where a task came from (source) from how it entered
// Checkmate (capture method). Anything arriving through this endpoint was captured
// by an agent, which is what "hermes" names, so the user can later see which of
// their tasks an assistant put there.
const captureMethod = "hermes"

// addWriteTools registers the mutating tools and records their scope requirement.
func (h *Handler) addWriteTools(server *mcp.Server) {
	// Registered through this helper so a tool cannot be added to the server
	// without also being added to the scope map the middleware enforces.
	write := func(t *mcp.Tool) *mcp.Tool {
		toolScopes[t.Name] = oauth.ScopeWrite

		if t.Annotations == nil {
			t.Annotations = &mcp.ToolAnnotations{}
		}

		return t
	}

	mcp.AddTool(server, write(&mcp.Tool{
		Name:  "create_task",
		Title: "Create a task",
		Description: "Create a task. Only the title is required. Leave context_id out " +
			"to capture into the inbox for the user to triage later, which is the right " +
			"choice when the area is not obvious. Set due_on for a real deadline and " +
			"planned_on for the day the user intends to do it. A day_slot can organize " +
			"a planned task into morning, midday, afternoon, evening or night.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: false},
	}), h.createTask)

	mcp.AddTool(server, write(&mcp.Tool{
		Name:  "update_task",
		Title: "Update a task",
		Description: "Change fields on an existing task. Omitted fields are left alone. " +
			"To clear an optional date use the matching clear_* flag. Use complete_task " +
			"to finish something and delegate_task to hand it to someone.",
	}), h.updateTask)

	mcp.AddTool(server, write(&mcp.Tool{
		Name:  "complete_task",
		Title: "Complete a task",
		Description: "Mark a task done. This is the tool for finishing something, " +
			"rather than update_task with a status.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true},
	}), h.completeTask)

	mcp.AddTool(server, write(&mcp.Tool{
		Name:  "cancel_task",
		Title: "Cancel a task",
		Description: "Close a task by deciding not to do it. Distinct from " +
			"complete_task, which means the work was done, and from delete_task, " +
			"which removes the record entirely. Use this when the user says they are " +
			"not going to do something but wants to remember that they decided so.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true},
	}), h.cancelTask)

	mcp.AddTool(server, write(&mcp.Tool{
		Name:  "delete_task",
		Title: "Delete a task",
		Description: "Delete a task and its subtasks. Prefer complete_task for work " +
			"that was finished; deleting is for things that should never have been " +
			"there. Anything blocked by it becomes unblocked.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), IdempotentHint: true},
	}), h.deleteTask)

	mcp.AddTool(server, write(&mcp.Tool{
		Name:  "delegate_task",
		Title: "Delegate a task",
		Description: "Hand a task to someone and start waiting on them. Accepts a " +
			"person's name; a person who does not exist yet is created. This sets the " +
			"status to delegated so the task shows up under what the user is waiting on.",
	}), h.delegateTask)

	mcp.AddTool(server, write(&mcp.Tool{
		Name:  "triage_task",
		Title: "Triage an inbox task",
		Description: "Give an untriaged inbox task a context, and optionally a project " +
			"and a day to work on it. This is how a quick capture becomes a real task.",
	}), h.triageTask)

	mcp.AddTool(server, write(&mcp.Tool{
		Name:  "create_project",
		Title: "Create a project",
		Description: "Create a project inside a context, to group related tasks. " +
			"Call list_projects first to avoid making a duplicate.",
	}), h.createProject)

	mcp.AddTool(server, write(&mcp.Tool{
		Name:  "create_recurrence",
		Title: "Create a recurring task",
		Description: "Create a template that produces a task on a schedule, given an " +
			"RFC 5545 recurrence rule such as FREQ=DAILY or FREQ=WEEKLY;BYDAY=MO. " +
			"Occurrences become ordinary tasks, so completing one does not affect the " +
			"next. Use lead_days to have a task appear before its date.",
	}), h.createRecurrence)
}

func boolPtr(v bool) *bool { return &v }

// ---------------------------------------------------------------------------

type createTaskInput struct {
	Title           string `json:"title" jsonschema:"what needs doing"`
	Details         string `json:"details,omitempty" jsonschema:"longer notes or context"`
	ContextID       string `json:"context_id,omitempty" jsonschema:"the context to file it under; omit to capture into the inbox"`
	ProjectID       string `json:"project_id,omitempty" jsonschema:"a project in the same context"`
	ParentID        string `json:"parent_id,omitempty" jsonschema:"make this a subtask of another task"`
	DueOn           string `json:"due_on,omitempty" jsonschema:"YYYY-MM-DD deadline"`
	PlannedOn       string `json:"planned_on,omitempty" jsonschema:"YYYY-MM-DD day to work on it"`
	DaySlot         string `json:"day_slot,omitempty" jsonschema:"morning, midday, afternoon, evening or night; requires planned_on"`
	SlotOrder       int64  `json:"slot_order,omitempty" jsonschema:"manual position inside the day slot; zero is first"`
	Priority        string `json:"priority,omitempty" jsonschema:"urgent, high, medium or low"`
	EstimateMinutes int64  `json:"estimate_minutes,omitempty" jsonschema:"how long it should take, in minutes"`
	DelegateTo      string `json:"delegate_to,omitempty" jsonschema:"a person's name to delegate this to immediately"`
	Source          string `json:"source,omitempty" jsonschema:"where it came from: self, email, slack, google_chat, meeting or phone"`
	ReferenceURL    string `json:"reference_url,omitempty" jsonschema:"an absolute http or https link to the origin, such as a Slack message"`
	ReferenceLabel  string `json:"reference_label,omitempty" jsonschema:"a short label for the reference link"`
}

type taskResult struct {
	Task taskView `json:"task"`
}

func (h *Handler) createTask(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in createTaskInput,
) (*mcp.CallToolResult, taskResult, error) {
	userID, _, scopes, err := caller(ctx)
	if err != nil {
		return nil, taskResult{}, err
	}

	if err := requireWrite(scopes); err != nil {
		return toolError("%v", err), taskResult{}, nil
	}

	if strings.TrimSpace(in.Title) == "" {
		return toolError("title is required"), taskResult{}, nil
	}

	if bad := invalidDates(map[string]string{"due_on": in.DueOn, "planned_on": in.PlannedOn}); bad != "" {
		return toolError("%s", bad), taskResult{}, nil
	}

	if in.Source != "" && !containsStr(sourceKeys, in.Source) {
		return toolError("source %q is not valid; use one of: %s",
			in.Source, strings.Join(sourceKeys, ", ")), taskResult{}, nil
	}

	if in.Priority != "" && !containsStr(model.TaskPriorities, in.Priority) {
		return toolError("priority %q is not valid; use one of: %s",
			in.Priority, strings.Join(model.TaskPriorities, ", ")), taskResult{}, nil
	}

	if in.DaySlot != "" && !containsStr(model.DaySlots, in.DaySlot) {
		return toolError("day_slot %q is not valid; use one of: %s",
			in.DaySlot, strings.Join(model.DaySlots, ", ")), taskResult{}, nil
	}

	if in.DaySlot != "" && in.PlannedOn == "" {
		return toolError("planned_on is required when day_slot is set"), taskResult{}, nil
	}

	if in.SlotOrder < 0 {
		return toolError("slot_order must be zero or greater"), taskResult{}, nil
	}

	params := store.TaskCreate{
		Title:          strings.TrimSpace(in.Title),
		CaptureMethod:  captureMethod,
		Details:        optional(in.Details),
		ContextID:      optional(in.ContextID),
		ProjectID:      optional(in.ProjectID),
		ParentID:       optional(in.ParentID),
		DueOn:          optional(in.DueOn),
		PlannedOn:      optional(in.PlannedOn),
		DaySlot:        optional(in.DaySlot),
		Priority:       optional(in.Priority),
		Source:         optional(in.Source),
		ReferenceURL:   optional(in.ReferenceURL),
		ReferenceLabel: optional(in.ReferenceLabel),
	}

	if in.SlotOrder > 0 {
		slotOrder := in.SlotOrder
		params.SlotOrder = &slotOrder
	}

	if in.EstimateMinutes > 0 {
		estimate := in.EstimateMinutes
		params.EstimateMinutes = &estimate
	}

	// Delegating on creation resolves the person by name, matching the REST
	// endpoint's convenience so "add a task and give it to Marc" is one call.
	if name := strings.TrimSpace(in.DelegateTo); name != "" {
		person, err := h.store.FindOrCreatePerson(ctx, userID, name)
		if err != nil {
			return h.storeToolError(err), taskResult{}, nil
		}

		params.DelegatedToID = &person.ID
		params.Status = model.StatusDelegated
	}

	task, err := h.store.CreateTask(ctx, userID, params)
	if err != nil {
		return h.storeToolError(err), taskResult{}, nil
	}

	return h.taskOutcome(ctx, userID, task, "Created")
}

type updateTaskInput struct {
	TaskID          string `json:"task_id" jsonschema:"the task to change"`
	Title           string `json:"title,omitempty"`
	Details         string `json:"details,omitempty"`
	Status          string `json:"status,omitempty" jsonschema:"inbox, todo, in_progress, blocked, done or cancelled; use delegate_task to delegate"`
	ContextID       string `json:"context_id,omitempty"`
	ProjectID       string `json:"project_id,omitempty"`
	DueOn           string `json:"due_on,omitempty" jsonschema:"YYYY-MM-DD"`
	PlannedOn       string `json:"planned_on,omitempty" jsonschema:"YYYY-MM-DD"`
	DaySlot         string `json:"day_slot,omitempty" jsonschema:"morning, midday, afternoon, evening or night; requires a planned date"`
	SlotOrder       *int64 `json:"slot_order,omitempty" jsonschema:"manual position inside the day slot; zero is first"`
	Priority        string `json:"priority,omitempty" jsonschema:"urgent, high, medium or low"`
	EstimateMinutes int64  `json:"estimate_minutes,omitempty"`
	BlockedByID     string `json:"blocked_by_id,omitempty" jsonschema:"the id of the task blocking this one; also sets the status to blocked"`

	ClearDueOn     bool `json:"clear_due_on,omitempty" jsonschema:"remove the due date"`
	ClearPlannedOn bool `json:"clear_planned_on,omitempty" jsonschema:"remove the planned date"`
	ClearDaySlot   bool `json:"clear_day_slot,omitempty" jsonschema:"remove the morning, midday, afternoon, evening or night slot"`
	ClearPriority  bool `json:"clear_priority,omitempty" jsonschema:"remove the priority"`
	ClearBlocker   bool `json:"clear_blocker,omitempty" jsonschema:"remove the blocker and return the task to todo"`
}

func (h *Handler) updateTask(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in updateTaskInput,
) (*mcp.CallToolResult, taskResult, error) {
	userID, _, scopes, err := caller(ctx)
	if err != nil {
		return nil, taskResult{}, err
	}

	if err := requireWrite(scopes); err != nil {
		return toolError("%v", err), taskResult{}, nil
	}

	if in.TaskID == "" {
		return toolError("task_id is required"), taskResult{}, nil
	}

	if bad := invalidDates(map[string]string{"due_on": in.DueOn, "planned_on": in.PlannedOn}); bad != "" {
		return toolError("%s", bad), taskResult{}, nil
	}

	if in.Status == model.StatusDelegated {
		return toolError("use delegate_task to delegate a task, so a person is named"), taskResult{}, nil
	}

	if in.Status != "" && !containsStr(model.WritableTaskStatuses, in.Status) {
		return toolError("status %q is not valid; use one of: %s",
			in.Status, strings.Join(model.WritableTaskStatuses, ", ")), taskResult{}, nil
	}

	if in.Priority != "" && !containsStr(model.TaskPriorities, in.Priority) {
		return toolError("priority %q is not valid; use one of: %s",
			in.Priority, strings.Join(model.TaskPriorities, ", ")), taskResult{}, nil
	}

	if in.DaySlot != "" && !containsStr(model.DaySlots, in.DaySlot) {
		return toolError("day_slot %q is not valid; use one of: %s",
			in.DaySlot, strings.Join(model.DaySlots, ", ")), taskResult{}, nil
	}

	if in.SlotOrder != nil && *in.SlotOrder < 0 {
		return toolError("slot_order must be zero or greater"), taskResult{}, nil
	}

	params := store.TaskUpdate{
		Title:     setIf(in.Title),
		Details:   setIf(in.Details),
		Status:    setIf(in.Status),
		ContextID: setIf(in.ContextID),
		ProjectID: setIf(in.ProjectID),
	}

	// A value sets the field; the matching clear flag nulls it. Two inputs rather
	// than one because JSON has no way to say "absent" in a flat schema a model
	// can reliably fill in.
	params.DueOn = dateField(in.DueOn, in.ClearDueOn)
	params.PlannedOn = dateField(in.PlannedOn, in.ClearPlannedOn)
	switch {
	case in.ClearPlannedOn || in.ClearDaySlot:
		params.DaySlot = patch.Field[string]{Set: true, Null: true}
	case in.DaySlot != "":
		params.DaySlot = patch.Field[string]{Set: true, Value: in.DaySlot}
	}

	if in.SlotOrder != nil {
		params.SlotOrder = patch.Field[int64]{Set: true, Value: *in.SlotOrder}
	}
	switch {
	case in.ClearPriority:
		params.Priority = patch.Field[string]{Set: true, Null: true}
	case in.Priority != "":
		params.Priority = patch.Field[string]{Set: true, Value: in.Priority}
	}

	if in.EstimateMinutes > 0 {
		params.EstimateMinutes = patch.Field[int64]{Set: true, Value: in.EstimateMinutes}
	}

	switch {
	case in.ClearBlocker:
		params.BlockedByID = patch.Field[string]{Set: true, Null: true}

		if in.Status == "" {
			params.Status = patch.Field[string]{Set: true, Value: model.StatusTodo}
		}

	case in.BlockedByID != "":
		params.BlockedByID = patch.Field[string]{Set: true, Value: in.BlockedByID}

		if in.Status == "" {
			params.Status = patch.Field[string]{Set: true, Value: model.StatusBlocked}
		}
	}

	task, err := h.store.UpdateTask(ctx, userID, in.TaskID, params)
	if err != nil {
		return h.storeToolError(err), taskResult{}, nil
	}

	return h.taskOutcome(ctx, userID, task, "Updated")
}

type completeTaskInput struct {
	TaskID string `json:"task_id" jsonschema:"the task to mark done"`
}

func (h *Handler) completeTask(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in completeTaskInput,
) (*mcp.CallToolResult, taskResult, error) {
	userID, _, scopes, err := caller(ctx)
	if err != nil {
		return nil, taskResult{}, err
	}

	if err := requireWrite(scopes); err != nil {
		return toolError("%v", err), taskResult{}, nil
	}

	if in.TaskID == "" {
		return toolError("task_id is required"), taskResult{}, nil
	}

	task, err := h.store.UpdateTask(ctx, userID, in.TaskID, store.TaskUpdate{
		Status: patch.Field[string]{Set: true, Value: model.StatusDone},
	})
	if err != nil {
		return h.storeToolError(err), taskResult{}, nil
	}

	return h.taskOutcome(ctx, userID, task, "Completed")
}

type cancelTaskInput struct {
	TaskID string `json:"task_id" jsonschema:"the task to cancel"`
}

// cancelTask closes a task as not-doing.
//
// Its own tool rather than update_task with a status, for the same reason
// complete_task is: "I'm not going to do that" is a distinct and common
// instruction, and a model reaches for a named verb far more reliably than for a
// status enum it has to remember the spelling of.
func (h *Handler) cancelTask(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in cancelTaskInput,
) (*mcp.CallToolResult, taskResult, error) {
	userID, _, scopes, err := caller(ctx)
	if err != nil {
		return nil, taskResult{}, err
	}

	if err := requireWrite(scopes); err != nil {
		return toolError("%v", err), taskResult{}, nil
	}

	if in.TaskID == "" {
		return toolError("task_id is required"), taskResult{}, nil
	}

	task, err := h.store.UpdateTask(ctx, userID, in.TaskID, store.TaskUpdate{
		Status: patch.Field[string]{Set: true, Value: model.StatusCancelled},
	})
	if err != nil {
		return h.storeToolError(err), taskResult{}, nil
	}

	return h.taskOutcome(ctx, userID, task, "Cancelled")
}

type deleteTaskInput struct {
	TaskID string `json:"task_id" jsonschema:"the task to delete, along with its subtasks"`
}

type deleteTaskOutput struct {
	Deleted bool   `json:"deleted"`
	TaskID  string `json:"task_id"`
}

func (h *Handler) deleteTask(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in deleteTaskInput,
) (*mcp.CallToolResult, deleteTaskOutput, error) {
	userID, _, scopes, err := caller(ctx)
	if err != nil {
		return nil, deleteTaskOutput{}, err
	}

	if err := requireWrite(scopes); err != nil {
		return toolError("%v", err), deleteTaskOutput{}, nil
	}

	if in.TaskID == "" {
		return toolError("task_id is required"), deleteTaskOutput{}, nil
	}

	if err := h.store.DeleteTask(ctx, userID, in.TaskID); err != nil {
		return h.storeToolError(err), deleteTaskOutput{}, nil
	}

	return &mcp.CallToolResult{Content: summary("Deleted task " + in.TaskID)},
		deleteTaskOutput{Deleted: true, TaskID: in.TaskID}, nil
}

type delegateTaskInput struct {
	TaskID string `json:"task_id" jsonschema:"the task to delegate"`
	Person string `json:"person" jsonschema:"the name of the person to delegate to; created if new"`
}

func (h *Handler) delegateTask(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in delegateTaskInput,
) (*mcp.CallToolResult, taskResult, error) {
	userID, _, scopes, err := caller(ctx)
	if err != nil {
		return nil, taskResult{}, err
	}

	if err := requireWrite(scopes); err != nil {
		return toolError("%v", err), taskResult{}, nil
	}

	if in.TaskID == "" || strings.TrimSpace(in.Person) == "" {
		return toolError("task_id and person are both required"), taskResult{}, nil
	}

	person, err := h.store.FindOrCreatePerson(ctx, userID, in.Person)
	if err != nil {
		return h.storeToolError(err), taskResult{}, nil
	}

	// Delegate and status move together: the schema forbids a delegated task with
	// no delegate, so setting one without the other would be rejected.
	task, err := h.store.UpdateTask(ctx, userID, in.TaskID, store.TaskUpdate{
		DelegatedToID: patch.Field[string]{Set: true, Value: person.ID},
		Status:        patch.Field[string]{Set: true, Value: model.StatusDelegated},
	})
	if err != nil {
		return h.storeToolError(err), taskResult{}, nil
	}

	return h.taskOutcome(ctx, userID, task, "Delegated to "+person.Name+":")
}

type triageTaskInput struct {
	TaskID    string `json:"task_id" jsonschema:"the inbox task to triage"`
	ContextID string `json:"context_id" jsonschema:"the context to file it under"`
	ProjectID string `json:"project_id,omitempty" jsonschema:"a project in that context"`
	PlannedOn string `json:"planned_on,omitempty" jsonschema:"YYYY-MM-DD day to work on it"`
	DueOn     string `json:"due_on,omitempty" jsonschema:"YYYY-MM-DD deadline"`
}

func (h *Handler) triageTask(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in triageTaskInput,
) (*mcp.CallToolResult, taskResult, error) {
	userID, _, scopes, err := caller(ctx)
	if err != nil {
		return nil, taskResult{}, err
	}

	if err := requireWrite(scopes); err != nil {
		return toolError("%v", err), taskResult{}, nil
	}

	if in.TaskID == "" || in.ContextID == "" {
		return toolError("task_id and context_id are both required"), taskResult{}, nil
	}

	if bad := invalidDates(map[string]string{"due_on": in.DueOn, "planned_on": in.PlannedOn}); bad != "" {
		return toolError("%s", bad), taskResult{}, nil
	}

	params := store.TaskUpdate{
		ContextID: patch.Field[string]{Set: true, Value: in.ContextID},
		ProjectID: setIf(in.ProjectID),
		DueOn:     setIf(in.DueOn),
		PlannedOn: setIf(in.PlannedOn),
	}

	// Triage is what moves a capture out of the inbox, so the status moves with
	// it; leaving it as inbox would mean the task shows up as untriaged forever.
	current, err := h.store.GetTask(ctx, userID, in.TaskID)
	if err != nil {
		return h.storeToolError(err), taskResult{}, nil
	}

	if current.Status == model.StatusInbox {
		params.Status = patch.Field[string]{Set: true, Value: model.StatusTodo}
	}

	task, err := h.store.UpdateTask(ctx, userID, in.TaskID, params)
	if err != nil {
		return h.storeToolError(err), taskResult{}, nil
	}

	return h.taskOutcome(ctx, userID, task, "Triaged")
}

type createProjectInput struct {
	Name        string `json:"name" jsonschema:"the project name"`
	ContextID   string `json:"context_id" jsonschema:"the context it belongs to"`
	Description string `json:"description,omitempty"`
}

type createProjectOutput struct {
	Project projectView `json:"project"`
}

func (h *Handler) createProject(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in createProjectInput,
) (*mcp.CallToolResult, createProjectOutput, error) {
	userID, _, scopes, err := caller(ctx)
	if err != nil {
		return nil, createProjectOutput{}, err
	}

	if err := requireWrite(scopes); err != nil {
		return toolError("%v", err), createProjectOutput{}, nil
	}

	if strings.TrimSpace(in.Name) == "" || in.ContextID == "" {
		return toolError("name and context_id are both required"), createProjectOutput{}, nil
	}

	project, err := h.store.CreateProject(ctx, userID, store.ProjectCreate{
		ContextID:   in.ContextID,
		Name:        strings.TrimSpace(in.Name),
		Description: optional(in.Description),
	})
	if err != nil {
		return h.storeToolError(err), createProjectOutput{}, nil
	}

	l, err := h.loadLookups(ctx, userID)
	if err != nil {
		return nil, createProjectOutput{}, err
	}

	out := createProjectOutput{Project: projectView{
		ID:          project.ID,
		Name:        project.Name,
		ContextID:   project.ContextID,
		ContextName: l.contexts[project.ContextID],
		Status:      project.Status,
	}}

	return &mcp.CallToolResult{Content: summary("Created project " + project.Name)}, out, nil
}

type createRecurrenceInput struct {
	Title           string `json:"title" jsonschema:"what recurs"`
	ContextID       string `json:"context_id" jsonschema:"the context the occurrences belong to"`
	RRule           string `json:"rrule" jsonschema:"an RFC 5545 rule such as FREQ=DAILY or FREQ=WEEKLY;BYDAY=MO"`
	StartsOn        string `json:"starts_on,omitempty" jsonschema:"YYYY-MM-DD first date to consider; defaults to today"`
	EndsOn          string `json:"ends_on,omitempty" jsonschema:"YYYY-MM-DD last date, optional"`
	Details         string `json:"details,omitempty"`
	ProjectID       string `json:"project_id,omitempty"`
	EstimateMinutes int64  `json:"estimate_minutes,omitempty"`
	LeadDays        int64  `json:"lead_days,omitempty" jsonschema:"how many days before its date each occurrence should appear"`
	Timezone        string `json:"timezone,omitempty" jsonschema:"IANA timezone deciding the day boundary; defaults to the user's"`
}

type createRecurrenceOutput struct {
	Recurrence recurrenceView `json:"recurrence"`
	Spawned    int            `json:"spawned" jsonschema:"how many occurrences were created immediately"`
}

func (h *Handler) createRecurrence(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in createRecurrenceInput,
) (*mcp.CallToolResult, createRecurrenceOutput, error) {
	userID, timezone, scopes, err := caller(ctx)
	if err != nil {
		return nil, createRecurrenceOutput{}, err
	}

	if err := requireWrite(scopes); err != nil {
		return toolError("%v", err), createRecurrenceOutput{}, nil
	}

	if strings.TrimSpace(in.Title) == "" || in.ContextID == "" || strings.TrimSpace(in.RRule) == "" {
		return toolError("title, context_id and rrule are all required"),
			createRecurrenceOutput{}, nil
	}

	zone := in.Timezone
	if zone == "" {
		zone = timezone
	}

	location, err := time.LoadLocation(zone)
	if err != nil {
		return toolError("timezone %q is not a known IANA timezone", zone),
			createRecurrenceOutput{}, nil
	}

	startsOn := in.StartsOn
	if startsOn == "" {
		startsOn = time.Now().In(location).Format(database.DateOnly)
	}

	if bad := invalidDates(map[string]string{"starts_on": startsOn, "ends_on": in.EndsOn}); bad != "" {
		return toolError("%s", bad), createRecurrenceOutput{}, nil
	}

	params := store.RecurrenceCreate{
		ContextID: in.ContextID,
		Title:     strings.TrimSpace(in.Title),
		RRule:     strings.TrimSpace(in.RRule),
		Timezone:  location.String(),
		StartsOn:  startsOn,
		EndsOn:    optional(in.EndsOn),
		Details:   optional(in.Details),
		ProjectID: optional(in.ProjectID),
	}

	if in.EstimateMinutes > 0 {
		estimate := in.EstimateMinutes
		params.EstimateMinutes = &estimate
	}

	if in.LeadDays > 0 {
		lead := in.LeadDays
		params.LeadDays = &lead
	}

	created, err := h.store.CreateRecurrence(ctx, userID, params)
	if err != nil {
		return h.storeToolError(err), createRecurrenceOutput{}, nil
	}

	// Materialize straight away, so the model can tell the user what appeared
	// rather than promising something for later.
	var spawned int

	if h.spawner != nil {
		if result, err := h.spawner.RunTemplate(ctx, userID, created.ID); err == nil {
			spawned = result.Created
		} else {
			h.log.Warn("mcp: could not spawn occurrences",
				slog.String("recurrence_id", created.ID), slog.Any("error", err))
		}
	}

	refreshed, err := h.store.GetRecurrence(ctx, userID, created.ID)
	if err != nil {
		return nil, createRecurrenceOutput{}, err
	}

	l, err := h.loadLookups(ctx, userID)
	if err != nil {
		return nil, createRecurrenceOutput{}, err
	}

	view := recurrenceView{
		ID:          refreshed.ID,
		Title:       refreshed.Title,
		RRule:       refreshed.RRule,
		ContextID:   refreshed.ContextID,
		ContextName: l.contexts[refreshed.ContextID],
		Timezone:    refreshed.Timezone,
		State:       refreshed.State,
	}

	if refreshed.NextOccurrenceOn != nil {
		view.NextOccurrenceOn = *refreshed.NextOccurrenceOn
	}

	return &mcp.CallToolResult{
		Content: summary(fmt.Sprintf("Created recurring task %q; %d occurrence(s) created now",
			refreshed.Title, spawned)),
	}, createRecurrenceOutput{Recurrence: view, Spawned: spawned}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// sourceKeys mirrors the seeded sources table.
var sourceKeys = []string{"self", "email", "slack", "google_chat", "meeting", "phone"}

// taskOutcome renders a mutated task consistently across the write tools.
func (h *Handler) taskOutcome(
	ctx context.Context,
	userID string,
	task model.Task,
	verb string,
) (*mcp.CallToolResult, taskResult, error) {
	l, err := h.loadLookups(ctx, userID)
	if err != nil {
		return nil, taskResult{}, err
	}

	view := toView(task, l)

	text := fmt.Sprintf("%s %q [%s]", verb, view.Title, view.Status)
	if view.ContextName != "" {
		text += " in " + view.ContextName
	} else {
		text += " in the inbox"
	}

	if view.DueOn != "" {
		text += ", due " + view.DueOn
	}

	return &mcp.CallToolResult{Content: summary(text)}, taskResult{Task: view}, nil
}

// storeToolError turns a store failure into feedback a model can act on.
//
// Validation and ownership failures become tool errors rather than protocol
// errors, because they describe something the model got wrong and can retry
// differently. An unexpected failure is logged and reported without detail, since
// internals are not the model's business.
func (h *Handler) storeToolError(err error) *mcp.CallToolResult {
	var (
		invalidRef *store.InvalidRefError
		conflict   *store.ConflictError
	)

	switch {
	case errors.Is(err, store.ErrNotFound):
		return toolError("not found, or it belongs to someone else")

	case errors.As(err, &invalidRef):
		return toolError("%s %q does not exist; list the available ones first",
			invalidRef.Field, invalidRef.ID)

	case errors.As(err, &conflict):
		return toolError("%s %s", conflict.Field, conflict.Detail)

	default:
		h.log.Error("mcp: tool failed", slog.Any("error", err))

		return toolError("the request could not be completed")
	}
}

// optional turns an empty string into a nil pointer, which is how the store
// distinguishes "not given" from "given as empty".
func optional(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	trimmed := strings.TrimSpace(value)

	return &trimmed
}

// setIf builds a patch field that is set only when a value was supplied.
func setIf(value string) patch.Field[string] {
	if strings.TrimSpace(value) == "" {
		return patch.Field[string]{}
	}

	return patch.Field[string]{Set: true, Value: strings.TrimSpace(value)}
}

// dateField resolves a value plus a clear flag into a patch field. Clearing wins,
// since asking to both set and clear is contradictory and removing is the safer
// reading.
func dateField(value string, clear bool) patch.Field[string] {
	if clear {
		return patch.Field[string]{Set: true, Null: true}
	}

	return setIf(value)
}

// invalidDates returns a message naming the first field that is not a real date.
//
// Checked here as well as in the store so a model gets "planned_on is not a valid
// date" rather than a constraint violation it cannot interpret.
func invalidDates(fields map[string]string) string {
	for field, value := range fields {
		if value == "" {
			continue
		}

		if _, err := time.Parse(database.DateOnly, value); err != nil {
			return fmt.Sprintf("%s %q is not a valid YYYY-MM-DD date", field, value)
		}
	}

	return ""
}
