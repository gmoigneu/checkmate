import type {
	ApiErrorPayload,
	Brief,
	Collection,
	Context,
	Me,
	Person,
	Project,
	Recurrence,
	Task,
} from "./types";

export class ApiError extends Error {
	readonly status: number;
	readonly fields: Record<string, string>;

	constructor(status: number, payload: ApiErrorPayload) {
		super(payload.error || "Something went wrong.");
		this.name = "ApiError";
		this.status = status;
		this.fields = payload.fields ?? {};
	}
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
	const response = await fetch(path, {
		credentials: "same-origin",
		headers: { "Content-Type": "application/json", ...init?.headers },
		...init,
	});

	if (!response.ok) {
		const payload = (await response
			.json()
			.catch(() => ({ error: response.statusText }))) as ApiErrorPayload;
		throw new ApiError(response.status, payload);
	}

	if (response.status === 204) return undefined as T;
	return (await response.json()) as T;
}

export const api = {
	me: () => request<Me>("/v1/me"),
	brief: (date?: string, contextId?: string) => {
		const query = new URLSearchParams();
		if (date) query.set("date", date);
		if (contextId) query.set("context_id", contextId);
		return request<Brief>(`/v1/brief?${query}`);
	},
	contexts: () => request<Collection<Context>>("/v1/contexts?limit=200"),
	projects: (contextId?: string) =>
		request<Collection<Project>>(
			`/v1/projects?limit=200${contextId ? `&context_id=${contextId}` : ""}`,
		),
	people: () => request<Collection<Person>>("/v1/people?limit=200"),
	tasks: (params: URLSearchParams) =>
		request<Collection<Task>>(`/v1/tasks?${params}`),
	routines: () =>
		request<Collection<Recurrence>>("/v1/recurrences?kind=routine&limit=200"),
	createRoutine: (body: Record<string, unknown>) =>
		request<Recurrence>("/v1/recurrences", {
			method: "POST",
			body: JSON.stringify({ ...body, kind: "routine" }),
		}),
	updateRoutine: (id: string, body: Record<string, unknown>) =>
		request<Recurrence>(`/v1/recurrences/${id}`, {
			method: "PATCH",
			body: JSON.stringify(body),
		}),
	deleteRoutine: (id: string) =>
		request<void>(`/v1/recurrences/${id}`, { method: "DELETE" }),
	task: (id: string) => request<Task>(`/v1/tasks/${id}`),
	createTask: (body: Record<string, unknown>) =>
		request<Task>("/v1/tasks", { method: "POST", body: JSON.stringify(body) }),
	updateTask: (id: string, body: Record<string, unknown>) =>
		request<Task>(`/v1/tasks/${id}`, {
			method: "PATCH",
			body: JSON.stringify(body),
		}),
	deleteTask: (id: string) =>
		request<void>(`/v1/tasks/${id}`, { method: "DELETE" }),
	authConfig: () => request<{ providers: string[] }>("/auth/config"),
};
