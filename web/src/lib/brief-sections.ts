import type { Brief, Task, WaitingGroup } from "./types";

export type BriefDisplayTask = Task & { alsoIn?: string[] };

export interface BriefSections {
	overdue: BriefDisplayTask[];
	dueToday: BriefDisplayTask[];
	planned: BriefDisplayTask[];
	inProgress: BriefDisplayTask[];
	waitingOn: Array<Omit<WaitingGroup, "tasks"> & { tasks: BriefDisplayTask[] }>;
	blocked: BriefDisplayTask[];
	inbox: BriefDisplayTask[];
	openTaskCount: number;
}

export function buildBriefSections(data: Brief): BriefSections {
	const orderedBuckets: Array<[string, Task[]]> = [
		["overdue", data.overdue],
		["due today", data.due_today],
		["planned", data.planned],
		["in progress", data.in_progress],
		["waiting on", data.waiting_on.flatMap((group) => group.tasks)],
		["blocked", data.blocked],
		["inbox", data.inbox],
	];
	const memberships = new Map<string, string[]>();
	const openTaskIds = new Set<string>();
	for (const [label, tasks] of orderedBuckets) {
		for (const task of tasks) {
			const labels = memberships.get(task.id) ?? [];
			labels.push(label);
			memberships.set(task.id, labels);
			openTaskIds.add(task.id);
		}
	}
	const decorate = (tasks: Task[], bucket: string): BriefDisplayTask[] =>
		tasks.map((task) => ({
			...task,
			alsoIn: (memberships.get(task.id) ?? []).filter(
				(label) => label !== bucket,
			),
		}));

	return {
		overdue: decorate(data.overdue, "overdue"),
		dueToday: decorate(data.due_today, "due today"),
		planned: decorate(data.planned, "planned"),
		inProgress: decorate(data.in_progress, "in progress"),
		waitingOn: data.waiting_on.map((group) => ({
			...group,
			tasks: decorate(group.tasks, "waiting on"),
		})),
		blocked: decorate(data.blocked, "blocked"),
		inbox: decorate(data.inbox, "inbox"),
		openTaskCount: openTaskIds.size,
	};
}
