import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import {
	Archive,
	ChevronDown,
	ChevronRight,
	ChevronUp,
	GripVertical,
	MoreHorizontal,
	Pencil,
	Plus,
	RotateCcw,
	Trash2,
} from "lucide-react";
import { type ReactNode, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { ApiError, api } from "@/lib/api";
import { contextPalette } from "@/lib/format";
import type { Collection, Context, Project, ProjectStatus } from "@/lib/types";
import { cn } from "@/lib/utils";

const projectStatusLabels: Record<ProjectStatus, string> = {
	active: "Active",
	paused: "Paused",
	done: "Done",
	archived: "Archived",
};

const projectStatusOrder: Record<ProjectStatus, number> = {
	active: 0,
	paused: 1,
	done: 2,
	archived: 3,
};

function sortContexts(items: Context[]) {
	return [...items].sort(
		(a, b) => a.sort_order - b.sort_order || a.name.localeCompare(b.name),
	);
}

function sortProjects(items: Project[]) {
	return [...items].sort(
		(a, b) =>
			projectStatusOrder[a.status] - projectStatusOrder[b.status] ||
			a.name.localeCompare(b.name),
	);
}

async function invalidateOrganization(
	queryClient: ReturnType<typeof useQueryClient>,
) {
	await Promise.all(
		[
			["contexts"],
			["projects"],
			["brief"],
			["tasks"],
			["context-tasks"],
			["project-tasks"],
			["inbox"],
			["routines"],
		].map((queryKey) => queryClient.invalidateQueries({ queryKey })),
	);
}

function apiFieldError(error: unknown, field: string) {
	return error instanceof ApiError ? error.fields[field] : undefined;
}

function MutationError({ error }: { error: unknown }) {
	if (!error) return null;
	return (
		<p
			role="alert"
			className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive"
		>
			{error instanceof Error ? error.message : "Something went wrong."}
		</p>
	);
}

function FormField({
	label,
	htmlFor,
	error,
	children,
}: {
	label: string;
	htmlFor: string;
	error?: string;
	children: ReactNode;
}) {
	return (
		<div className="grid gap-1.5 text-sm">
			<label htmlFor={htmlFor} className="font-medium">
				{label}
			</label>
			{children}
			{error ? <span className="text-xs text-destructive">{error}</span> : null}
		</div>
	);
}

function ContextFormDialog({
	context,
	open,
	onOpenChange,
	onSaved,
}: {
	context?: Context;
	open: boolean;
	onOpenChange: (open: boolean) => void;
	onSaved?: (context: Context) => void;
}) {
	const queryClient = useQueryClient();
	const [name, setName] = useState(context?.name ?? "");
	const [color, setColor] = useState(
		context?.color ?? contextPalette[0] ?? "#C05E3C",
	);
	const [slug, setSlug] = useState(context?.slug ?? "");
	const mutation = useMutation({
		mutationFn: () =>
			context
				? api.updateContext(context.id, {
						name: name.trim(),
						color: color || null,
						slug: slug.trim(),
					})
				: api.createContext({
						name: name.trim(),
						color: color || null,
						...(slug.trim() ? { slug: slug.trim() } : {}),
					}),
		onSuccess: async (saved) => {
			await invalidateOrganization(queryClient);
			onOpenChange(false);
			onSaved?.(saved);
		},
	});
	const nameError = apiFieldError(mutation.error, "name");
	const colorError = apiFieldError(mutation.error, "color");
	const slugError = apiFieldError(mutation.error, "slug");

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent>
				<form
					className="grid gap-5"
					onSubmit={(event) => {
						event.preventDefault();
						mutation.mutate();
					}}
				>
					<DialogHeader>
						<DialogTitle>
							{context ? "Edit context" : "New context"}
						</DialogTitle>
						<DialogDescription>
							Contexts keep the different parts of your life distinct.
						</DialogDescription>
					</DialogHeader>
					<FormField label="Name" htmlFor="context-name" error={nameError}>
						<Input
							id="context-name"
							autoFocus
							value={name}
							onChange={(event) => setName(event.target.value)}
							aria-invalid={Boolean(nameError)}
							placeholder="Personal"
						/>
					</FormField>
					<FormField
						label="Colour"
						htmlFor="context-custom-colour"
						error={colorError}
					>
						<div className="flex flex-wrap items-center gap-2">
							{contextPalette.map((swatch) => (
								<button
									key={swatch}
									type="button"
									className={cn(
										"size-8 rounded-full border-2 border-white shadow-sm outline-none ring-offset-2 focus-visible:ring-2 focus-visible:ring-ring",
										color === swatch && "ring-2 ring-foreground",
									)}
									style={{ backgroundColor: swatch }}
									onClick={() => setColor(swatch)}
									aria-label={`Use colour ${swatch}`}
									aria-pressed={color === swatch}
								/>
							))}
							<input
								id="context-custom-colour"
								type="color"
								value={color}
								onChange={(event) => setColor(event.target.value)}
								className="size-8 cursor-pointer rounded border border-border bg-transparent p-0.5"
								aria-label="Choose a custom colour"
							/>
						</div>
					</FormField>
					<details className="rounded-lg border border-border px-3 py-2">
						<summary className="cursor-pointer text-sm font-medium">
							Advanced
						</summary>
						<div className="mt-3">
							<FormField
								label="URL slug"
								htmlFor="context-slug"
								error={slugError}
							>
								<Input
									id="context-slug"
									value={slug}
									onChange={(event) => setSlug(event.target.value)}
									aria-invalid={Boolean(slugError)}
									placeholder={context ? undefined : "Generated from the name"}
								/>
							</FormField>
							{context ? (
								<p className="mt-2 text-xs leading-5 text-muted-foreground">
									Changing this also changes the context URL.
								</p>
							) : null}
						</div>
					</details>
					<MutationError error={mutation.error} />
					<DialogFooter>
						<Button
							type="button"
							variant="outline"
							onClick={() => onOpenChange(false)}
						>
							Cancel
						</Button>
						<Button type="submit" disabled={!name.trim() || mutation.isPending}>
							{mutation.isPending
								? "Saving…"
								: context
									? "Save changes"
									: "Create context"}
						</Button>
					</DialogFooter>
				</form>
			</DialogContent>
		</Dialog>
	);
}

function ProjectFormDialog({
	project,
	contexts,
	defaultContextId,
	open,
	onOpenChange,
	onSaved,
}: {
	project?: Project;
	contexts: Context[];
	defaultContextId?: string;
	open: boolean;
	onOpenChange: (open: boolean) => void;
	onSaved?: (project: Project) => void;
}) {
	const queryClient = useQueryClient();
	const [name, setName] = useState(project?.name ?? "");
	const [contextId, setContextId] = useState(
		project?.context_id ?? defaultContextId ?? contexts[0]?.id ?? "",
	);
	const [description, setDescription] = useState(project?.description ?? "");
	const [status, setStatus] = useState<ProjectStatus>(
		project?.status ?? "active",
	);
	const mutation = useMutation({
		mutationFn: () =>
			project
				? api.updateProject(project.id, {
						name: name.trim(),
						context_id: contextId,
						description: description.trim() || null,
						status,
					})
				: api.createProject({
						name: name.trim(),
						context_id: contextId,
						description: description.trim() || null,
						status,
					}),
		onSuccess: async (saved) => {
			await invalidateOrganization(queryClient);
			onOpenChange(false);
			onSaved?.(saved);
		},
	});
	const nameError = apiFieldError(mutation.error, "name");
	const contextError = apiFieldError(mutation.error, "context_id");
	const descriptionError = apiFieldError(mutation.error, "description");
	const statusError = apiFieldError(mutation.error, "status");
	const isMoving = Boolean(project && project.context_id !== contextId);

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent>
				<form
					className="grid gap-5"
					onSubmit={(event) => {
						event.preventDefault();
						mutation.mutate();
					}}
				>
					<DialogHeader>
						<DialogTitle>
							{project ? "Edit project" : "New project"}
						</DialogTitle>
						<DialogDescription>
							A project groups related work inside one context.
						</DialogDescription>
					</DialogHeader>
					<FormField label="Name" htmlFor="project-name" error={nameError}>
						<Input
							id="project-name"
							autoFocus
							value={name}
							onChange={(event) => setName(event.target.value)}
							aria-invalid={Boolean(nameError)}
							placeholder="Website launch"
						/>
					</FormField>
					<FormField
						label="Context"
						htmlFor="project-context"
						error={contextError}
					>
						<select
							id="project-context"
							value={contextId}
							onChange={(event) => setContextId(event.target.value)}
							aria-invalid={Boolean(contextError)}
							className="h-9 rounded-md border border-input bg-background px-3 text-sm"
						>
							{sortContexts(contexts).map((context) => (
								<option key={context.id} value={context.id}>
									{context.name}
								</option>
							))}
						</select>
					</FormField>
					{isMoving ? (
						<p className="rounded-lg bg-[var(--sand)] px-3 py-2 text-sm leading-5">
							Moving this project also moves all of its tasks into the new
							context.
						</p>
					) : null}
					<FormField
						label="Description"
						htmlFor="project-description"
						error={descriptionError}
					>
						<Textarea
							id="project-description"
							value={description}
							onChange={(event) => setDescription(event.target.value)}
							aria-invalid={Boolean(descriptionError)}
							placeholder="What belongs in this project?"
						/>
					</FormField>
					<FormField
						label="Status"
						htmlFor="project-status"
						error={statusError}
					>
						<select
							id="project-status"
							value={status}
							onChange={(event) =>
								setStatus(event.target.value as ProjectStatus)
							}
							aria-invalid={Boolean(statusError)}
							className="h-9 rounded-md border border-input bg-background px-3 text-sm"
						>
							{Object.entries(projectStatusLabels).map(([value, label]) => (
								<option key={value} value={value}>
									{label}
								</option>
							))}
						</select>
					</FormField>
					<MutationError error={mutation.error} />
					<DialogFooter>
						<Button
							type="button"
							variant="outline"
							onClick={() => onOpenChange(false)}
						>
							Cancel
						</Button>
						<Button
							type="submit"
							disabled={!name.trim() || !contextId || mutation.isPending}
						>
							{mutation.isPending
								? "Saving…"
								: project
									? "Save changes"
									: "Create project"}
						</Button>
					</DialogFooter>
				</form>
			</DialogContent>
		</Dialog>
	);
}

function ConfirmDialog({
	open,
	onOpenChange,
	title,
	description,
	confirmLabel,
	pending,
	error,
	destructive = false,
	onConfirm,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	title: string;
	description: ReactNode;
	confirmLabel: string;
	pending: boolean;
	error: unknown;
	destructive?: boolean;
	onConfirm: () => void;
}) {
	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent>
				<DialogHeader>
					<DialogTitle>{title}</DialogTitle>
					<DialogDescription asChild>
						<div className="leading-6">{description}</div>
					</DialogDescription>
				</DialogHeader>
				<MutationError error={error} />
				<DialogFooter>
					<Button variant="outline" onClick={() => onOpenChange(false)}>
						Cancel
					</Button>
					<Button
						variant={destructive ? "destructive" : "default"}
						disabled={pending}
						onClick={onConfirm}
					>
						{pending ? "Working…" : confirmLabel}
					</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}

export function NewContextButton({
	onCreated,
}: {
	onCreated?: (context: Context) => void;
}) {
	const [open, setOpen] = useState(false);
	return (
		<>
			<Button onClick={() => setOpen(true)}>
				<Plus className="size-4" />
				New context
			</Button>
			{open ? (
				<ContextFormDialog
					open={open}
					onOpenChange={setOpen}
					onSaved={onCreated}
				/>
			) : null}
		</>
	);
}

export function NewProjectButton({
	contexts,
	defaultContextId,
	onCreated,
	label = "New project",
}: {
	contexts: Context[];
	defaultContextId?: string;
	onCreated?: (project: Project) => void;
	label?: string;
}) {
	const [open, setOpen] = useState(false);
	return (
		<>
			<Button
				variant="outline"
				size="sm"
				disabled={!contexts.length}
				onClick={() => setOpen(true)}
			>
				<Plus className="size-4" />
				{label}
			</Button>
			{open ? (
				<ProjectFormDialog
					open={open}
					onOpenChange={setOpen}
					contexts={contexts}
					defaultContextId={defaultContextId}
					onSaved={onCreated}
				/>
			) : null}
		</>
	);
}

export function ContextActionsMenu({
	context,
	onSaved,
	onRemoved,
}: {
	context: Context;
	onSaved?: (context: Context) => void;
	onRemoved?: () => void;
}) {
	const queryClient = useQueryClient();
	const [mode, setMode] = useState<"edit" | "archive" | "delete" | null>(null);
	const archiveMutation = useMutation({
		mutationFn: (archived: boolean) =>
			api.updateContext(context.id, { archived }),
		onSuccess: async (saved) => {
			await invalidateOrganization(queryClient);
			setMode(null);
			onSaved?.(saved);
			if (saved.archived_at) onRemoved?.();
		},
	});
	const deleteMutation = useMutation({
		mutationFn: () => api.deleteContext(context.id),
		onSuccess: async () => {
			await invalidateOrganization(queryClient);
			setMode(null);
			onRemoved?.();
		},
	});

	return (
		<>
			<DropdownMenu>
				<DropdownMenuTrigger asChild>
					<Button
						variant="ghost"
						size="icon-sm"
						aria-label={`Manage ${context.name}`}
					>
						<MoreHorizontal className="size-4" />
					</Button>
				</DropdownMenuTrigger>
				<DropdownMenuContent align="end">
					<DropdownMenuItem onSelect={() => setMode("edit")}>
						<Pencil />
						Edit
					</DropdownMenuItem>
					{context.archived_at ? (
						<DropdownMenuItem
							disabled={archiveMutation.isPending}
							onSelect={() => archiveMutation.mutate(false)}
						>
							<RotateCcw />
							Restore
						</DropdownMenuItem>
					) : (
						<DropdownMenuItem onSelect={() => setMode("archive")}>
							<Archive />
							Archive
						</DropdownMenuItem>
					)}
					<DropdownMenuSeparator />
					<DropdownMenuItem
						variant="destructive"
						onSelect={() => setMode("delete")}
					>
						<Trash2 />
						Delete
					</DropdownMenuItem>
				</DropdownMenuContent>
			</DropdownMenu>
			{mode === "edit" ? (
				<ContextFormDialog
					context={context}
					open
					onOpenChange={(open) => !open && setMode(null)}
					onSaved={onSaved}
				/>
			) : null}
			<ConfirmDialog
				open={mode === "archive"}
				onOpenChange={(open) => !open && setMode(null)}
				title={`Archive ${context.name}?`}
				description="The context leaves everyday navigation but remains available in Settings, where you can restore it."
				confirmLabel="Archive context"
				pending={archiveMutation.isPending}
				error={archiveMutation.error}
				onConfirm={() => archiveMutation.mutate(true)}
			/>
			<ConfirmDialog
				open={mode === "delete"}
				onOpenChange={(open) => !open && setMode(null)}
				title={`Delete ${context.name}?`}
				description={
					<>
						Its projects and recurrences will be deleted. Its tasks will be
						moved to the Inbox, not deleted.
					</>
				}
				confirmLabel="Delete context"
				pending={deleteMutation.isPending}
				error={deleteMutation.error}
				destructive
				onConfirm={() => deleteMutation.mutate()}
			/>
		</>
	);
}

export function ProjectActionsMenu({
	project,
	contexts,
	onSaved,
	onRemoved,
}: {
	project: Project;
	contexts: Context[];
	onSaved?: (project: Project) => void;
	onRemoved?: () => void;
}) {
	const queryClient = useQueryClient();
	const [mode, setMode] = useState<"edit" | "delete" | null>(null);
	const deleteMutation = useMutation({
		mutationFn: () => api.deleteProject(project.id),
		onSuccess: async () => {
			await invalidateOrganization(queryClient);
			setMode(null);
			onRemoved?.();
		},
	});

	return (
		<>
			<DropdownMenu>
				<DropdownMenuTrigger asChild>
					<Button
						variant="ghost"
						size="icon-sm"
						aria-label={`Manage ${project.name}`}
					>
						<MoreHorizontal className="size-4" />
					</Button>
				</DropdownMenuTrigger>
				<DropdownMenuContent align="end">
					<DropdownMenuItem onSelect={() => setMode("edit")}>
						<Pencil />
						Edit
					</DropdownMenuItem>
					<DropdownMenuSeparator />
					<DropdownMenuItem
						variant="destructive"
						onSelect={() => setMode("delete")}
					>
						<Trash2 />
						Delete
					</DropdownMenuItem>
				</DropdownMenuContent>
			</DropdownMenu>
			{mode === "edit" ? (
				<ProjectFormDialog
					project={project}
					contexts={contexts}
					open
					onOpenChange={(open) => !open && setMode(null)}
					onSaved={onSaved}
				/>
			) : null}
			<ConfirmDialog
				open={mode === "delete"}
				onOpenChange={(open) => !open && setMode(null)}
				title={`Delete ${project.name}?`}
				description="Its tasks will stay in their context and simply lose this project grouping."
				confirmLabel="Delete project"
				pending={deleteMutation.isPending}
				error={deleteMutation.error}
				destructive
				onConfirm={() => deleteMutation.mutate()}
			/>
		</>
	);
}

function ProjectManagementRow({
	project,
	contexts,
	highlighted,
}: {
	project: Project;
	contexts: Context[];
	highlighted: boolean;
}) {
	return (
		<div
			className={cn(
				"flex items-center gap-3 rounded-lg px-3 py-2 transition",
				highlighted ? "bg-[var(--sand)]" : "hover:bg-muted/60",
			)}
		>
			<span
				className={cn(
					"size-2 rounded-full",
					project.status === "active" && "bg-[var(--success)]",
					project.status === "paused" && "bg-[var(--clay-400)]",
					project.status === "done" && "bg-[var(--sage)]",
					project.status === "archived" && "bg-muted-foreground/50",
				)}
			/>
			<Link
				to="/p/$projectId"
				params={{ projectId: project.id }}
				className="min-w-0 flex-1 truncate text-sm font-medium hover:text-[var(--accent)]"
			>
				{project.name}
			</Link>
			<span className="text-xs text-muted-foreground">
				{projectStatusLabels[project.status]}
			</span>
			<ProjectActionsMenu project={project} contexts={contexts} />
		</div>
	);
}

function ContextManagementRow({
	context,
	allContexts,
	projects,
	index,
	total,
	highlightedId,
	onHighlight,
	onMove,
	onDropContext,
}: {
	context: Context;
	allContexts: Context[];
	projects: Project[];
	index: number;
	total: number;
	highlightedId?: string;
	onHighlight: (id: string) => void;
	onMove: (id: string, direction: -1 | 1) => void;
	onDropContext: (sourceId: string, targetId: string) => void;
}) {
	const [expanded, setExpanded] = useState(true);
	const [draggedId, setDraggedId] = useState<string>();
	const openProjects = sortProjects(
		projects.filter(
			(project) => project.status === "active" || project.status === "paused",
		),
	);
	const closedProjects = sortProjects(
		projects.filter(
			(project) => project.status === "done" || project.status === "archived",
		),
	);

	return (
		<article
			draggable
			onDragStart={(event) => {
				setDraggedId(context.id);
				event.dataTransfer.setData("text/context-id", context.id);
				event.dataTransfer.effectAllowed = "move";
			}}
			onDragEnd={() => setDraggedId(undefined)}
			onDragOver={(event) => event.preventDefault()}
			onDrop={(event) => {
				event.preventDefault();
				const sourceId =
					event.dataTransfer.getData("text/context-id") || draggedId;
				if (sourceId && sourceId !== context.id) {
					onDropContext(sourceId, context.id);
				}
			}}
			className={cn(
				"overflow-hidden rounded-2xl border bg-card transition",
				highlightedId === context.id
					? "border-[var(--accent)] shadow-sm"
					: "border-border",
			)}
		>
			<header className="flex items-center gap-2 px-3 py-3 sm:px-4">
				<GripVertical
					className="hidden size-4 cursor-grab text-muted-foreground sm:block"
					aria-hidden="true"
				/>
				<button
					type="button"
					className="grid size-7 place-items-center rounded-md hover:bg-muted"
					onClick={() => setExpanded((value) => !value)}
					aria-label={`${expanded ? "Collapse" : "Expand"} ${context.name}`}
					aria-expanded={expanded}
				>
					{expanded ? (
						<ChevronDown className="size-4" />
					) : (
						<ChevronRight className="size-4" />
					)}
				</button>
				<span
					className="size-3 rounded-full"
					style={{ backgroundColor: context.color ?? contextPalette[0] }}
				/>
				<Link
					to="/c/$slug"
					params={{ slug: context.slug }}
					className="min-w-0 flex-1 truncate font-medium hover:text-[var(--accent)]"
				>
					{context.name}
				</Link>
				<span className="hidden text-xs text-muted-foreground sm:inline">
					{projects.length} {projects.length === 1 ? "project" : "projects"}
				</span>
				<div className="flex items-center">
					<Button
						variant="ghost"
						size="icon-xs"
						disabled={index === 0}
						onClick={() => onMove(context.id, -1)}
						aria-label={`Move ${context.name} up`}
					>
						<ChevronUp />
					</Button>
					<Button
						variant="ghost"
						size="icon-xs"
						disabled={index === total - 1}
						onClick={() => onMove(context.id, 1)}
						aria-label={`Move ${context.name} down`}
					>
						<ChevronDown />
					</Button>
				</div>
				<ContextActionsMenu context={context} />
			</header>
			{expanded ? (
				<div className="border-t border-border px-3 py-3 sm:px-5">
					<div className="mb-2 flex items-center justify-between">
						<p className="text-xs font-medium tracking-wide text-muted-foreground uppercase">
							Projects
						</p>
						<NewProjectButton
							contexts={allContexts}
							defaultContextId={context.id}
							onCreated={(project) => onHighlight(project.id)}
						/>
					</div>
					{openProjects.length ? (
						<div className="grid gap-1">
							{openProjects.map((project) => (
								<ProjectManagementRow
									key={project.id}
									project={project}
									contexts={allContexts}
									highlighted={highlightedId === project.id}
								/>
							))}
						</div>
					) : (
						<p className="rounded-lg bg-muted/50 px-3 py-3 text-sm text-muted-foreground">
							No active projects in this context.
						</p>
					)}
					{closedProjects.length ? (
						<details className="mt-3 border-t border-border pt-3">
							<summary className="cursor-pointer text-sm text-muted-foreground">
								Completed &amp; archived · {closedProjects.length}
							</summary>
							<div className="mt-2 grid gap-1">
								{closedProjects.map((project) => (
									<ProjectManagementRow
										key={project.id}
										project={project}
										contexts={allContexts}
										highlighted={highlightedId === project.id}
									/>
								))}
							</div>
						</details>
					) : null}
				</div>
			) : null}
		</article>
	);
}

export function ContextProjectSettingsPage() {
	const queryClient = useQueryClient();
	const contextsQuery = useQuery({
		queryKey: ["contexts", "all"],
		queryFn: () => api.contexts(true),
	});
	const projectsQuery = useQuery({
		queryKey: ["projects"],
		queryFn: () => api.projects(),
	});
	const [highlightedId, setHighlightedId] = useState<string>();
	const [reorderError, setReorderError] = useState<unknown>();
	const contexts = useMemo(
		() => sortContexts(contextsQuery.data?.data ?? []),
		[contextsQuery.data],
	);
	const activeContexts = contexts.filter((context) => !context.archived_at);
	const archivedContexts = contexts.filter((context) => context.archived_at);
	const projects = projectsQuery.data?.data ?? [];
	const reorderMutation = useMutation({
		mutationFn: async (ordered: Context[]) => {
			setReorderError(undefined);
			return Promise.all(
				ordered.map((context, index) =>
					api.updateContext(context.id, { sort_order: (index + 1) * 10 }),
				),
			);
		},
		onMutate: async (ordered) => {
			await queryClient.cancelQueries({ queryKey: ["contexts", "all"] });
			const previous = queryClient.getQueryData<Collection<Context>>([
				"contexts",
				"all",
			]);
			if (previous) {
				const nextOrder = new Map(
					ordered.map((context, index) => [context.id, (index + 1) * 10]),
				);
				queryClient.setQueryData<Collection<Context>>(["contexts", "all"], {
					...previous,
					data: previous.data.map((context) => ({
						...context,
						sort_order: nextOrder.get(context.id) ?? context.sort_order,
					})),
				});
			}
			return { previous };
		},
		onError: (error, _ordered, context) => {
			setReorderError(error);
			if (context?.previous) {
				queryClient.setQueryData(["contexts", "all"], context.previous);
			}
		},
		onSettled: async () => {
			await invalidateOrganization(queryClient);
		},
	});
	const reorder = (sourceId: string, targetId: string) => {
		const sourceIndex = activeContexts.findIndex(
			(context) => context.id === sourceId,
		);
		const targetIndex = activeContexts.findIndex(
			(context) => context.id === targetId,
		);
		if (sourceIndex < 0 || targetIndex < 0 || sourceIndex === targetIndex)
			return;
		const ordered = [...activeContexts];
		const [moved] = ordered.splice(sourceIndex, 1);
		if (!moved) return;
		ordered.splice(targetIndex, 0, moved);
		reorderMutation.mutate(ordered);
	};
	const move = (sourceId: string, direction: -1 | 1) => {
		const sourceIndex = activeContexts.findIndex(
			(context) => context.id === sourceId,
		);
		const target = activeContexts[sourceIndex + direction];
		if (target) reorder(sourceId, target.id);
	};

	if (contextsQuery.isLoading || projectsQuery.isLoading) {
		return (
			<div className="space-y-3">
				<div className="h-10 w-72 animate-pulse rounded-xl bg-muted" />
				<div className="h-28 animate-pulse rounded-2xl bg-muted" />
				<div className="h-28 animate-pulse rounded-2xl bg-muted" />
			</div>
		);
	}
	if (contextsQuery.error || projectsQuery.error) {
		return (
			<section className="max-w-3xl">
				<h1 className="font-display text-4xl tracking-tight">
					Contexts &amp; projects
				</h1>
				<div className="mt-6">
					<MutationError error={contextsQuery.error ?? projectsQuery.error} />
				</div>
				<Button
					className="mt-4"
					variant="outline"
					onClick={() => {
						contextsQuery.refetch();
						projectsQuery.refetch();
					}}
				>
					Try again
				</Button>
			</section>
		);
	}

	return (
		<section className="mx-auto max-w-4xl">
			<div className="mb-8 flex flex-wrap items-end justify-between gap-4">
				<div>
					<Link
						to="/settings"
						className="mb-3 inline-flex text-sm text-muted-foreground hover:text-foreground"
					>
						Settings
					</Link>
					<h1 className="font-display text-4xl tracking-tight">
						Contexts &amp; projects
					</h1>
					<p className="mt-2 max-w-2xl text-sm leading-6 text-muted-foreground">
						Keep each part of life distinct, then group related work into
						projects.
					</p>
				</div>
				<NewContextButton
					onCreated={(context) => setHighlightedId(context.id)}
				/>
			</div>
			<MutationError error={reorderError} />
			{activeContexts.length ? (
				<div className="mt-4 grid gap-3">
					{activeContexts.map((context, index) => (
						<ContextManagementRow
							key={context.id}
							context={context}
							allContexts={activeContexts}
							projects={projects.filter(
								(project) => project.context_id === context.id,
							)}
							index={index}
							total={activeContexts.length}
							highlightedId={highlightedId}
							onHighlight={setHighlightedId}
							onMove={move}
							onDropContext={reorder}
						/>
					))}
				</div>
			) : (
				<div className="mt-6 rounded-2xl border border-dashed border-border bg-card/50 px-6 py-12 text-center">
					<h2 className="font-display text-2xl">Create your first context</h2>
					<p className="mx-auto mt-2 max-w-md text-sm leading-6 text-muted-foreground">
						Start with one clear area of life or work. You can add projects
						inside it next.
					</p>
				</div>
			)}
			{archivedContexts.length ? (
				<details className="mt-8 rounded-2xl border border-border bg-card">
					<summary className="cursor-pointer px-5 py-4 text-sm font-medium">
						Archived contexts · {archivedContexts.length}
					</summary>
					<div className="grid gap-1 border-t border-border p-3">
						{archivedContexts.map((context) => (
							<div
								key={context.id}
								className="flex items-center gap-3 rounded-lg px-3 py-2"
							>
								<span
									className="size-3 rounded-full opacity-60"
									style={{
										backgroundColor: context.color ?? contextPalette[0],
									}}
								/>
								<span className="flex-1 text-sm font-medium">
									{context.name}
								</span>
								<span className="text-xs text-muted-foreground">Archived</span>
								<ContextActionsMenu context={context} />
							</div>
						))}
					</div>
				</details>
			) : null}
		</section>
	);
}
