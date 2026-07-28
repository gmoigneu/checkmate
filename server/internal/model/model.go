// Package model holds the domain types shared by the store and the HTTP layer.
//
// Dates and timestamps are strings, not time.Time. They are stored as TEXT in
// exactly the format the API speaks (RFC3339 UTC for timestamps, YYYY-MM-DD for
// calendar dates), so keeping them as strings removes a parse/format round trip
// on every read and, more usefully, removes any chance of a timezone shifting a
// due date by a day.
package model

import "time"

// Source is where a task came from (brief section A).
type Source struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	SortOrder int64  `json:"sort_order"`
}

// Context is a top-level bucket for a user's work.
type Context struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Slug       string  `json:"slug"`
	Color      *string `json:"color"`
	SortOrder  int64   `json:"sort_order"`
	ArchivedAt *string `json:"archived_at"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
	DeletedAt  *string `json:"deleted_at,omitempty"`
	Rev        int64   `json:"rev"`
}

// Project is an optional grouping inside exactly one context.
type Project struct {
	ID          string  `json:"id"`
	ContextID   string  `json:"context_id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Status      string  `json:"status"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
	DeletedAt   *string `json:"deleted_at,omitempty"`
	Rev         int64   `json:"rev"`
}

// Person is a delegation target or follow-up counterparty.
type Person struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Email     *string `json:"email"`
	ContextID *string `json:"context_id"`
	Notes     *string `json:"notes"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
	DeletedAt *string `json:"deleted_at,omitempty"`
	Rev       int64   `json:"rev"`
}

// Recurrence is the template a repeating task is spawned from.
type Recurrence struct {
	ID               string  `json:"id"`
	Kind             string  `json:"kind"`
	ContextID        string  `json:"context_id"`
	ProjectID        *string `json:"project_id"`
	Source           *string `json:"source"`
	Title            string  `json:"title"`
	Details          *string `json:"details"`
	DaySlot          *string `json:"day_slot"`
	SlotOrder        int64   `json:"slot_order"`
	RRule            string  `json:"rrule"`
	Timezone         string  `json:"timezone"`
	EstimateMinutes  *int64  `json:"estimate_minutes"`
	DelegatedToID    *string `json:"delegated_to_id"`
	LeadDays         int64   `json:"lead_days"`
	StartsOn         string  `json:"starts_on"`
	EndsOn           *string `json:"ends_on"`
	NextOccurrenceOn *string `json:"next_occurrence_on"`
	LastSpawnedOn    *string `json:"last_spawned_on"`

	// Active is the operational flag the spawner queries. It is false both for a
	// series a person paused and for one that ran out, which is why State exists.
	Active bool `json:"active"`

	// CompletedAt is set by the spawner when it retires a series, and only then.
	// Pausing leaves it null.
	CompletedAt *string `json:"completed_at"`

	// State is derived from Active and CompletedAt, never stored and never
	// writable: "active", "paused" (a person turned it off) or "finished" (it
	// reached its end date or exhausted a COUNT). Resuming a finished series only
	// makes sense alongside changing the rule, so a UI can say so.
	State     string  `json:"state"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
	DeletedAt *string `json:"deleted_at,omitempty"`
	Rev       int64   `json:"rev"`
}

// Task is the core entity (brief section C).
type Task struct {
	ID              string  `json:"id"`
	ContextID       *string `json:"context_id"`
	ProjectID       *string `json:"project_id"`
	ParentID        *string `json:"parent_id"`
	RecurrenceID    *string `json:"recurrence_id"`
	OccurrenceOn    *string `json:"occurrence_on"`
	Source          *string `json:"source"`
	CaptureMethod   string  `json:"capture_method"`
	Title           string  `json:"title"`
	Details         *string `json:"details"`
	Status          string  `json:"status"`
	Priority        *string `json:"priority"`
	DueOn           *string `json:"due_on"`
	PlannedOn       *string `json:"planned_on"`
	DaySlot         *string `json:"day_slot"`
	SlotOrder       int64   `json:"slot_order"`
	EstimateMinutes *int64  `json:"estimate_minutes"`
	DelegatedToID   *string `json:"delegated_to_id"`
	BlockedByID     *string `json:"blocked_by_id"`
	ReferenceURL    *string `json:"reference_url"`
	ReferenceLabel  *string `json:"reference_label"`

	// Kind is derived by the tasks_with_kind view, never stored.
	Kind string `json:"kind"`

	CompletedAt *string `json:"completed_at"`
	CancelledAt *string `json:"cancelled_at"`
	ExpiredAt   *string `json:"expired_at"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
	DeletedAt   *string `json:"deleted_at,omitempty"`
	Rev         int64   `json:"rev"`
}

// TaskActivity is one immutable record of a task mutation.
type TaskActivity struct {
	ID            int64    `json:"id"`
	TaskID        string   `json:"task_id"`
	TaskTitle     string   `json:"task_title"`
	Action        string   `json:"action"`
	ChangedFields []string `json:"changed_fields"`
	StatusBefore  *string  `json:"status_before"`
	StatusAfter   *string  `json:"status_after"`
	OccurredAt    string   `json:"occurred_at"`
}

// Task statuses.
const (
	StatusInbox      = "inbox"
	StatusTodo       = "todo"
	StatusInProgress = "in_progress"
	StatusBlocked    = "blocked"
	StatusDelegated  = "delegated"
	StatusDone       = "done"
	StatusCancelled  = "cancelled"
	StatusExpired    = "expired"
)

// TaskStatuses is every status exposed by the API. Expired is normalized from
// expired_at while the persisted status remains cancelled.
var TaskStatuses = []string{
	StatusInbox, StatusTodo, StatusInProgress, StatusBlocked,
	StatusDelegated, StatusDone, StatusCancelled,
	StatusExpired,
}

// WritableTaskStatuses excludes expired, which is a terminal outcome written
// only by the routine lifecycle.
var WritableTaskStatuses = []string{
	StatusInbox, StatusTodo, StatusInProgress, StatusBlocked,
	StatusDelegated, StatusDone, StatusCancelled,
}

// Task priorities, from most to least important. A nil priority means the task
// has not been prioritized.
const (
	PriorityUrgent = "urgent"
	PriorityHigh   = "high"
	PriorityMedium = "medium"
	PriorityLow    = "low"
)

// TaskPriorities is every legal non-null task priority, matching the CHECK
// constraint and ordered the same way as the default task listing.
var TaskPriorities = []string{
	PriorityUrgent, PriorityHigh, PriorityMedium, PriorityLow,
}

// CaptureMethods is every legal capture method, matching the CHECK constraint.
var CaptureMethods = []string{
	"form", "api", "hermes", "chrome_ext", "ios_widget", "voice", "recurrence",
}

// ProjectStatuses is every legal project status, matching the CHECK constraint.
var ProjectStatuses = []string{"active", "paused", "done", "archived"}

// TaskKinds is every value the tasks_with_kind view can derive.
var TaskKinds = []string{"short", "long", "recurring", "routine", "delegated", "blocked"}

// Day slots are fixed scheduling buckets in their display order.
const (
	DaySlotMorning   = "morning"
	DaySlotMidday    = "midday"
	DaySlotAfternoon = "afternoon"
	DaySlotEvening   = "evening"
	DaySlotNight     = "night"
)

var DaySlots = []string{
	DaySlotMorning, DaySlotMidday, DaySlotAfternoon, DaySlotEvening, DaySlotNight,
}

const (
	RecurrenceClassic = "classic"
	RecurrenceRoutine = "routine"
)

var RecurrenceKinds = []string{RecurrenceClassic, RecurrenceRoutine}

// Recurrence states, derived rather than stored.
const (
	RecurrenceActive   = "active"
	RecurrencePaused   = "paused"
	RecurrenceFinished = "finished"
)

// RecurrenceStates is every value State can take, and every value the API accepts
// as a filter.
var RecurrenceStates = []string{RecurrenceActive, RecurrencePaused, RecurrenceFinished}

// DeriveRecurrenceState resolves the stored pair into the state a person reads.
func DeriveRecurrenceState(active bool, completedAt *string) string {
	switch {
	case active:
		return RecurrenceActive
	case completedAt != nil && *completedAt != "":
		return RecurrenceFinished
	default:
		return RecurrencePaused
	}
}

// Identity is the authenticated caller behind a request.
//
// Exactly one of TokenID and SessionID is set, naming which credential kind was
// presented. Handlers only ever read UserID and Scopes; the distinction matters
// to the middleware, which applies CSRF checks to cookie-authenticated
// mutations but not to bearer-token ones.
type Identity struct {
	UserID    string
	TokenID   string
	SessionID string
	Scopes    []string
	Email     string
	Name      string
	Timezone  string

	// ClientID and Audience are set only for OAuth access tokens, naming the
	// client acting on the user's behalf and the resource the token was minted
	// for. Useful in logs: an OAuth request is attributable to a client, which a
	// device token is not.
	ClientID string
	Audience string

	// ExpiresAt is when the presented credential stops working.
	//
	// Every code path that authenticates already filters expiry in SQL, so this
	// is not load-bearing for access control. It exists because the MCP SDK's
	// bearer middleware requires a non-zero expiry, and a device token that
	// genuinely never expires still has to report something.
	ExpiresAt time.Time
}

// NeverExpires is the expiry reported for a credential with no expiry set, such
// as a device token created without one. Far enough out to be meaningless as a
// deadline, concrete enough to satisfy callers that require a value.
var NeverExpires = time.Now().AddDate(100, 0, 0)

// ViaCookie reports whether the caller authenticated with a session cookie.
func (i Identity) ViaCookie() bool { return i.SessionID != "" }

// HasScope reports whether the caller was granted scope.
func (i Identity) HasScope(scope string) bool {
	for _, s := range i.Scopes {
		if s == scope {
			return true
		}
	}

	return false
}
