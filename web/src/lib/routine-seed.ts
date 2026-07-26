import type { DaySlot } from "./types";

export interface RoutineSeedItem {
	title: string;
	slot: DaySlot;
	context: "Work" | "Personal" | "Health";
	days: string[];
}

const everyDay = ["MO", "TU", "WE", "TH", "FR", "SA", "SU"];

// Transcribed from the handwritten routine supplied with the feature.
export const exampleRoutineSeed: RoutineSeedItem[] = [
	{
		title: "Bed & shower",
		slot: "morning",
		context: "Personal",
		days: everyDay,
	},
	{
		title: "Review email & Slack, then capture tasks",
		slot: "morning",
		context: "Work",
		days: everyDay,
	},
	{
		title: "Plan the day",
		slot: "morning",
		context: "Personal",
		days: everyDay,
	},
	{
		title: "Prepare meetings",
		slot: "morning",
		context: "Work",
		days: everyDay,
	},
	{
		title: "Intelligence brief",
		slot: "morning",
		context: "Work",
		days: everyDay,
	},
	{
		title: "Gym, lunch & machines",
		slot: "midday",
		context: "Health",
		days: everyDay,
	},
	{
		title: "Review tasks & progress",
		slot: "midday",
		context: "Work",
		days: everyDay,
	},
	{
		title: "Review tasks & prepare the next day",
		slot: "evening",
		context: "Work",
		days: everyDay,
	},
	{
		title: "Journal & recap in-progress and completed work",
		slot: "evening",
		context: "Personal",
		days: everyDay,
	},
	{
		title: "Read & learn",
		slot: "evening",
		context: "Personal",
		days: everyDay,
	},
	{
		title: "Pickleball",
		slot: "evening",
		context: "Health",
		days: ["TU", "TH", "FR", "SA"],
	},
];
