import type { Context, Person } from "./types";

export interface CaptureParse {
	title: string;
	context?: Context;
	person?: Person;
	source?: string;
	estimate_minutes?: number;
	due_on?: string;
	planned_on?: string;
	unresolved: string[];
}

function relativeDate(token: string) {
	const date = new Date();
	if (token === "today") return date;
	if (token === "tomorrow") return new Date(date.setDate(date.getDate() + 1));
	const inDays = token.match(/^in\s+(\d+)\s+days?$/);
	if (inDays) return new Date(date.setDate(date.getDate() + Number(inDays[1])));
	return undefined;
}

function dateString(date: Date) {
	return new Intl.DateTimeFormat("en-CA", {
		year: "numeric",
		month: "2-digit",
		day: "2-digit",
	}).format(date);
}

export function parseCapture(
	value: string,
	contexts: Context[],
	people: Person[],
): CaptureParse {
	let title = value.trim();
	const unresolved: string[] = [];
	const contextMatch = title.match(/(?:^|\s)#([\w-]+)/i);
	const personMatch = title.match(/(?:^|\s)@([\w .'-]+)/i);
	const sourceMatch = title.match(
		/(?:^|\s)!(self|email|slack|google_chat|meeting|phone)\b/i,
	);
	const durationMatch = title.match(/(?:^|\s)(\d+h(?:\d+m)?|\d+m)\b/i);
	const plannedMatch = title.match(
		/(?:^|\s)>(today|tomorrow|in\s+\d+\s+days?)\b/i,
	);
	const dueMatch = title.match(/(?:^|\s)(today|tomorrow|in\s+\d+\s+days?)\b/i);
	const parse: CaptureParse = { title, unresolved };

	if (contextMatch) {
		const needle = contextMatch[1].toLowerCase();
		const matches = contexts.filter(
			(context) =>
				context.name.toLowerCase().startsWith(needle) ||
				context.slug.toLowerCase().startsWith(needle),
		);
		if (matches.length === 1) {
			parse.context = matches[0];
			title = title.replace(contextMatch[0], " ");
		} else unresolved.push(`#${contextMatch[1]}`);
	}
	if (personMatch) {
		const needle = personMatch[1].trim().toLowerCase();
		const person = people.find(
			(candidate) => candidate.name.toLowerCase() === needle,
		);
		if (person) {
			parse.person = person;
			title = title.replace(personMatch[0], " ");
		} else unresolved.push(`@${personMatch[1].trim()}`);
	}
	if (sourceMatch) {
		parse.source = sourceMatch[1].toLowerCase();
		title = title.replace(sourceMatch[0], " ");
	}
	if (durationMatch) {
		const raw = durationMatch[1].toLowerCase();
		const hours = Number(raw.match(/(\d+)h/)?.[1] ?? 0);
		const minutes = Number(raw.match(/(\d+)m/)?.[1] ?? 0);
		parse.estimate_minutes = hours * 60 + minutes;
		title = title.replace(durationMatch[0], " ");
	}
	if (plannedMatch) {
		const date = relativeDate(plannedMatch[1].toLowerCase());
		if (date) {
			parse.planned_on = dateString(date);
			title = title.replace(plannedMatch[0], " ");
		}
	} else if (dueMatch) {
		const date = relativeDate(dueMatch[1].toLowerCase());
		if (date) {
			parse.due_on = dateString(date);
			title = title.replace(dueMatch[0], " ");
		}
	}
	parse.title = title.replace(/\s+/g, " ").trim();
	return parse;
}
