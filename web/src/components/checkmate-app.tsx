import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useLocation, useNavigate } from "@tanstack/react-router";
import {
	ArrowLeft,
	ArrowRight,
	Check,
	ChevronDown,
	ChevronLeft,
	ChevronRight,
	type Circle,
	CircleDotDashed,
	Clock3,
	Command,
	Inbox,
	LoaderCircle,
	Menu,
	MoreHorizontal,
	Plus,
	RefreshCw,
	Search,
	Settings,
	Sparkles,
	X,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Sheet, SheetContent } from "@/components/ui/sheet";
import { Textarea } from "@/components/ui/textarea";
import { ApiError, api } from "@/lib/api";
import { parseCapture } from "@/lib/capture";
import {
	contextPalette,
	daysLate,
	displayDate,
	formatDate,
	formatMinutes,
	todayString,
} from "@/lib/format";
import type { Brief, Context, Person, Task } from "@/lib/types";
import { cn } from "@/lib/utils";

type Page =
	| "brief"
	| "inbox"
	| "tasks"
	| "waiting"
	| "repeating"
	| "settings"
	| "context"
	| "project";

const navItems: Array<{
	page: Page;
	label: string;
	icon: typeof Circle;
	href: string;
}> = [
	{ page: "brief", label: "Brief", icon: Sparkles, href: "/" },
	{ page: "inbox", label: "Inbox", icon: Inbox, href: "/inbox" },
];

function pageForPath(pathname: string): Page {
	if (pathname === "/") return "brief";
	if (pathname.startsWith("/inbox")) return "inbox";
	if (pathname.startsWith("/waiting")) return "waiting";
	if (pathname.startsWith("/repeating")) return "repeating";
	if (pathname.startsWith("/settings")) return "settings";
	if (pathname.startsWith("/c/")) return "context";
	if (pathname.startsWith("/p/")) return "project";
	return "tasks";
}

export function CheckmateApp({ detailId }: { detailId?: string }) {
	const location = useLocation();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const [captureOpen, setCaptureOpen] = useState(false);
	const [sidebarOpen, setSidebarOpen] = useState(false);
	const page = pageForPath(location.pathname);
	const me = useQuery({ queryKey: ["me"], queryFn: api.me, retry: false });
	const contexts = useQuery({
		queryKey: ["contexts"],
		queryFn: api.contexts,
		retry: false,
	});
	const projects = useQuery({
		queryKey: ["projects"],
		queryFn: () => api.projects(),
		retry: false,
	});
	const people = useQuery({
		queryKey: ["people"],
		queryFn: api.people,
		retry: false,
	});
	const brief = useQuery({
		queryKey: ["brief", todayString()],
		queryFn: () => api.brief(todayString()),
		retry: false,
	});

	useEffect(() => {
		const handler = (event: KeyboardEvent) => {
			if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
				event.preventDefault();
				setCaptureOpen(true);
			}
			if (
				event.key.toLowerCase() === "c" &&
				!(
					event.target instanceof HTMLInputElement ||
					event.target instanceof HTMLTextAreaElement
				)
			)
				setCaptureOpen(true);
		};
		window.addEventListener("keydown", handler);
		return () => window.removeEventListener("keydown", handler);
	}, []);

	if (me.error instanceof ApiError && me.error.status === 401) {
		window.location.assign(
			`/signin?redirect_to=${encodeURIComponent(location.pathname)}`,
		);
		return null;
	}

	const isLoading = me.isLoading || contexts.isLoading || brief.isLoading;
	const invalidate = () =>
		queryClient.invalidateQueries({ queryKey: ["brief"] });
	const appContexts = contexts.data?.data ?? [];
	const appProjects = projects.data?.data ?? [];
	const appPeople = people.data?.data ?? [];

	const content = () => {
		const loadedBrief = brief.data;
		if (isLoading) return <LoadingPage />;
		if (brief.error) return <ErrorState onRetry={invalidate} />;
		if (!loadedBrief) return <LoadingPage />;
		if (page === "brief")
			return (
				<BriefPage
					brief={loadedBrief}
					contexts={appContexts}
					onOpenCapture={() => setCaptureOpen(true)}
				/>
			);
		if (page === "inbox")
			return <InboxPage contexts={appContexts} people={appPeople} />;
		if (page === "waiting") return <WaitingPage brief={loadedBrief} />;
		if (page === "repeating")
			return (
				<EmptyPage
					title="Repeating"
					description="Recurring work will appear here. Create a series from a task when you know the rhythm."
				/>
			);
		if (page === "settings") return <SettingsPage me={me.data} />;
		if (page === "context")
			return (
				<ContextPage
					contexts={appContexts}
					projects={appProjects}
					pathname={location.pathname}
				/>
			);
		if (page === "project")
			return (
				<ProjectPage projects={appProjects} pathname={location.pathname} />
			);
		return <TaskListPage />;
	};

	return (
		<div className="min-h-screen bg-[var(--page)] text-foreground">
			<div className="fixed inset-x-0 top-0 z-50 h-1 bg-[var(--coral)]" />
			<aside className="hidden lg:block">
				<Sidebar
					page={page}
					brief={brief.data}
					contexts={appContexts}
					projects={appProjects}
					onCapture={() => setCaptureOpen(true)}
				/>
			</aside>
			<Sheet open={sidebarOpen} onOpenChange={setSidebarOpen}>
				<SheetContent
					side="left"
					className="w-80 border-none bg-[var(--sidebar)] p-0 lg:hidden"
				>
					<Sidebar
						page={page}
						brief={brief.data}
						contexts={appContexts}
						projects={appProjects}
						onCapture={() => setCaptureOpen(true)}
					/>
				</SheetContent>
			</Sheet>
			<main className="lg:pl-72">
				<header className="sticky top-0 z-30 flex h-16 items-center justify-between border-b border-border/70 bg-[color:var(--page)/0.9] px-4 backdrop-blur-xl sm:px-7">
					<Button
						variant="ghost"
						size="icon"
						className="lg:hidden"
						onClick={() => setSidebarOpen(true)}
						aria-label="Open navigation"
					>
						<Menu />
					</Button>
					<button
						className="group hidden max-w-sm items-center gap-2 rounded-xl border border-border bg-card px-3 py-2 text-sm text-muted-foreground transition hover:border-[var(--coral)] hover:text-foreground md:flex"
						onClick={() => setCaptureOpen(true)}
					>
						<Search className="size-4" />{" "}
						<span className="mr-6">Capture anything…</span>
						<kbd className="rounded bg-muted px-1.5 py-0.5 text-xs">⌘K</kbd>
					</button>
					<div className="ml-auto flex items-center gap-2">
						<button
							className="hidden items-center gap-2 rounded-full px-3 py-1.5 text-xs text-muted-foreground hover:bg-muted sm:flex"
							onClick={invalidate}
						>
							<span className="size-2 rounded-full bg-[var(--sage)]" /> Synced
							now
						</button>
						<button
							className="flex size-9 items-center justify-center rounded-full bg-[var(--ink)] text-xs font-semibold text-white"
							aria-label="Account menu"
						>
							{me.data?.name.slice(0, 2).toUpperCase() ?? "CM"}
						</button>
					</div>
				</header>
				<div className="mx-auto max-w-6xl px-4 py-7 sm:px-7 sm:py-10">
					{content()}
				</div>
			</main>
			<Button
				className="fixed bottom-5 right-5 z-20 size-14 rounded-full bg-[var(--coral)] shadow-lg hover:bg-[var(--coral-deep)] lg:hidden"
				size="icon"
				onClick={() => setCaptureOpen(true)}
				aria-label="Capture a task"
			>
				<Plus className="size-6" />
			</Button>
			<CaptureDialog
				open={captureOpen}
				onOpenChange={setCaptureOpen}
				contexts={appContexts}
				people={appPeople}
			/>
			{detailId ? (
				<TaskDetail
					taskId={detailId}
					contexts={appContexts}
					people={appPeople}
					onClose={() =>
						navigate({
							to: location.pathname.replace(`/t/${detailId}`, "") || "/",
						})
					}
				/>
			) : null}
		</div>
	);
}

function Sidebar({
	page,
	brief,
	contexts,
	projects,
	onCapture,
}: {
	page: Page;
	brief?: Brief;
	contexts: Context[];
	projects: { context_id: string; id: string; name: string; status: string }[];
	onCapture: () => void;
}) {
	return (
		<div className="fixed inset-y-0 flex w-72 flex-col bg-[var(--sidebar)] px-4 py-6 text-[var(--sidebar-text)]">
			<Link to="/" className="mb-8 flex items-center gap-3 px-3">
				<span className="grid size-9 place-items-center rounded-[14px] bg-[var(--coral)] text-lg font-black text-white">
					C
				</span>
				<span className="font-display text-xl tracking-tight">Checkmate</span>
			</Link>
			<nav className="space-y-1">
				{navItems.map((item) => (
					<Link
						key={item.page}
						to={item.href}
						className={cn(
							"flex items-center justify-between rounded-xl px-3 py-2.5 text-sm transition",
							page === item.page
								? "bg-white/10 font-medium text-white"
								: "text-white/60 hover:bg-white/6 hover:text-white",
						)}
					>
						<span className="flex items-center gap-3">
							<item.icon className="size-4" />
							{item.label}
						</span>
						<Count
							value={
								item.page === "brief"
									? (brief?.totals.overdue ?? 0) +
										(brief?.totals.due_today ?? 0) +
										(brief?.totals.planned ?? 0)
									: (brief?.totals.inbox ?? 0)
							}
						/>
					</Link>
				))}
			</nav>
			<div className="my-6 border-t border-white/10" />
			<div className="mb-2 px-3 text-[10px] font-semibold uppercase tracking-[0.14em] text-white/35">
				Contexts
			</div>
			<nav className="space-y-1">
				{contexts.map((context, index) => (
					<div key={context.id}>
						<Link
							to="/c/$slug"
							params={{ slug: context.slug }}
							className={cn(
								"flex items-center gap-3 rounded-xl px-3 py-2 text-sm text-white/65 transition hover:bg-white/6 hover:text-white",
								page === "context" && "bg-white/10 text-white",
							)}
						>
							<span
								className="size-2 rounded-full"
								style={{
									backgroundColor:
										context.color ??
										contextPalette[index % contextPalette.length],
								}}
							/>
							{context.name}
						</Link>
						{projects
							.filter(
								(project) =>
									project.context_id === context.id &&
									project.status !== "archived",
							)
							.slice(0, 3)
							.map((project) => (
								<Link
									key={project.id}
									to="/p/$projectId"
									params={{ projectId: project.id }}
									className="ml-8 block truncate rounded-lg px-3 py-1.5 text-xs text-white/40 hover:bg-white/5 hover:text-white/80"
								>
									{project.name}
								</Link>
							))}
					</div>
				))}
			</nav>
			<div className="mt-auto space-y-1">
				<Link
					to="/waiting"
					className={cn(
						"flex items-center justify-between rounded-xl px-3 py-2.5 text-sm text-white/60 hover:bg-white/6 hover:text-white",
						page === "waiting" && "bg-white/10 text-white",
					)}
				>
					<span className="flex items-center gap-3">
						<Clock3 className="size-4" />
						Waiting on
					</span>
					<Count value={brief?.totals.waiting_on ?? 0} />
				</Link>
				<Link
					to="/repeating"
					className="flex items-center gap-3 rounded-xl px-3 py-2.5 text-sm text-white/60 hover:bg-white/6 hover:text-white"
				>
					<RefreshCw className="size-4" />
					Repeating
				</Link>
				<Link
					to="/settings"
					className="flex items-center gap-3 rounded-xl px-3 py-2.5 text-sm text-white/60 hover:bg-white/6 hover:text-white"
				>
					<Settings className="size-4" />
					Settings
				</Link>
				<Button
					className="mt-4 w-full justify-start gap-2 bg-white text-[var(--ink)] hover:bg-white/90"
					onClick={onCapture}
				>
					<Plus className="size-4" />
					New task{" "}
					<kbd className="ml-auto rounded bg-black/5 px-1.5 text-[10px]">C</kbd>
				</Button>
			</div>
		</div>
	);
}

function Count({ value }: { value: number }) {
	return value ? (
		<span className="grid min-w-5 place-items-center rounded-full bg-white/12 px-1.5 py-0.5 text-[10px] tabular-nums text-white/80">
			{value > 99 ? "99+" : value}
		</span>
	) : null;
}

function BriefPage({
	brief,
	contexts,
	onOpenCapture,
}: {
	brief: Brief;
	contexts: Context[];
	onOpenCapture: () => void;
}) {
	const [date, setDate] = useState(brief.date || todayString());
	const [contextId, setContextId] = useState<string>();
	const query = useQuery({
		queryKey: ["brief", date, contextId],
		queryFn: () => api.brief(date, contextId),
		initialData: brief,
	});
	const data = query.data ?? brief;
	const openWork =
		data.totals.overdue + data.totals.due_today + data.totals.planned;
	const doneRatio =
		data.totals.completed_today + openWork
			? Math.round(
					(data.totals.completed_today /
						(data.totals.completed_today + openWork)) *
						100,
				)
			: 0;
	const perfect = !openWork && !data.totals.inbox;
	return (
		<section className="animate-in fade-in duration-500">
			<div className="mb-8 flex flex-col gap-5 sm:flex-row sm:items-end sm:justify-between">
				<div>
					<p className="mb-2 text-sm font-medium text-muted-foreground">
						Your day, in one calm read
					</p>
					<h1 className="font-display text-3xl tracking-tight sm:text-4xl">
						{displayDate(data.date)}
					</h1>
				</div>
				<div className="flex flex-wrap items-center gap-2">
					<div className="flex items-center rounded-xl border border-border bg-card p-1">
						<Button
							variant="ghost"
							size="icon"
							className="size-8"
							onClick={() => setDate(dateOffset(date, -1))}
							aria-label="Previous day"
						>
							<ChevronLeft className="size-4" />
						</Button>
						<Button
							variant="ghost"
							size="sm"
							onClick={() => setDate(todayString())}
						>
							Today
						</Button>
						<Button
							variant="ghost"
							size="icon"
							className="size-8"
							onClick={() => setDate(dateOffset(date, 1))}
							aria-label="Next day"
						>
							<ChevronRight className="size-4" />
						</Button>
					</div>
					<select
						value={contextId ?? ""}
						onChange={(event) => setContextId(event.target.value || undefined)}
						className="h-10 rounded-xl border border-border bg-card px-3 text-sm outline-none focus:ring-2 focus:ring-ring"
					>
						<option value="">All contexts</option>
						{contexts.map((context) => (
							<option key={context.id} value={context.id}>
								{context.name}
							</option>
						))}
					</select>
				</div>
			</div>
			{perfect ? (
				<PerfectDay onCapture={onOpenCapture} />
			) : (
				<>
					<div className="mb-8 rounded-2xl border border-border bg-card p-5 shadow-sm">
						<div className="flex flex-wrap gap-x-5 gap-y-2 text-sm text-muted-foreground">
							<span>
								<strong className="text-[var(--overdue)] tabular-nums">
									{data.totals.overdue}
								</strong>{" "}
								overdue
							</span>
							<span>
								<strong className="text-foreground tabular-nums">
									{data.totals.due_today}
								</strong>{" "}
								due today
							</span>
							<span>
								<strong className="text-foreground tabular-nums">
									{data.totals.planned}
								</strong>{" "}
								planned
							</span>
							<span>
								<strong className="text-foreground tabular-nums">
									{formatMinutes(data.totals.planned_minutes)}
								</strong>{" "}
								planned
								{data.totals.planned_without_estimate
									? ` · ${data.totals.planned_without_estimate} without estimate`
									: ""}
							</span>
						</div>
						<div className="mt-5 flex items-center gap-3">
							<div className="h-1.5 flex-1 overflow-hidden rounded-full bg-muted">
								<div
									className="h-full rounded-full bg-[var(--sage)] transition-all"
									style={{ width: `${doneRatio}%` }}
								/>
							</div>
							<span className="whitespace-nowrap text-xs text-muted-foreground tabular-nums">
								{data.totals.completed_today} done today
							</span>
						</div>
					</div>
					<BriefSection
						title="Overdue"
						count={data.totals.overdue}
						tasks={data.overdue}
						kind="overdue"
					/>
					<BriefSection
						title="Due today"
						count={data.totals.due_today}
						tasks={data.due_today}
					/>
					<BriefSection
						title="Planned today"
						count={data.totals.planned}
						tasks={data.planned}
						planned
					/>
					<BriefSection
						title="In progress"
						count={data.totals.in_progress}
						tasks={data.in_progress}
					/>
					<WaitingSection groups={data.waiting_on} />
					<BriefSection
						title={contextId ? "Inbox · all contexts" : "Inbox"}
						count={data.totals.inbox}
						tasks={data.inbox.slice(0, 4)}
						action={
							<Link
								to="/inbox"
								className="text-xs font-medium text-[var(--coral)] hover:underline"
							>
								Triage all →
							</Link>
						}
						empty="Your inbox is clear"
					/>
					<BriefSection
						title="Done today"
						count={data.totals.completed_today}
						tasks={data.completed_today}
						done
					/>
				</>
			)}
		</section>
	);
}

function BriefSection({
	title,
	count,
	tasks,
	kind,
	planned,
	done,
	action,
	empty,
}: {
	title: string;
	count: number;
	tasks: Task[];
	kind?: "overdue";
	planned?: boolean;
	done?: boolean;
	action?: React.ReactNode;
	empty?: string;
}) {
	const [collapsed, setCollapsed] = useState(done);
	if (!count && !empty) return null;
	return (
		<section className="mb-7">
			<div className="mb-2 flex items-center gap-3">
				<button
					className="flex items-center gap-2 text-left"
					onClick={() => setCollapsed(!collapsed)}
				>
					<ChevronDown
						className={cn(
							"size-4 text-muted-foreground transition",
							collapsed && "-rotate-90",
						)}
					/>
					<h2 className="text-xs font-semibold uppercase tracking-[0.14em] text-muted-foreground">
						{title} <span className="tabular-nums">· {count}</span>
						{planned ? (
							<span className="normal-case tracking-normal">
								{" "}
								·{" "}
								{formatMinutes(
									tasks.reduce(
										(total, task) => total + (task.estimate_minutes ?? 0),
										0,
									),
								)}
							</span>
						) : null}
					</h2>
				</button>
				<div className="h-px flex-1 bg-border" />
				{action}
			</div>
			{!collapsed &&
				(tasks.length ? (
					<div className="overflow-hidden rounded-2xl border border-border bg-card">
						<div className="divide-y divide-border">
							{tasks.map((task) => (
								<TaskRow
									key={task.id}
									task={task}
									overdue={kind === "overdue"}
									done={done}
								/>
							))}
						</div>
					</div>
				) : (
					<p className="rounded-xl bg-muted/50 px-4 py-3 text-sm text-muted-foreground">
						{empty}
					</p>
				))}
		</section>
	);
}

function WaitingSection({ groups }: { groups: Brief["waiting_on"] }) {
	if (!groups.length) return null;
	return (
		<section className="mb-7">
			<div className="mb-2 flex items-center gap-3">
				<h2 className="text-xs font-semibold uppercase tracking-[0.14em] text-muted-foreground">
					Waiting on ·{" "}
					{groups.reduce((sum, group) => sum + group.tasks.length, 0)}
				</h2>
				<div className="h-px flex-1 bg-border" />
			</div>
			<div className="space-y-3">
				{groups.map((group) => (
					<div
						key={group.person_id}
						className="rounded-2xl border border-border bg-card p-4"
					>
						<div className="mb-3 flex items-center justify-between">
							<span className="text-sm font-medium">
								{group.person_name}{" "}
								<span className="text-muted-foreground">
									· {group.tasks.length}
								</span>
							</span>
							<Button
								variant="ghost"
								size="sm"
								className="h-7 text-xs text-[var(--coral)]"
							>
								Follow up
							</Button>
						</div>
						<div className="divide-y divide-border">
							{group.tasks.map((task) => (
								<TaskRow key={task.id} task={task} compact />
							))}
						</div>
					</div>
				))}
			</div>
		</section>
	);
}

function TaskRow({
	task,
	overdue,
	done,
	compact,
}: {
	task: Task;
	overdue?: boolean;
	done?: boolean;
	compact?: boolean;
}) {
	const queryClient = useQueryClient();
	const mutation = useMutation({
		mutationFn: () => api.updateTask(task.id, { status: "done" }),
		onSuccess: () => queryClient.invalidateQueries({ queryKey: ["brief"] }),
	});
	return (
		<Link
			to="/t/$taskId"
			params={{ taskId: task.id }}
			className={cn(
				"group flex items-center gap-3 px-4 py-3.5 transition hover:bg-muted/45",
				compact && "px-0 py-2.5",
				done && "opacity-55",
			)}
		>
			<button
				className={cn(
					"grid size-5 shrink-0 place-items-center rounded-full border transition",
					done
						? "border-[var(--sage)] bg-[var(--sage)] text-white"
						: "border-muted-foreground/50 hover:border-[var(--coral)] hover:text-[var(--coral)]",
				)}
				onClick={(event) => {
					event.preventDefault();
					mutation.mutate();
				}}
				aria-label={`Mark ${task.title} as done`}
			>
				{done ? (
					<Check className="size-3" />
				) : mutation.isPending ? (
					<LoaderCircle className="size-3 animate-spin" />
				) : null}
			</button>
			<span className="min-w-0 flex-1">
				<span className="block truncate text-sm font-medium">{task.title}</span>
				<span className="mt-1 flex flex-wrap gap-x-2 text-xs text-muted-foreground">
					{task.project_id ? <span>Project</span> : null}
					{task.kind === "recurring" ? <span>Repeats</span> : null}
					{task.delegated_to_name ? (
						<span>→ {task.delegated_to_name}</span>
					) : null}
				</span>
			</span>
			{task.estimate_minutes ? (
				<span className="hidden text-xs tabular-nums text-muted-foreground sm:block">
					{formatMinutes(task.estimate_minutes)}
				</span>
			) : null}
			{overdue && task.due_on ? (
				<span className="rounded-md bg-[var(--overdue-bg)] px-2 py-1 text-xs font-medium tabular-nums text-[var(--overdue)]">
					due {formatDate(task.due_on)} · {daysLate(task.due_on)}d
				</span>
			) : task.due_on ? (
				<span className="hidden text-xs tabular-nums text-muted-foreground sm:block">
					due {formatDate(task.due_on)}
				</span>
			) : null}
		</Link>
	);
}

function CaptureDialog({
	open,
	onOpenChange,
	contexts,
	people,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	contexts: Context[];
	people: Person[];
}) {
	const queryClient = useQueryClient();
	const [value, setValue] = useState("");
	const parsed = useMemo(
		() => parseCapture(value, contexts, people),
		[value, contexts, people],
	);
	const create = useMutation({
		mutationFn: () =>
			api.createTask({
				title: parsed.title || value.trim(),
				context_id: parsed.context?.id ?? null,
				status: parsed.person ? "delegated" : parsed.context ? "todo" : "inbox",
				delegated_to_id: parsed.person?.id,
				source: parsed.source,
				estimate_minutes: parsed.estimate_minutes,
				due_on: parsed.due_on,
				planned_on: parsed.planned_on,
				capture_method: "form",
			}),
		onSuccess: () => {
			setValue("");
			onOpenChange(false);
			queryClient.invalidateQueries({ queryKey: ["brief"] });
		},
	});
	const chips = [
		parsed.context && `# ${parsed.context.name}`,
		parsed.person && `@ ${parsed.person.name}`,
		parsed.source && `! ${parsed.source}`,
		parsed.estimate_minutes && formatMinutes(parsed.estimate_minutes),
		parsed.due_on && `Due ${formatDate(parsed.due_on)}`,
		parsed.planned_on && `Plan ${formatDate(parsed.planned_on)}`,
	].filter(Boolean);
	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent className="max-w-2xl overflow-hidden border-none bg-transparent p-0 shadow-2xl">
				<div className="rounded-3xl border border-border bg-card p-3">
					<div className="flex items-center gap-3 px-3 pt-2 text-sm text-muted-foreground">
						<Command className="size-4 text-[var(--coral)]" />
						Quick capture <span className="ml-auto text-xs">Esc to close</span>
					</div>
					<Textarea
						autoFocus
						value={value}
						onChange={(event) => setValue(event.target.value)}
						onKeyDown={(event) => {
							if (event.key === "Enter" && !event.shiftKey) {
								event.preventDefault();
								if (parsed.title || value.trim()) create.mutate();
							}
						}}
						placeholder="What needs your attention?"
						className="min-h-30 resize-none border-0 bg-transparent px-3 text-xl shadow-none focus-visible:ring-0"
					/>
					{chips.length || parsed.unresolved.length ? (
						<div className="flex flex-wrap gap-2 border-t border-border px-3 py-3">
							{chips.map((chip) => (
								<Badge
									key={chip as string}
									variant="secondary"
									className="bg-[var(--sand)] font-normal text-[var(--ink)]"
								>
									{chip}
								</Badge>
							))}
							{parsed.unresolved.map((chip) => (
								<Badge
									key={chip}
									variant="outline"
									className="border-[var(--overdue)] text-[var(--overdue)]"
								>
									Resolve {chip}
								</Badge>
							))}
						</div>
					) : (
						<div className="border-t border-border px-3 py-3 text-xs text-muted-foreground">
							Try <kbd>#upsun</kbd> <kbd>@marc</kbd> <kbd>!slack</kbd>{" "}
							<kbd>30m</kbd> <kbd>tomorrow</kbd> or <kbd>&gt;tomorrow</kbd>
						</div>
					)}
					<div className="flex items-center justify-between px-3 pb-2 pt-1">
						<Button variant="ghost" size="sm" className="text-muted-foreground">
							More details
						</Button>
						<Button
							disabled={!value.trim() || create.isPending}
							onClick={() => create.mutate()}
							className="gap-2 bg-[var(--coral)] hover:bg-[var(--coral-deep)]"
						>
							{create.isPending ? (
								<LoaderCircle className="size-4 animate-spin" />
							) : null}
							Add to {parsed.context?.name ?? "Inbox"}
							<kbd className="rounded bg-white/15 px-1.5 text-[10px]">↵</kbd>
						</Button>
					</div>
				</div>
			</DialogContent>
		</Dialog>
	);
}

function InboxPage({
	contexts,
	people,
}: {
	contexts: Context[];
	people: Person[];
}) {
	const queryClient = useQueryClient();
	const query = useQuery({
		queryKey: ["inbox"],
		queryFn: () =>
			api.tasks(
				new URLSearchParams({
					context_id: "null",
					status: "inbox",
					sort: "created_at",
					order: "asc",
					limit: "200",
				}),
			),
	});
	const [focus, setFocus] = useState(false);
	const [draft, setDraft] = useState("");
	const create = useMutation({
		mutationFn: () =>
			api.createTask({
				title: draft,
				status: "inbox",
				context_id: null,
				capture_method: "form",
			}),
		onSuccess: () => {
			setDraft("");
			queryClient.invalidateQueries({ queryKey: ["inbox"] });
			queryClient.invalidateQueries({ queryKey: ["brief"] });
		},
	});
	const tasks = query.data?.data ?? [];
	if (focus && tasks.length)
		return (
			<TriageCard
				task={tasks[0]}
				contexts={contexts}
				people={people}
				total={tasks.length}
				onFinish={() => setFocus(false)}
			/>
		);
	return (
		<section>
			<div className="mb-8 flex items-end justify-between">
				<div>
					<p className="mb-2 text-sm font-medium text-muted-foreground">
						Capture first. Decide later.
					</p>
					<h1 className="font-display text-4xl tracking-tight">Inbox</h1>
				</div>
				{tasks.length ? (
					<Button variant="outline" onClick={() => setFocus(true)}>
						Triage all <ArrowRight className="ml-2 size-4" />
					</Button>
				) : null}
			</div>
			<div className="mb-6 flex rounded-2xl border border-border bg-card p-2">
				<Input
					value={draft}
					onChange={(event) => setDraft(event.target.value)}
					onKeyDown={(event) => {
						if (event.key === "Enter" && draft.trim()) create.mutate();
					}}
					placeholder="Add a thought before it gets away…"
					className="border-0 shadow-none focus-visible:ring-0"
				/>
				<Button
					disabled={!draft.trim()}
					onClick={() => create.mutate()}
					className="rounded-xl bg-[var(--coral)] hover:bg-[var(--coral-deep)]"
				>
					<Plus className="size-4" />
				</Button>
			</div>
			{query.isLoading ? (
				<LoadingPage />
			) : tasks.length ? (
				<div className="overflow-hidden rounded-2xl border border-border bg-card">
					{tasks.map((task) => (
						<TaskRow key={task.id} task={task} />
					))}
				</div>
			) : (
				<EmptyPage
					title="Your inbox is clear"
					description="New tasks land here until you're ready to give them a home."
				/>
			)}
		</section>
	);
}

function TriageCard({
	task,
	contexts,
	people,
	total,
	onFinish,
}: {
	task: Task;
	contexts: Context[];
	people: Person[];
	total: number;
	onFinish: () => void;
}) {
	const queryClient = useQueryClient();
	const [contextId, setContextId] = useState<string>();
	const [due, setDue] = useState<string>();
	const update = useMutation({
		mutationFn: (body: Record<string, unknown>) =>
			api.updateTask(task.id, body),
		onSuccess: () => {
			queryClient.invalidateQueries({ queryKey: ["inbox"] });
			queryClient.invalidateQueries({ queryKey: ["brief"] });
		},
	});
	const commit = (status: "todo" | "blocked" | "delegated" = "todo") => {
		if (status === "delegated" && !people[0]) return;
		update.mutate({
			status,
			context_id: contextId ?? null,
			due_on: due ?? null,
			...(status === "delegated" ? { delegated_to_id: people[0].id } : {}),
		});
	};
	if (update.isSuccess)
		return <InboxPage contexts={contexts} people={people} />;
	return (
		<section className="mx-auto max-w-3xl">
			<div className="mb-8 flex items-center justify-between text-sm text-muted-foreground">
				<button onClick={onFinish} className="hover:text-foreground">
					<ArrowLeft className="mr-1 inline size-4" />
					Inbox
				</button>
				<span>Triage · 1 of {total}</span>
				<button onClick={onFinish}>Done</button>
			</div>
			<article className="rounded-3xl border border-border bg-card p-7 shadow-sm sm:p-10">
				<p className="mb-4 text-sm text-muted-foreground">
					Captured {formatDate(task.created_at.slice(0, 10))} · via{" "}
					{task.capture_method}
				</p>
				<h1 className="font-display text-3xl leading-tight tracking-tight sm:text-4xl">
					{task.title}
				</h1>
				<div className="mt-10">
					<p className="mb-3 text-sm font-medium">Context</p>
					<div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
						{contexts.map((context, index) => (
							<button
								key={context.id}
								onClick={() => setContextId(context.id)}
								className={cn(
									"rounded-xl border px-3 py-3 text-left text-sm transition",
									contextId === context.id
										? "border-[var(--coral)] bg-[var(--sand)]"
										: "border-border hover:bg-muted",
								)}
							>
								<span
									className="mr-2 inline-block size-2 rounded-full"
									style={{
										backgroundColor:
											context.color ??
											contextPalette[index % contextPalette.length],
									}}
								/>
								{context.name}
							</button>
						))}
					</div>
				</div>
				<div className="mt-7">
					<p className="mb-3 text-sm font-medium">When</p>
					<div className="flex flex-wrap gap-2">
						{[
							["Today", todayString()],
							["Tomorrow", dateOffset(todayString(), 1)],
							["No date", ""],
						].map(([label, value]) => (
							<Button
								key={label}
								variant={due === value ? "default" : "outline"}
								className={cn(due === value && "bg-[var(--ink)]")}
								onClick={() => setDue(value)}
							>
								{label}
							</Button>
						))}
					</div>
				</div>
				<div className="mt-10 flex flex-wrap gap-3">
					<Button
						className="bg-[var(--coral)] hover:bg-[var(--coral-deep)]"
						onClick={() => commit()}
						disabled={update.isPending}
					>
						To do <span className="ml-6 text-white/70">↵</span>
					</Button>
					<Button
						variant="outline"
						onClick={() => commit("delegated")}
						disabled={!people.length}
					>
						Delegate
					</Button>
					<Button variant="outline" onClick={() => commit("blocked")}>
						Blocked
					</Button>
					<Button
						variant="ghost"
						className="ml-auto text-muted-foreground"
						onClick={onFinish}
					>
						Skip
					</Button>
				</div>
			</article>
		</section>
	);
}

function TaskListPage() {
	const [query, setQuery] = useState("");
	const [status, setStatus] = useState("");
	const parameters = new URLSearchParams({
		limit: "200",
		sort: "due_on",
		order: "asc",
	});
	if (query) parameters.set("q", query);
	if (status) parameters.set("status", status);
	const tasks = useQuery({
		queryKey: ["tasks", query, status],
		queryFn: () => api.tasks(parameters),
	});
	return (
		<section>
			<div className="mb-8">
				<p className="mb-2 text-sm font-medium text-muted-foreground">
					Everything, with an honest order.
				</p>
				<h1 className="font-display text-4xl tracking-tight">Tasks</h1>
			</div>
			<div className="mb-5 flex flex-col gap-2 rounded-2xl border border-border bg-card p-3 sm:flex-row">
				<div className="flex flex-1 items-center gap-2">
					<Search className="ml-2 size-4 text-muted-foreground" />
					<Input
						value={query}
						onChange={(event) => setQuery(event.target.value)}
						placeholder="Search title and details"
						className="border-0 shadow-none focus-visible:ring-0"
					/>
				</div>
				<select
					value={status}
					onChange={(event) => setStatus(event.target.value)}
					className="h-9 rounded-lg border border-border bg-background px-3 text-sm"
				>
					<option value="">Any status</option>
					<option value="todo,in_progress">Open work</option>
					<option value="blocked">Blocked</option>
					<option value="delegated">Waiting on</option>
					<option value="done">Done</option>
					<option value="cancelled">Cancelled</option>
				</select>
			</div>
			<p className="mb-3 text-xs text-muted-foreground">
				{tasks.data?.data.length ?? 0} tasks · sorted by due date · undated
				tasks last
			</p>
			{tasks.isLoading ? (
				<LoadingPage />
			) : tasks.data?.data.length ? (
				<div className="overflow-hidden rounded-2xl border border-border bg-card">
					{tasks.data.data.map((task) => (
						<TaskRow key={task.id} task={task} />
					))}
				</div>
			) : (
				<EmptyPage
					title="Nothing matches these filters"
					description="Try clearing a filter or searching a different phrase."
				/>
			)}
		</section>
	);
}

function WaitingPage({ brief }: { brief: Brief }) {
	return (
		<section>
			<div className="mb-8">
				<p className="mb-2 text-sm font-medium text-muted-foreground">
					People, not loose ends.
				</p>
				<h1 className="font-display text-4xl tracking-tight">Waiting on</h1>
			</div>
			{brief.waiting_on.length ? (
				<WaitingSection groups={brief.waiting_on} />
			) : (
				<EmptyPage
					title="No one is holding up your day"
					description="Delegated work appears here, grouped by the person you need to follow up with."
				/>
			)}
		</section>
	);
}

function ContextPage({
	contexts,
	projects,
	pathname,
}: {
	contexts: Context[];
	projects: { id: string; context_id: string; name: string; status: string }[];
	pathname: string;
}) {
	const slug = pathname.split("/").pop();
	const context = contexts.find((candidate) => candidate.slug === slug);
	const tasks = useQuery({
		queryKey: ["context-tasks", context?.id],
		queryFn: () =>
			api.tasks(
				new URLSearchParams({
					context_id: context?.id ?? "",
					status: "todo,in_progress,blocked,delegated",
					limit: "200",
					sort: "due_on",
					order: "asc",
				}),
			),
		enabled: Boolean(context),
	});
	if (!context)
		return (
			<EmptyPage
				title="This context no longer exists"
				description="It may have been removed on another device."
			/>
		);
	const color =
		context.color ??
		contextPalette[contexts.indexOf(context) % contextPalette.length];
	return (
		<section>
			<div className="mb-9 rounded-3xl bg-[var(--ink)] px-7 py-8 text-white">
				<span
					className="mb-5 block size-3 rounded-full"
					style={{ backgroundColor: color }}
				/>
				<p className="mb-2 text-sm text-white/60">One life at a time</p>
				<h1 className="font-display text-4xl tracking-tight">{context.name}</h1>
				<div className="mt-7 flex gap-7 text-sm">
					<span>
						<b className="tabular-nums">
							{tasks.data?.data.filter(
								(task) => task.due_on && task.due_on < todayString(),
							).length ?? 0}
						</b>{" "}
						overdue
					</span>
					<span>
						<b className="tabular-nums">
							{tasks.data?.data.filter((task) => task.due_on === todayString())
								.length ?? 0}
						</b>{" "}
						due today
					</span>
				</div>
			</div>
			<div className="mb-6 flex items-center justify-between">
				<h2 className="font-display text-2xl">Projects</h2>
				<Button variant="outline" size="sm">
					<Plus className="mr-1 size-4" />
					New project
				</Button>
			</div>
			{projects.filter((project) => project.context_id === context.id)
				.length ? (
				<div className="mb-10 grid gap-3 sm:grid-cols-2">
					{projects
						.filter((project) => project.context_id === context.id)
						.map((project) => (
							<Link
								key={project.id}
								to="/p/$projectId"
								params={{ projectId: project.id }}
								className="rounded-2xl border border-border bg-card p-5 transition hover:border-[var(--coral)]"
							>
								<p className="font-medium">{project.name}</p>
								<p className="mt-4 text-xs text-muted-foreground">
									{project.status === "paused"
										? "Paused"
										: "No completed tasks yet"}
								</p>
							</Link>
						))}
				</div>
			) : (
				<p className="mb-10 rounded-xl bg-muted/50 px-4 py-3 text-sm text-muted-foreground">
					This context has no projects.
				</p>
			)}
			<h2 className="mb-3 font-display text-2xl">Open tasks</h2>
			{tasks.data?.data.length ? (
				<div className="overflow-hidden rounded-2xl border border-border bg-card">
					{tasks.data.data.map((task) => (
						<TaskRow key={task.id} task={task} />
					))}
				</div>
			) : (
				<EmptyPage
					title="No tasks here yet"
					description="Add a task when something in this part of life needs attention."
				/>
			)}
		</section>
	);
}

function ProjectPage({
	projects,
	pathname,
}: {
	projects: {
		id: string;
		context_id: string;
		name: string;
		description?: string | null;
		status: string;
	}[];
	pathname: string;
}) {
	const id = pathname.split("/").pop();
	const project = projects.find((candidate) => candidate.id === id);
	const tasks = useQuery({
		queryKey: ["project-tasks", project?.id],
		queryFn: () =>
			api.tasks(
				new URLSearchParams({
					project_id: project?.id ?? "",
					top_level: "true",
					limit: "200",
					sort: "due_on",
					order: "asc",
				}),
			),
		enabled: Boolean(project),
	});
	if (!project)
		return (
			<EmptyPage
				title="This project no longer exists"
				description="It may have been archived or removed elsewhere."
			/>
		);
	return (
		<section>
			<Link
				to="/tasks"
				className="mb-5 inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
			>
				<ChevronLeft className="size-4" />
				All tasks
			</Link>
			<div className="mb-9">
				<p className="mb-2 text-sm text-muted-foreground">{project.status}</p>
				<h1 className="font-display text-4xl tracking-tight">{project.name}</h1>
				{project.description ? (
					<p className="mt-3 max-w-2xl text-muted-foreground">
						{project.description}
					</p>
				) : null}
			</div>
			<div className="mb-4 flex items-center justify-between">
				<h2 className="font-display text-2xl">Tasks</h2>
				<Button variant="outline" size="sm">
					<Plus className="mr-1 size-4" />
					Add task
				</Button>
			</div>
			{tasks.data?.data.length ? (
				<div className="overflow-hidden rounded-2xl border border-border bg-card">
					{tasks.data.data.map((task) => (
						<TaskRow key={task.id} task={task} />
					))}
				</div>
			) : (
				<EmptyPage
					title="No tasks yet"
					description="Start with the smallest next step."
				/>
			)}
		</section>
	);
}

function SettingsPage({
	me,
}: {
	me?: { name: string; email: string; timezone: string };
}) {
	return (
		<section className="max-w-2xl">
			<p className="mb-2 text-sm font-medium text-muted-foreground">
				Your space, your rules.
			</p>
			<h1 className="mb-8 font-display text-4xl tracking-tight">Settings</h1>
			<div className="overflow-hidden rounded-2xl border border-border bg-card">
				<SettingsRow
					label="Account"
					value={me ? `${me.name} · ${me.email}` : "Loading…"}
				/>
				<SettingsRow label="Timezone" value={me?.timezone ?? "—"} />
				<SettingsRow label="Appearance" value="System" />
				<SettingsRow label="Sync" value="Up to date" />
				<SettingsRow label="Access tokens" value="Manage connected devices" />
			</div>
		</section>
	);
}
function SettingsRow({ label, value }: { label: string; value: string }) {
	return (
		<button className="flex w-full items-center justify-between border-b border-border px-5 py-4 text-left last:border-0 hover:bg-muted/40">
			<span className="font-medium">{label}</span>
			<span className="flex items-center gap-2 text-sm text-muted-foreground">
				{value}
				<ChevronRight className="size-4" />
			</span>
		</button>
	);
}

function TaskDetail({
	taskId,
	contexts,
	people,
	onClose,
}: {
	taskId: string;
	contexts: Context[];
	people: Person[];
	onClose: () => void;
}) {
	const queryClient = useQueryClient();
	const taskQuery = useQuery({
		queryKey: ["task", taskId],
		queryFn: () => api.task(taskId),
	});
	const [editing, setEditing] = useState(false);
	const [title, setTitle] = useState("");
	const update = useMutation({
		mutationFn: (body: Record<string, unknown>) => api.updateTask(taskId, body),
		onSuccess: (task) => {
			queryClient.setQueryData(["task", taskId], task);
			queryClient.invalidateQueries({ queryKey: ["brief"] });
			setEditing(false);
		},
	});
	const task = taskQuery.data;
	useEffect(() => {
		if (task) setTitle(task.title);
	}, [task]);
	return (
		<Sheet open onOpenChange={(open) => !open && onClose()}>
			<SheetContent className="w-full overflow-y-auto border-l border-border bg-card p-0 sm:max-w-xl">
				<div className="sticky top-0 z-10 flex items-center justify-between border-b border-border bg-card/90 px-5 py-4 backdrop-blur">
					<Button
						variant="ghost"
						size="icon"
						onClick={onClose}
						aria-label="Close task"
					>
						<X />
					</Button>
					<div className="flex gap-1">
						<Button variant="ghost" size="icon">
							<MoreHorizontal />
						</Button>
					</div>
				</div>
				{taskQuery.isLoading ? (
					<LoadingPage />
				) : task ? (
					<div className="p-6">
						<div className="flex gap-3">
							<button
								className={cn(
									"mt-1 grid size-6 shrink-0 place-items-center rounded-full border",
									task.status === "done"
										? "border-[var(--sage)] bg-[var(--sage)] text-white"
										: "border-muted-foreground/50",
								)}
								onClick={() =>
									update.mutate({
										status: task.status === "done" ? "todo" : "done",
									})
								}
							>
								{task.status === "done" ? <Check className="size-4" /> : null}
							</button>
							<div className="min-w-0 flex-1">
								{editing ? (
									<Input
										autoFocus
										value={title}
										onChange={(event) => setTitle(event.target.value)}
										onKeyDown={(event) => {
											if (event.key === "Enter" && title.trim())
												update.mutate({ title });
										}}
										onBlur={() => title.trim() && update.mutate({ title })}
										className="h-auto px-0 font-display text-3xl shadow-none"
									/>
								) : (
									<button
										className="text-left font-display text-3xl leading-tight tracking-tight"
										onClick={() => setEditing(true)}
									>
										{task.title}
									</button>
								)}
							</div>
						</div>
						<div className="mt-8 divide-y divide-border rounded-2xl border border-border">
							<Field label="Status">
								<select
									value={task.status}
									onChange={(event) =>
										update.mutate({ status: event.target.value })
									}
									className="bg-transparent text-sm outline-none"
								>
									<option value="inbox">Inbox</option>
									<option value="todo">To do</option>
									<option value="in_progress">In progress</option>
									<option value="blocked">Blocked</option>
									<option value="delegated">Waiting on</option>
									<option value="done">Done</option>
									<option value="cancelled">Cancelled</option>
								</select>
							</Field>
							<Field label="Context">
								<select
									value={task.context_id ?? ""}
									onChange={(event) =>
										update.mutate({
											context_id: event.target.value || null,
											project_id: null,
										})
									}
									className="max-w-52 bg-transparent text-right text-sm outline-none"
								>
									<option value="">No context</option>
									{contexts.map((context) => (
										<option key={context.id} value={context.id}>
											{context.name}
										</option>
									))}
								</select>
							</Field>
							<DateField
								label="Due"
								value={task.due_on}
								onChange={(value) => update.mutate({ due_on: value })}
							/>
							<DateField
								label="Planned"
								value={task.planned_on}
								onChange={(value) => update.mutate({ planned_on: value })}
							/>
							<Field label="Estimate">
								{task.estimate_minutes ? (
									<button
										onClick={() => update.mutate({ estimate_minutes: null })}
										className="text-sm text-muted-foreground"
									>
										{formatMinutes(task.estimate_minutes)} ×
									</button>
								) : (
									<div className="flex gap-1">
										{[15, 30, 60].map((minutes) => (
											<button
												key={minutes}
												onClick={() =>
													update.mutate({ estimate_minutes: minutes })
												}
												className="rounded-md bg-muted px-2 py-1 text-xs"
											>
												{formatMinutes(minutes)}
											</button>
										))}
									</div>
								)}
							</Field>
							<Field label="Delegate">
								<select
									value={task.delegated_to_id ?? ""}
									onChange={(event) =>
										update.mutate(
											event.target.value
												? {
														delegated_to_id: event.target.value,
														status: "delegated",
													}
												: {
														delegated_to_id: null,
														status:
															task.status === "delegated"
																? "todo"
																: task.status,
													},
										)
									}
									className="max-w-52 bg-transparent text-right text-sm outline-none"
								>
									<option value="">Nobody</option>
									{people.map((person) => (
										<option key={person.id} value={person.id}>
											{person.name}
										</option>
									))}
								</select>
							</Field>
						</div>
						<div className="mt-8">
							<p className="mb-2 text-sm font-medium">Details</p>
							<Textarea
								defaultValue={task.details ?? ""}
								onBlur={(event) =>
									event.target.value !== (task.details ?? "") &&
									update.mutate({ details: event.target.value || null })
								}
								placeholder="Add a little useful context…"
								className="min-h-32 rounded-2xl bg-muted/50"
							/>
						</div>
						<p className="mt-8 text-xs text-muted-foreground">
							Captured {formatDate(task.created_at.slice(0, 10))} via{" "}
							{task.capture_method} · updated{" "}
							{new Intl.DateTimeFormat(undefined, {
								hour: "numeric",
								minute: "2-digit",
							}).format(new Date(task.updated_at))}
						</p>
					</div>
				) : (
					<EmptyPage
						title="This task no longer exists"
						description="It may have been deleted on another device."
					/>
				)}
			</SheetContent>
		</Sheet>
	);
}
function Field({
	label,
	children,
}: {
	label: string;
	children: React.ReactNode;
}) {
	return (
		<div className="flex min-h-13 items-center justify-between gap-5 px-4 py-3">
			<span className="shrink-0 text-sm text-muted-foreground">{label}</span>
			{children}
		</div>
	);
}
function DateField({
	label,
	value,
	onChange,
}: {
	label: string;
	value: string | null;
	onChange: (value: string | null) => void;
}) {
	return (
		<Field label={label}>
			<div className="flex items-center gap-2">
				{value ? (
					<button
						onClick={() => onChange(null)}
						className="text-sm text-muted-foreground"
					>
						{formatDate(value)} ×
					</button>
				) : (
					<input
						type="date"
						onChange={(event) =>
							event.target.value && onChange(event.target.value)
						}
						className="w-30 bg-transparent text-right text-sm outline-none"
					/>
				)}
			</div>
		</Field>
	);
}

function PerfectDay({ onCapture }: { onCapture: () => void }) {
	return (
		<div className="rounded-3xl border border-[var(--sage)]/25 bg-[var(--sage-bg)] px-6 py-14 text-center sm:px-12">
			<div className="mx-auto mb-5 grid size-14 place-items-center rounded-2xl bg-[var(--sage)] text-white">
				<Sparkles className="size-6" />
			</div>
			<h2 className="font-display text-3xl tracking-tight">A clear day.</h2>
			<p className="mx-auto mt-3 max-w-md text-sm leading-6 text-muted-foreground">
				Nothing is overdue, due today, or planned. Leave space for what matters,
				or capture a thought when one arrives.
			</p>
			<Button
				variant="outline"
				className="mt-6 border-[var(--sage)]/30 bg-white"
				onClick={onCapture}
			>
				<Plus className="mr-2 size-4" />
				Capture a thought
			</Button>
		</div>
	);
}
function EmptyPage({
	title,
	description,
}: {
	title: string;
	description: string;
}) {
	return (
		<div className="rounded-3xl border border-dashed border-border bg-card/50 px-6 py-16 text-center">
			<CircleDotDashed className="mx-auto mb-4 size-7 text-muted-foreground" />
			<h2 className="font-display text-2xl tracking-tight">{title}</h2>
			<p className="mx-auto mt-2 max-w-md text-sm leading-6 text-muted-foreground">
				{description}
			</p>
		</div>
	);
}
function LoadingPage() {
	return (
		<div className="space-y-3">
			<div className="h-10 w-52 animate-pulse rounded-xl bg-muted" />
			<div className="h-32 animate-pulse rounded-2xl bg-muted" />
			<div className="h-16 animate-pulse rounded-2xl bg-muted" />
			<div className="h-16 animate-pulse rounded-2xl bg-muted" />
		</div>
	);
}
function ErrorState({ onRetry }: { onRetry: () => void }) {
	return (
		<div className="rounded-2xl border border-[var(--overdue)]/30 bg-[var(--overdue-bg)] p-6">
			<h2 className="font-display text-2xl">The brief is unavailable</h2>
			<p className="mt-2 text-sm text-muted-foreground">
				Your last synced data remains readable when it is available. Try again
				when the server is reachable.
			</p>
			<Button className="mt-4" variant="outline" onClick={onRetry}>
				<RefreshCw className="mr-2 size-4" />
				Try again
			</Button>
		</div>
	);
}
function dateOffset(date: string, days: number) {
	const value = new Date(`${date}T12:00:00`);
	value.setDate(value.getDate() + days);
	return new Intl.DateTimeFormat("en-CA", {
		year: "numeric",
		month: "2-digit",
		day: "2-digit",
	}).format(value);
}
