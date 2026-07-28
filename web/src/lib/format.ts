export const contextPalette = ["#C05E3C", "#6E7A4F", "#C39A3A", "#6A6B7C"];
const todayFormatters = new Map<string, Intl.DateTimeFormat>();
const timestampFormatter = new Intl.DateTimeFormat(undefined, {
	dateStyle: "medium",
	timeStyle: "short",
});

export function formatMinutes(minutes: number | null | undefined) {
	if (!minutes) return "No estimate";
	const hours = Math.floor(minutes / 60);
	const remainder = minutes % 60;
	return [hours ? `${hours}h` : "", remainder ? `${remainder}m` : ""]
		.filter(Boolean)
		.join("");
}

export function formatDate(date: string | null) {
	if (!date) return null;
	return new Intl.DateTimeFormat(undefined, {
		day: "numeric",
		month: "short",
	}).format(new Date(`${date}T12:00:00`));
}

export function formatTimestamp(timestamp: string) {
	return timestampFormatter.format(new Date(timestamp));
}

export function daysLate(date: string | null) {
	if (!date) return null;
	const today = new Date();
	const target = new Date(`${date}T12:00:00`);
	return Math.max(
		0,
		Math.round((today.setHours(0, 0, 0, 0) - target.getTime()) / 86_400_000),
	);
}

export function todayString(timeZone?: string) {
	const key = timeZone ?? "";
	let formatter = todayFormatters.get(key);
	if (!formatter) {
		formatter = new Intl.DateTimeFormat("en-CA", {
			year: "numeric",
			month: "2-digit",
			day: "2-digit",
			timeZone,
		});
		todayFormatters.set(key, formatter);
	}
	return formatter.format(new Date());
}

export function displayDate(date: string) {
	return new Intl.DateTimeFormat("en-GB", {
		weekday: "long",
		day: "numeric",
		month: "long",
		year: "numeric",
	}).format(new Date(`${date}T12:00:00`));
}
