import assert from "node:assert/strict";
import test from "node:test";
import { buildBriefSections } from "./brief-sections.ts";
import type { Brief, Task } from "./types.ts";

function task(overrides: Partial<Task> = {}): Task {
	return {
		id: "task-1",
		kind: "one_off",
		context_id: "context-1",
		project_id: null,
		parent_id: null,
		source: null,
		title: "Prepare 1:1",
		details: null,
		status: "in_progress",
		priority: "high",
		due_on: "2026-07-27",
		planned_on: null,
		day_slot: null,
		slot_order: 0,
		estimate_minutes: 30,
		delegated_to_id: null,
		blocked_by_id: null,
		recurrence_id: null,
		occurrence_on: null,
		completed_at: null,
		cancelled_at: null,
		expired_at: null,
		reference_url: null,
		reference_label: null,
		capture_method: "form",
		created_at: "2026-07-27T08:00:00Z",
		updated_at: "2026-07-27T09:00:00Z",
		deleted_at: null,
		...overrides,
	};
}

function brief(overrides: Partial<Brief> = {}): Brief {
	return {
		date: "2026-07-27",
		overdue: [],
		due_today: [],
		planned: [],
		in_progress: [],
		inbox: [],
		blocked: [],
		waiting_on: [],
		routine: [],
		completed_today: [],
		totals: {
			overdue: 0,
			due_today: 0,
			planned: 0,
			inbox: 0,
			blocked: 0,
			waiting_on: 0,
			in_progress: 0,
			completed_today: 0,
			routine: 0,
			routine_open: 0,
			routine_done: 0,
			routine_expired: 0,
			planned_minutes: 0,
			planned_without_estimate: 0,
		},
		...overrides,
	};
}

test("an in-progress task due today appears in both Brief sections", () => {
	const overlappingTask = task();
	const sections = buildBriefSections(
		brief({
			due_today: [overlappingTask],
			in_progress: [overlappingTask],
		}),
	);

	assert.deepEqual(
		sections.dueToday.map((item) => item.id),
		["task-1"],
	);
	assert.deepEqual(
		sections.inProgress.map((item) => item.id),
		["task-1"],
	);
	assert.equal(sections.openTaskCount, 1);
});
