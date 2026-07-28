import type { Context, Project } from "./types";

export type CaptureDefaults = {
	contextId?: string;
	projectId?: string;
};

function routeValue(pathname: string, prefix: string) {
	const value = pathname.match(new RegExp(`^/${prefix}/([^/?#]+)`))?.[1];
	if (!value) return undefined;

	try {
		return decodeURIComponent(value);
	} catch {
		return value;
	}
}

export function captureDefaultsForPath(
	pathname: string,
	contexts: Array<Pick<Context, "id" | "slug">>,
	projects: Array<Pick<Project, "id" | "context_id">>,
): CaptureDefaults {
	const contextSlug = routeValue(pathname, "c");
	if (contextSlug) {
		const context = contexts.find(
			(candidate) => candidate.slug === contextSlug,
		);
		return context ? { contextId: context.id } : {};
	}

	const projectId = routeValue(pathname, "p");
	if (projectId) {
		const project = projects.find((candidate) => candidate.id === projectId);
		return project
			? { contextId: project.context_id, projectId: project.id }
			: {};
	}

	return {};
}
