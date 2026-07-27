export type TaskStatus =
	| "inbox"
	| "todo"
	| "in_progress"
	| "blocked"
	| "delegated"
	| "done"
	| "cancelled"
	| "expired";

export type TaskKind =
	| "short"
	| "long"
	| "blocked"
	| "delegated"
	| "recurring"
	| "routine";
export type TaskPriority = "urgent" | "high" | "medium" | "low";
export type DaySlot = "morning" | "midday" | "afternoon" | "evening" | "night";
export type ProjectStatus = "active" | "paused" | "done" | "archived";

export interface Context {
	id: string;
	name: string;
	slug: string;
	color: string | null;
	sort_order: number;
	archived_at: string | null;
}

export interface Project {
	id: string;
	context_id: string;
	name: string;
	description: string | null;
	status: ProjectStatus;
}

export interface Person {
	id: string;
	name: string;
	email: string | null;
	context_id: string | null;
}

export interface Task {
	id: string;
	context_id: string | null;
	project_id: string | null;
	parent_id: string | null;
	recurrence_id: string | null;
	occurrence_on: string | null;
	source: string | null;
	capture_method: string;
	title: string;
	details: string | null;
	status: TaskStatus;
	priority: TaskPriority | null;
	due_on: string | null;
	planned_on: string | null;
	day_slot: DaySlot | null;
	slot_order: number;
	estimate_minutes: number | null;
	delegated_to_id: string | null;
	delegated_to_name?: string | null;
	blocked_by_id: string | null;
	reference_url: string | null;
	reference_label: string | null;
	kind: TaskKind;
	completed_at: string | null;
	cancelled_at: string | null;
	expired_at: string | null;
	created_at: string;
	updated_at: string;
	deleted_at: string | null;
}

export interface Recurrence {
	id: string;
	kind: "classic" | "routine";
	context_id: string;
	project_id: string | null;
	source: string | null;
	title: string;
	details: string | null;
	day_slot: DaySlot | null;
	slot_order: number;
	rrule: string;
	timezone: string;
	estimate_minutes: number | null;
	delegated_to_id: string | null;
	lead_days: number;
	starts_on: string;
	ends_on: string | null;
	next_occurrence_on: string | null;
	last_spawned_on: string | null;
	active: boolean;
	state: "active" | "paused" | "finished";
	completed_at: string | null;
	created_at: string;
	updated_at: string;
	deleted_at: string | null;
}

export interface WaitingGroup {
	person_id: string;
	person_name: string;
	tasks: Task[];
}

export interface BriefTotals {
	overdue: number;
	due_today: number;
	planned: number;
	inbox: number;
	blocked: number;
	waiting_on: number;
	in_progress: number;
	completed_today: number;
	cancelled_today?: number;
	routine: number;
	routine_open: number;
	routine_done: number;
	routine_expired: number;
	planned_minutes: number;
	planned_without_estimate: number;
}

export interface Brief {
	date: string;
	overdue: Task[];
	due_today: Task[];
	planned: Task[];
	in_progress: Task[];
	inbox: Task[];
	blocked: Task[];
	waiting_on: WaitingGroup[];
	routine: Task[];
	completed_today: Task[];
	cancelled_today?: Task[];
	totals: BriefTotals;
}

export interface Collection<T> {
	data: T[];
	next_cursor: string | null;
}

export interface Me {
	user_id: string;
	email: string;
	name: string;
	timezone: string;
	auth_via: string;
	scopes: string[];
}

export interface ApiErrorPayload {
	error: string;
	fields?: Record<string, string>;
}
