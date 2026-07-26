import type { TaskStatus } from "./types";

export const taskStatusLabels: Record<TaskStatus, string> = {
	inbox: "Inbox",
	todo: "To do",
	in_progress: "In progress",
	blocked: "Blocked",
	delegated: "Waiting on",
	done: "Done",
	cancelled: "Cancelled",
};

export const taskListStatusFilters: TaskStatus[] = [
	"blocked",
	"delegated",
	"done",
	"cancelled",
];
