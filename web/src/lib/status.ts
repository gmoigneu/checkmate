import {
	ArrowRight,
	Check,
	CircleDotDashed,
	Inbox,
	LoaderCircle,
	type LucideIcon,
	OctagonX,
	X,
} from "lucide-react";
import type { TaskStatus } from "./types";

export const taskStatusOptions: Record<
	TaskStatus,
	{ label: string; icon: LucideIcon }
> = {
	inbox: { label: "Inbox", icon: Inbox },
	todo: { label: "To do", icon: CircleDotDashed },
	in_progress: { label: "In progress", icon: LoaderCircle },
	blocked: { label: "Blocked", icon: OctagonX },
	delegated: { label: "Waiting on", icon: ArrowRight },
	done: { label: "Done", icon: Check },
	cancelled: { label: "Cancelled", icon: X },
};

export const taskStatusEntries = Object.entries(taskStatusOptions) as Array<
	[TaskStatus, (typeof taskStatusOptions)[TaskStatus]]
>;

export function taskStatusValue(status: TaskStatus | string): TaskStatus {
	return Object.hasOwn(taskStatusOptions, status)
		? (status as TaskStatus)
		: "todo";
}

export function taskStatusOption(status: TaskStatus | string) {
	return taskStatusOptions[taskStatusValue(status)];
}

export const taskListStatusFilters: TaskStatus[] = [
	"blocked",
	"delegated",
	"done",
	"cancelled",
];
