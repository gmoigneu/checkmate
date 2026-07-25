// Package model holds the domain types shared by the store and the HTTP layer.
//
// Dates and timestamps are strings, not time.Time. They are stored as TEXT in
// exactly the format the API speaks (RFC3339 UTC for timestamps, YYYY-MM-DD for
// calendar dates), so keeping them as strings removes a parse/format round trip
// on every read and, more usefully, removes any chance of a timezone shifting a
// due date by a day.
package model

// Source is where a task came from (brief section A).
type Source struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	SortOrder int64  `json:"sort_order"`
}

// Context is a top-level bucket: Upsun, Personal, Gaal, Arkea.
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
	ContextID        string  `json:"context_id"`
	ProjectID        *string `json:"project_id"`
	Source           *string `json:"source"`
	Title            string  `json:"title"`
	Details          *string `json:"details"`
	RRule            string  `json:"rrule"`
	Timezone         string  `json:"timezone"`
	EstimateMinutes  *int64  `json:"estimate_minutes"`
	DelegatedToID    *string `json:"delegated_to_id"`
	LeadDays         int64   `json:"lead_days"`
	StartsOn         string  `json:"starts_on"`
	EndsOn           *string `json:"ends_on"`
	NextOccurrenceOn *string `json:"next_occurrence_on"`
	LastSpawnedOn    *string `json:"last_spawned_on"`
	Active           bool    `json:"active"`
	CreatedAt        string  `json:"created_at"`
	UpdatedAt        string  `json:"updated_at"`
	DeletedAt        *string `json:"deleted_at,omitempty"`
	Rev              int64   `json:"rev"`
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
	DueOn           *string `json:"due_on"`
	PlannedOn       *string `json:"planned_on"`
	EstimateMinutes *int64  `json:"estimate_minutes"`
	DelegatedToID   *string `json:"delegated_to_id"`
	BlockedByID     *string `json:"blocked_by_id"`
	ReferenceURL    *string `json:"reference_url"`
	ReferenceLabel  *string `json:"reference_label"`

	// Kind is derived by the tasks_with_kind view, never stored.
	Kind string `json:"kind"`

	CompletedAt *string `json:"completed_at"`
	CancelledAt *string `json:"cancelled_at"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
	DeletedAt   *string `json:"deleted_at,omitempty"`
	Rev         int64   `json:"rev"`
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
)

// TaskStatuses is every legal task status, matching the CHECK constraint.
var TaskStatuses = []string{
	StatusInbox, StatusTodo, StatusInProgress, StatusBlocked,
	StatusDelegated, StatusDone, StatusCancelled,
}

// CaptureMethods is every legal capture method, matching the CHECK constraint.
var CaptureMethods = []string{
	"form", "api", "hermes", "chrome_ext", "ios_widget", "voice", "recurrence",
}

// ProjectStatuses is every legal project status, matching the CHECK constraint.
var ProjectStatuses = []string{"active", "paused", "done", "archived"}

// TaskKinds is every value the tasks_with_kind view can derive.
var TaskKinds = []string{"short", "long", "recurring", "delegated", "blocked"}

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
}

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
