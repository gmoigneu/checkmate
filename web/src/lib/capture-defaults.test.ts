import assert from "node:assert/strict";
import test from "node:test";
import { captureDefaultsForPath } from "./capture-defaults.ts";

const contexts = [
	{ id: "context-work", slug: "work" },
	{ id: "context-home", slug: "home-life" },
];
const projects = [
	{ id: "project-launch", context_id: "context-work" },
	{ id: "project-garden", context_id: "context-home" },
];

test("defaults quick capture to the current context page", () => {
	assert.deepEqual(captureDefaultsForPath("/c/home-life", contexts, projects), {
		contextId: "context-home",
	});
});

test("defaults quick capture to the current project and its context", () => {
	assert.deepEqual(
		captureDefaultsForPath("/p/project-launch", contexts, projects),
		{
			contextId: "context-work",
			projectId: "project-launch",
		},
	);
});

test("does not add page defaults outside context and project pages", () => {
	assert.deepEqual(captureDefaultsForPath("/inbox", contexts, projects), {});
});
