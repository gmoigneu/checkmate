import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useLocation, useNavigate } from "@tanstack/react-router";
import {
	ArrowLeft,
	ArrowRight,
	CalendarDays,
	CheckCheck,
	ChevronDown,
	ChevronLeft,
	ChevronRight,
	type Circle,
	CircleDotDashed,
	Command,
	FileText,
	History,
	Hourglass,
	Inbox,
	LoaderCircle,
	Menu,
	MoreHorizontal,
	OctagonX,
	Plus,
	RefreshCw,
	Repeat2,
	Search,
	Settings,
	Sparkles,
	Sun,
	Sunrise,
	X,
} from "lucide-react";
import {
	useCallback,
	useEffect,
	useMemo,
	useState,
	useSyncExternalStore,
} from "react";
import {
	ContextActionsMenu,
	ContextProjectSettingsPage,
	NewProjectButton,
	ProjectActionsMenu,
} from "@/components/context-project-management";
import { ReportPage } from "@/components/report-page";
import { RoutinePage } from "@/components/routine-page";
import { SettingsPage } from "@/components/settings-page";
import { StatusBadge, TaskStatusMenu } from "@/components/task-status-menu";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Sheet, SheetContent } from "@/components/ui/sheet";
import { Textarea } from "@/components/ui/textarea";
import { ApiError, api } from "@/lib/api";
import {
	type BriefDisplayTask,
	buildBriefSections,
} from "@/lib/brief-sections";
import { parseCapture } from "@/lib/capture";
import {
	type CaptureDefaults,
	captureDefaultsForPath,
} from "@/lib/capture-defaults";
import {
	contextPalette,
	daysLate,
	displayDate,
	formatDate,
	formatMinutes,
	formatTimestamp,
	todayString,
} from "@/lib/format";
import { taskListStatusFilters, taskStatusOptions } from "@/lib/status";
import type {
	Brief,
	Context,
	DaySlot,
	Person,
	Project,
	Task,
	TaskActivity,
	TaskPriority,
	TaskStatus,
} from "@/lib/types";
import { cn } from "@/lib/utils";

type Page =
	| "brief"
	| "inbox"
	| "tasks"
	| "done"
	| "activity"
	| "report"
	| "waiting"
	| "routine"
	| "repeating"
	| "blocked"
	| "settings"
	| "organization"
	| "context"
	| "project";

const navItems: Array<{
	page: Page;
	label: string;
	icon: typeof Circle;
	href: string;
}> = [
	{ page: "brief", label: "Brief", icon: Sun, href: "/" },
	{ page: "inbox", label: "Inbox", icon: Inbox, href: "/inbox" },
	{ page: "tasks", label: "Upcoming", icon: CalendarDays, href: "/tasks" },
	{ page: "done", label: "Done", icon: CheckCheck, href: "/done" },
	{ page: "activity", label: "Activity", icon: History, href: "/activity" },
	{ page: "report", label: "Report", icon: FileText, href: "/report" },
];

const priorityOptions: Array<{ value: TaskPriority; label: string }> = [
	{ value: "urgent", label: "Urgent" },
	{ value: "high", label: "High" },
	{ value: "medium", label: "Medium" },
	{ value: "low", label: "Low" },
];

function refreshTaskQueries(
	queryClient: ReturnType<typeof useQueryClient>,
	task: Task,
) {
	queryClient.setQueryData(["task", task.id], task);
	for (const queryKey of [
		["brief"],
		["inbox"],
		["tasks"],
		["done"],
		["activity"],
		["context-tasks"],
		["project-tasks"],
	]) {
		queryClient.invalidateQueries({ queryKey });
	}
}

function PriorityBadge({ priority }: { priority: TaskPriority }) {
	const label =
		priorityOptions.find((option) => option.value === priority)?.label ??
		priority;
	return (
		<span className={cn("cm-priority", `cm-priority-${priority}`)}>
			{label}
		</span>
	);
}

function briefQueryKey(date: string, contextId?: string) {
	return ["brief", date, contextId ?? "all"] as const;
}

const projectOnboardingDismissedKey =
	"checkmate:onboarding:projects-dismissed:v1";
const projectOnboardingListeners = new Set<() => void>();

function subscribeProjectOnboarding(onStoreChange: () => void) {
	projectOnboardingListeners.add(onStoreChange);
	window.addEventListener("storage", onStoreChange);
	return () => {
		projectOnboardingListeners.delete(onStoreChange);
		window.removeEventListener("storage", onStoreChange);
	};
}

function projectOnboardingSnapshot() {
	return window.localStorage.getItem(projectOnboardingDismissedKey) === "1";
}

function dismissProjectOnboarding() {
	window.localStorage.setItem(projectOnboardingDismissedKey, "1");
	for (const listener of projectOnboardingListeners) listener();
}

function pageForPath(pathname: string): Page {
	if (pathname === "/") return "brief";
	if (pathname.startsWith("/inbox")) return "inbox";
	if (pathname.startsWith("/done")) return "done";
	if (pathname.startsWith("/activity")) return "activity";
	if (pathname.startsWith("/report")) return "report";
	if (pathname.startsWith("/waiting")) return "waiting";
	if (pathname.startsWith("/routine")) return "routine";
	if (pathname.startsWith("/repeating")) return "repeating";
	if (pathname.startsWith("/blocked")) return "blocked";
	if (pathname.startsWith("/settings/contexts")) return "organization";
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
	const [captureDefaults, setCaptureDefaults] = useState<CaptureDefaults>({});
	const [sidebarOpen, setSidebarOpen] = useState(false);
	const page = pageForPath(location.pathname);
	const me = useQuery({ queryKey: ["me"], queryFn: api.me, retry: false });
	const contexts = useQuery({
		queryKey: ["contexts"],
		queryFn: () => api.contexts(),
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
		queryKey: briefQueryKey(todayString()),
		queryFn: () => api.brief(todayString()),
		retry: false,
	});
	const contextualCaptureDefaults = useMemo(
		() =>
			captureDefaultsForPath(
				location.pathname,
				contexts.data?.data ?? [],
				projects.data?.data ?? [],
			),
		[location.pathname, contexts.data?.data, projects.data?.data],
	);
	const openCapture = useCallback(
		(defaults: CaptureDefaults = {}) => {
			setCaptureDefaults({ ...contextualCaptureDefaults, ...defaults });
			setCaptureOpen(true);
		},
		[contextualCaptureDefaults],
	);

	useEffect(() => {
		const handler = (event: KeyboardEvent) => {
			if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
				event.preventDefault();
				openCapture();
			}
			const target = event.target;
			const isTypingTarget =
				target instanceof HTMLElement &&
				(target.matches("input, textarea, select, [contenteditable='true']") ||
					Boolean(target.closest('[role="menu"]')));
			if (
				event.key.toLowerCase() === "c" &&
				!event.metaKey &&
				!event.ctrlKey &&
				!event.altKey &&
				!event.shiftKey &&
				!isTypingTarget
			) {
				openCapture();
			}
		};
		window.addEventListener("keydown", handler);
		return () => window.removeEventListener("keydown", handler);
	}, [openCapture]);

	const needsAuthentication =
		me.error instanceof ApiError && me.error.status === 401;
	useEffect(() => {
		if (!needsAuthentication) return;
		window.location.assign(
			`/signin?redirect_to=${encodeURIComponent(location.pathname)}`,
		);
	}, [location.pathname, needsAuthentication]);
	if (needsAuthentication) return null;

	const isLoading = me.isLoading || contexts.isLoading;
	const invalidate = () =>
		queryClient.invalidateQueries({ queryKey: ["brief"] });
	const appContexts = [...(contexts.data?.data ?? [])].sort(
		(a, b) => a.sort_order - b.sort_order || a.name.localeCompare(b.name),
	);
	const appProjects = projects.data?.data ?? [];
	const appPeople = people.data?.data ?? [];

	const content = () => {
		const loadedBrief = brief.data;
		if (isLoading) return <LoadingPage />;
		if (page === "brief") {
			if (brief.error) return <ErrorState onRetry={invalidate} />;
			if (!loadedBrief) return <LoadingPage />;
			return (
				<BriefPage
					brief={loadedBrief}
					contexts={appContexts}
					projectCount={appProjects.length}
					projectsLoaded={projects.isSuccess}
					onOpenCapture={() => openCapture()}
				/>
			);
		}
		if (page === "inbox")
			return <InboxPage contexts={appContexts} people={appPeople} />;
		if (page === "done") return <DonePage contexts={appContexts} />;
		if (page === "activity") return <ActivityPage />;
		if (page === "report")
			return <ReportPage timezone={me.data?.timezone ?? "UTC"} />;
		if (page === "waiting") return <WaitingPage contexts={appContexts} />;
		if (page === "routine")
			return (
				<RoutinePage
					contexts={appContexts}
					projects={appProjects}
					timezone={me.data?.timezone ?? "UTC"}
					today={todayString(me.data?.timezone)}
				/>
			);
		if (page === "repeating")
			return (
				<TaskListPage
					key="repeating"
					title="Repeating"
					description="Recurring work, in the context where it belongs."
					contexts={appContexts}
					kind="recurring"
				/>
			);
		if (page === "blocked")
			return (
				<TaskListPage
					key="blocked"
					title="Blocked"
					description="Work that needs something else to move first."
					contexts={appContexts}
					fixedStatus="blocked"
				/>
			);
		if (page === "settings")
			return <SettingsPage me={me.data} pathname={location.pathname} />;
		if (page === "organization") return <ContextProjectSettingsPage />;
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
				<ProjectPage
					contexts={appContexts}
					projects={appProjects}
					pathname={location.pathname}
					onAddTask={(project) =>
						openCapture({
							contextId: project.context_id,
							projectId: project.id,
						})
					}
				/>
			);
		return (
			<TaskListPage
				key="upcoming"
				title="Upcoming"
				description="Everything ahead, with an honest order."
				contexts={appContexts}
				defaultStatus="todo,in_progress"
			/>
		);
	};

	return (
		<div className="cm-shell">
			<header className="cm-topbar">
				<Button
					variant="ghost"
					size="icon-sm"
					className="cm-menu-button lg:hidden"
					onClick={() => setSidebarOpen(true)}
					aria-label="Open navigation"
				>
					<Menu />
				</Button>
				<Link to="/" className="cm-brand" aria-label="Checkmate home">
					<span className="cm-brand-mark">
						<span />
						<span />
						<span />
					</span>
					<span>Checkmate</span>
				</Link>
				<button
					type="button"
					className="cm-command"
					onClick={() => openCapture()}
				>
					<Search className="size-3.5" />
					<span>Capture or search</span>
					<kbd>⌘K</kbd>
				</button>
				<div className="cm-topbar-spacer" />
				<button type="button" className="cm-sync" onClick={invalidate}>
					<RefreshCw className="size-3.5" />
					<span>Synced now</span>
				</button>
				<button type="button" className="cm-avatar" aria-label="Account menu">
					{me.data?.name.slice(0, 2).toUpperCase() ?? "CM"}
				</button>
			</header>
			<aside className="hidden lg:block">
				<Sidebar
					page={page}
					brief={brief.data}
					contexts={appContexts}
					projects={appProjects}
					onCapture={() => openCapture()}
				/>
			</aside>
			<Sheet open={sidebarOpen} onOpenChange={setSidebarOpen}>
				<SheetContent
					side="left"
					showCloseButton={false}
					className="w-[248px] border-none bg-[var(--surface-sidebar)] p-0 lg:hidden"
				>
					<Sidebar
						page={page}
						brief={brief.data}
						contexts={appContexts}
						projects={appProjects}
						onCapture={() => openCapture()}
					/>
				</SheetContent>
			</Sheet>
			<main className="cm-main">
				<div className="cm-page">{content()}</div>
			</main>
			<Button
				className="fixed right-5 bottom-5 z-20 size-12 rounded-full bg-[var(--accent)] text-white shadow-[var(--shadow-accent)] hover:bg-[var(--accent-hover)] lg:hidden"
				size="icon"
				onClick={() => openCapture()}
				aria-label="Capture a task"
			>
				<Plus className="size-6" />
			</Button>
			<CaptureDialog
				key={`${captureOpen ? "open" : "closed"}:${captureDefaults.contextId ?? ""}:${captureDefaults.projectId ?? ""}`}
				open={captureOpen}
				onOpenChange={(open) => {
					setCaptureOpen(open);
					if (!open) setCaptureDefaults({});
				}}
				contexts={appContexts}
				projects={appProjects}
				people={appPeople}
				defaultContextId={captureDefaults.contextId}
				defaultProjectId={captureDefaults.projectId}
			/>
			{detailId ? (
				<TaskDetail
					taskId={detailId}
					contexts={appContexts}
					projects={appProjects}
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
		<div className="cm-sidebar">
			<nav className="cm-sidebar-group" aria-label="Primary navigation">
				{navItems.map((item) => (
					<Link
						key={item.page}
						to={item.href}
						className={cn(
							"cm-sidebar-item",
							page === item.page ? "cm-sidebar-item-selected" : "",
						)}
					>
						<span className="cm-sidebar-label">
							<item.icon
								className={cn(
									"size-[17px]",
									item.page === "brief" && "text-[var(--status-today)]",
									item.page === "inbox" && "text-[var(--status-inbox)]",
									item.page === "tasks" && "text-[var(--status-upcoming)]",
								)}
							/>
							{item.label}
						</span>
						<Count
							value={
								item.page === "brief"
									? (brief?.totals.overdue ?? 0) +
										(brief?.totals.due_today ?? 0) +
										(brief?.totals.planned ?? 0)
									: item.page === "inbox"
										? (brief?.totals.inbox ?? 0)
										: 0
							}
						/>
					</Link>
				))}
			</nav>
			<div className="cm-sidebar-gap" />
			<nav className="cm-sidebar-group" aria-label="Contexts and projects">
				<div className="mb-1 flex items-center justify-between px-2">
					<span className="font-mono text-[10px] tracking-[.08em] text-[var(--text-tertiary)] uppercase">
						Contexts
					</span>
					<Link
						to="/settings/contexts"
						className="text-xs text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
					>
						Manage
					</Link>
				</div>
				{contexts.map((context, index) => (
					<div key={context.id}>
						<div className="cm-context-heading">
							<ChevronDown className="size-3 text-[var(--text-tertiary)]" />
							<Link
								to="/c/$slug"
								params={{ slug: context.slug }}
								className={cn(
									"cm-context-link",
									page === "context" && "cm-context-link-selected",
								)}
							>
								<span
									className="cm-context-dot"
									style={{
										backgroundColor:
											context.color ??
											contextPalette[index % contextPalette.length],
									}}
								/>
								<span>{context.name}</span>
							</Link>
						</div>
						{projects
							.filter(
								(project) =>
									project.context_id === context.id &&
									(project.status === "active" || project.status === "paused"),
							)
							.slice(0, 3)
							.map((project) => (
								<Link
									key={project.id}
									to="/p/$projectId"
									params={{ projectId: project.id }}
									className="cm-project-link"
								>
									<span className="cm-progress-pie" />
									{project.name}
								</Link>
							))}
					</div>
				))}
			</nav>
			<div className="cm-sidebar-gap" />
			<nav className="cm-sidebar-group" aria-label="Supporting views">
				<Link
					to="/routine"
					className={cn(
						"cm-sidebar-item",
						page === "routine" && "cm-sidebar-item-selected",
					)}
				>
					<span className="cm-sidebar-label">
						<Sunrise className="size-[17px] text-[var(--status-today)]" />
						Daily Routine
					</span>
					<Count value={brief?.totals.routine_open ?? 0} />
				</Link>
				<Link
					to="/waiting"
					className={cn(
						"cm-sidebar-item",
						page === "waiting" && "cm-sidebar-item-selected",
					)}
				>
					<span className="cm-sidebar-label">
						<Hourglass className="size-[17px] text-[var(--task-delegated-fg)]" />
						Waiting on
					</span>
					<Count value={brief?.totals.waiting_on ?? 0} />
				</Link>
				<Link
					to="/repeating"
					className={cn(
						"cm-sidebar-item",
						page === "repeating" && "cm-sidebar-item-selected",
					)}
				>
					<span className="cm-sidebar-label">
						<Repeat2 className="size-[17px]" />
						Repeating
					</span>
				</Link>
				<Link
					to="/blocked"
					className={cn(
						"cm-sidebar-item",
						page === "blocked" && "cm-sidebar-item-selected",
					)}
				>
					<span className="cm-sidebar-label">
						<OctagonX className="size-[17px] text-[var(--task-blocked-fg)]" />
						Blocked
					</span>
					<Count value={brief?.totals.blocked ?? 0} />
				</Link>
			</nav>
			<div className="cm-sidebar-spacer" />
			<div className="cm-sidebar-footer">
				<button
					type="button"
					className="cm-icon-button"
					onClick={onCapture}
					aria-label="New task"
				>
					<Plus className="size-4" />
				</button>
				<Link to="/settings" className="cm-icon-button" aria-label="Settings">
					<Settings className="size-4" />
				</Link>
			</div>
		</div>
	);
}

function Count({ value }: { value: number }) {
	return value ? (
		<span className="cm-count">{value > 99 ? "99+" : value}</span>
	) : null;
}

function ContextFilter({
	contexts,
	value,
	onChange,
	label,
	className = "cm-context-select",
}: {
	contexts: Context[];
	value?: string;
	onChange: (contextId?: string) => void;
	label: string;
	className?: string;
}) {
	return (
		<select
			value={value ?? ""}
			onChange={(event) => onChange(event.target.value || undefined)}
			className={className}
			aria-label={label}
		>
			<option value="">All contexts</option>
			{contexts.map((context) => (
				<option key={context.id} value={context.id}>
					{context.name}
				</option>
			))}
		</select>
	);
}

function BriefPage({
	brief,
	contexts,
	projectCount,
	projectsLoaded,
	onOpenCapture,
}: {
	brief: Brief;
	contexts: Context[];
	projectCount: number;
	projectsLoaded: boolean;
	onOpenCapture: () => void;
}) {
	const [date, setDate] = useState(brief.date || todayString());
	const [contextId, setContextId] = useState<string>();
	const projectPromptDismissed = useSyncExternalStore(
		subscribeProjectOnboarding,
		projectOnboardingSnapshot,
		() => true,
	);
	const query = useQuery({
		queryKey: briefQueryKey(date, contextId),
		queryFn: () => api.brief(date, contextId),
	});
	const data = query.data ?? brief;
	const hasCurrentData = Boolean(query.data);
	const canonical = useMemo(() => buildBriefSections(data), [data]);
	const openWork = canonical.openTaskCount + data.totals.routine_open;
	const completedWork = data.totals.completed_today + data.totals.routine_done;
	const doneRatio =
		completedWork + openWork
			? Math.round((completedWork / (completedWork + openWork)) * 100)
			: 0;
	const perfect = !openWork;
	return (
		<section className="cm-brief">
			<div className="cm-brief-header">
				<h1>{displayDate(data.date)}</h1>
				<div className="cm-brief-actions">
					<div className="cm-day-controls">
						<Button
							variant="ghost"
							size="icon-sm"
							onClick={() => setDate(dateOffset(date, -1))}
							aria-label="Previous day"
						>
							<ChevronLeft className="size-4" />
						</Button>
						<Button
							variant="ghost"
							size="sm"
							className="cm-today-button"
							onClick={() => setDate(todayString())}
						>
							Today
						</Button>
						<Button
							variant="ghost"
							size="icon-sm"
							onClick={() => setDate(dateOffset(date, 1))}
							aria-label="Next day"
						>
							<ChevronRight className="size-4" />
						</Button>
					</div>
					{contexts.length ? (
						<ContextFilter
							contexts={contexts}
							value={contextId}
							onChange={setContextId}
							label="Filter the brief by context"
						/>
					) : null}
				</div>
			</div>
			{!contexts.length ? (
				<OrganizationOnboardingCard stage="context" />
			) : projectsLoaded && projectCount === 0 && !projectPromptDismissed ? (
				<OrganizationOnboardingCard
					stage="project"
					onDismiss={dismissProjectOnboarding}
				/>
			) : null}
			{!hasCurrentData ? (
				<LoadingPage />
			) : perfect ? (
				<PerfectDay onCapture={onOpenCapture} />
			) : (
				<>
					<div className="cm-summary">
						<div className="cm-summary-stats">
							<span className="cm-summary-stat">
								<strong className="text-[var(--task-overdue-fg)]">
									{data.totals.overdue}
								</strong>
								<span>overdue</span>
							</span>
							<i>·</i>
							<span className="cm-summary-stat">
								<strong>{data.totals.due_today}</strong>
								<span>due today</span>
							</span>
							<i>·</i>
							<span className="cm-summary-stat">
								<strong>{data.totals.planned}</strong>
								<span>planned</span>
							</span>
							<i>·</i>
							<span className="cm-summary-stat">
								<strong>{data.totals.routine_open}</strong>
								<span>routine</span>
							</span>
							<i>·</i>
							<span className="cm-summary-stat">
								<strong>{formatMinutes(data.totals.planned_minutes)}</strong>
								{data.totals.planned_without_estimate
									? ` (${data.totals.planned_without_estimate} without estimate)`
									: " planned work"}
							</span>
						</div>
						<div className="cm-summary-progress">
							<div className="cm-progress-track">
								<div
									className="cm-progress-value"
									style={{ width: `${doneRatio}%` }}
								/>
							</div>
							<span>
								{completedWork} of {completedWork + openWork} done today
							</span>
						</div>
					</div>
					<BriefSection
						title="Overdue"
						count={data.totals.overdue}
						tasks={canonical.overdue}
						tone="overdue"
						contexts={contexts}
					/>
					<RoutineBriefSection
						tasks={data.routine}
						contexts={contexts}
						total={data.totals.routine}
						done={data.totals.routine_done}
					/>
					<BriefSection
						title="Due today"
						count={data.totals.due_today}
						tasks={canonical.dueToday}
						note="unestimated first"
						contexts={contexts}
					/>
					<BriefSection
						title="Planned today"
						count={data.totals.planned}
						tasks={canonical.planned}
						note={`${data.totals.planned_without_estimate} without estimate`}
						contexts={contexts}
					/>
					<BriefSection
						title="In progress"
						count={data.totals.in_progress}
						tasks={canonical.inProgress}
						tone="in-progress"
						contexts={contexts}
					/>
					<WaitingSection groups={canonical.waitingOn} contexts={contexts} />
					<BriefSection
						title="Blocked"
						count={data.totals.blocked}
						tasks={canonical.blocked}
						tone="blocked"
						contexts={contexts}
					/>
					<BriefSection
						title="Inbox"
						count={data.totals.inbox}
						tasks={canonical.inbox.slice(0, 2)}
						note={contextId ? "all contexts" : undefined}
						contexts={contexts}
						action={
							<Link to="/inbox" className="cm-section-action">
								Triage all <ArrowRight className="size-3.5" />
							</Link>
						}
						empty="Your inbox is clear"
					/>
					<BriefSection
						title="Done today"
						count={data.totals.completed_today}
						tasks={data.completed_today}
						done
						tone="done"
						contexts={contexts}
					/>
				</>
			)}
		</section>
	);
}

function OrganizationOnboardingCard({
	stage,
	onDismiss,
}: {
	stage: "context" | "project";
	onDismiss?: () => void;
}) {
	const contextStage = stage === "context";
	return (
		<div className="mt-6 flex flex-col gap-5 rounded-2xl border border-[var(--terracotta-300)]/50 bg-[var(--sand)] px-5 py-5 sm:flex-row sm:items-center">
			<div className="min-w-0 flex-1">
				<p className="font-display text-xl tracking-tight">
					{contextStage
						? "Give your work a little structure"
						: "Group related work into projects"}
				</p>
				<p className="mt-1 text-sm leading-6 text-muted-foreground">
					{contextStage
						? "Contexts separate the different parts of your life, while your Inbox stays available for quick capture."
						: "Projects live inside a context and keep related tasks together. They are optional."}
				</p>
			</div>
			<div className="flex shrink-0 items-center gap-2">
				{onDismiss ? (
					<Button variant="ghost" onClick={onDismiss}>
						Not now
					</Button>
				) : null}
				<Button asChild>
					<Link to="/settings/contexts">
						{contextStage ? "Set up contexts" : "Add a project"}
					</Link>
				</Button>
			</div>
		</div>
	);
}

const daySlotLabels: Record<DaySlot, string> = {
	morning: "Morning",
	midday: "Midday",
	afternoon: "Afternoon",
	evening: "Evening",
	night: "Night",
};

function RoutineBriefSection({
	tasks,
	contexts,
	total,
	done,
}: {
	tasks: Task[];
	contexts: Context[];
	total: number;
	done: number;
}) {
	if (!tasks.length) return null;

	return (
		<section className="cm-brief-section cm-routine-brief">
			<div className="cm-section-header">
				<h2>Daily Routine</h2>
				<span className="cm-section-count">
					· {done} of {total} done
				</span>
				<span className="cm-section-spacer" />
				<Link to="/routine" className="cm-section-action">
					Edit routine <ArrowRight className="size-3.5" />
				</Link>
			</div>
			<div className="cm-routine-brief-groups">
				{(Object.keys(daySlotLabels) as DaySlot[]).map((slot) => {
					const slotTasks = tasks.filter((task) => task.day_slot === slot);
					if (!slotTasks.length) return null;

					return (
						<div key={slot} className="cm-routine-brief-group">
							<p>{daySlotLabels[slot]}</p>
							<div className="cm-task-list">
								{slotTasks.map((task) => (
									<TaskRow key={task.id} task={task} contexts={contexts} />
								))}
							</div>
						</div>
					);
				})}
			</div>
		</section>
	);
}

function BriefSection({
	title,
	count,
	tasks,
	tone,
	note,
	done,
	contexts,
	action,
	empty,
}: {
	title: string;
	count: number;
	tasks: BriefDisplayTask[];
	tone?: "overdue" | "blocked" | "done" | "in-progress";
	note?: string;
	done?: boolean;
	contexts?: Context[];
	action?: React.ReactNode;
	empty?: string;
}) {
	const [toggled, setToggled] = useState(false);
	const collapsed = done ? !toggled : toggled;
	if (!count && !empty) return null;
	return (
		<section className="cm-brief-section">
			<div className="cm-section-header">
				<button
					type="button"
					className="cm-section-toggle"
					onClick={() => setToggled(!toggled)}
					aria-expanded={!collapsed}
				>
					<ChevronDown
						className={cn("size-3.5 transition", collapsed && "-rotate-90")}
					/>
					<h2 className={tone ? `cm-tone-${tone}` : undefined}>{title}</h2>
					<span className="cm-section-count">· {count}</span>
				</button>
				{note ? <span className="cm-section-note">{note}</span> : null}
				<span className="cm-section-spacer" />
				{action}
			</div>
			{!collapsed &&
				(tasks.length ? (
					<div className="cm-task-list">
						{tasks.map((task) => (
							<TaskRow
								key={task.id}
								task={task}
								contexts={contexts}
								overdue={tone === "overdue"}
								done={done}
							/>
						))}
					</div>
				) : (
					<p className="cm-section-empty">{empty}</p>
				))}
		</section>
	);
}

function WaitingSection({
	groups,
	contexts,
}: {
	groups: Array<
		Omit<Brief["waiting_on"][number], "tasks"> & {
			tasks: BriefDisplayTask[];
		}
	>;
	contexts?: Context[];
}) {
	const [collapsed, setCollapsed] = useState(false);
	if (!groups.length) return null;
	return (
		<section className="cm-brief-section">
			<div className="cm-section-header">
				<button
					type="button"
					className="cm-section-toggle"
					onClick={() => setCollapsed(!collapsed)}
					aria-expanded={!collapsed}
				>
					<ChevronDown
						className={cn("size-3.5 transition", collapsed && "-rotate-90")}
					/>
					<h2 className="cm-tone-delegated">Waiting on</h2>
					<span className="cm-section-count">
						· {groups.reduce((sum, group) => sum + group.tasks.length, 0)}
					</span>
				</button>
			</div>
			{collapsed ? null : (
				<div className="cm-waiting-groups">
					{groups.map((group) => (
						<div key={group.person_id} className="cm-waiting-group">
							<div className="cm-person-card">
								<span className="cm-person-avatar">
									{group.person_name
										.split(" ")
										.map((part) => part[0])
										.join("")
										.slice(0, 2)
										.toUpperCase()}
								</span>
								<span>{group.person_name}</span>
								<span className="cm-person-count">{group.tasks.length}</span>
								<Button variant="ghost" size="sm" className="cm-follow-up">
									<ArrowRight className="size-3.5" />
									Follow up
								</Button>
							</div>
							<div className="cm-task-list">
								{group.tasks.map((task) => (
									<TaskRow
										key={task.id}
										task={task}
										contexts={contexts}
										compact
									/>
								))}
							</div>
						</div>
					))}
				</div>
			)}
		</section>
	);
}

function TaskRow({
	task,
	overdue,
	done,
	compact,
	contexts,
	showCompletedAt,
}: {
	task: BriefDisplayTask;
	overdue?: boolean;
	done?: boolean;
	compact?: boolean;
	contexts?: Context[];
	showCompletedAt?: boolean;
}) {
	const queryClient = useQueryClient();
	const isDone =
		done ||
		task.status === "done" ||
		task.status === "cancelled" ||
		task.status === "expired";
	const context = contexts?.find(
		(candidate) => candidate.id === task.context_id,
	);
	const contextIndex = context ? (contexts?.indexOf(context) ?? 0) : 0;
	const mutation = useMutation({
		mutationFn: (status: TaskStatus) => api.updateTask(task.id, { status }),
		onSuccess: (updatedTask) => refreshTaskQueries(queryClient, updatedTask),
	});
	return (
		<div
			className={cn(
				"cm-task-row",
				compact && "cm-task-row-compact",
				isDone && "cm-task-row-done",
			)}
		>
			<div className="flex shrink-0 flex-col items-start gap-1">
				<TaskStatusMenu
					status={task.status}
					onStatusChange={(status) => mutation.mutate(status)}
					disabled={mutation.isPending}
					taskTitle={task.title}
					canDelegate={Boolean(task.delegated_to_id)}
				/>
				{mutation.error ? (
					<span
						role="alert"
						className="max-w-36 text-xs leading-tight text-destructive"
					>
						{mutation.error.message}
					</span>
				) : null}
			</div>
			<Link
				to="/t/$taskId"
				params={{ taskId: task.id }}
				className="cm-task-link"
			>
				<span className="cm-task-title">{task.title}</span>
				{task.priority ? <PriorityBadge priority={task.priority} /> : null}
				{task.alsoIn?.length ? (
					<span className="cm-also-in">↳ {task.alsoIn.join(" · ")}</span>
				) : null}
				{task.source ? (
					<span className="cm-task-meta cm-meta-source">{task.source}</span>
				) : null}
				{task.estimate_minutes ? (
					<span className="cm-task-meta cm-estimate">
						{formatMinutes(task.estimate_minutes)}
					</span>
				) : null}
				{task.kind === "recurring" || task.kind === "routine" ? (
					<span className="cm-task-meta">
						<Repeat2 className="size-3" />{" "}
						{task.kind === "routine" ? "Routine" : "Repeats"}
					</span>
				) : null}
				{task.delegated_to_name ? (
					<span className="cm-task-meta cm-delegated">
						<ArrowRight className="size-3" /> {task.delegated_to_name}
					</span>
				) : null}
				{task.day_slot ? (
					<span className="cm-slot-chip">{daySlotLabels[task.day_slot]}</span>
				) : null}
				{task.planned_on ? (
					<span className="cm-date-chip cm-planned-chip">
						Plan {formatDate(task.planned_on)}
					</span>
				) : null}
				{context ? (
					<span className="cm-task-context">
						<span
							className="cm-context-dot"
							style={{
								backgroundColor:
									context.color ??
									contextPalette[contextIndex % contextPalette.length],
							}}
						/>
						<span>{context.name}</span>
					</span>
				) : null}
				{task.due_on ? (
					<span
						className={cn(
							"cm-date-chip cm-due-chip",
							(overdue || task.due_on < todayString()) && "cm-overdue-chip",
							task.due_on === todayString() && "cm-due-today-chip",
						)}
					>
						Due {formatDate(task.due_on)}
						{overdue || task.due_on < todayString()
							? ` · ${daysLate(task.due_on)}d`
							: ""}
					</span>
				) : null}
				{showCompletedAt && task.completed_at ? (
					<time
						dateTime={task.completed_at}
						className="cm-task-meta ml-auto tabular-nums"
					>
						Done {formatTimestamp(task.completed_at)}
					</time>
				) : null}
			</Link>
		</div>
	);
}

function CaptureDialog({
	open,
	onOpenChange,
	contexts,
	projects,
	people,
	defaultContextId,
	defaultProjectId,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	contexts: Context[];
	projects: Project[];
	people: Person[];
	defaultContextId?: string;
	defaultProjectId?: string;
}) {
	const queryClient = useQueryClient();
	const [value, setValue] = useState("");
	const [priority, setPriority] = useState<TaskPriority | "">("");
	const [contextOverrideId, setContextOverrideId] = useState<
		string | null | undefined
	>(undefined);
	const [projectOverrideId, setProjectOverrideId] = useState<
		string | null | undefined
	>(undefined);
	const [statusOverride, setStatusOverride] = useState<TaskStatus>();
	const parsed = useMemo(
		() => parseCapture(value, contexts, people),
		[value, contexts, people],
	);
	const selectedContextId =
		contextOverrideId === undefined
			? (parsed.context?.id ?? defaultContextId)
			: (contextOverrideId ?? undefined);
	const selectedContext = contexts.find(
		(context) => context.id === selectedContextId,
	);
	const candidateProjectId =
		projectOverrideId === undefined
			? defaultProjectId
			: (projectOverrideId ?? undefined);
	const selectedProject = projects.find(
		(project) =>
			project.id === candidateProjectId &&
			project.context_id === selectedContext?.id,
	);
	const availableProjects = projects.filter(
		(project) => project.context_id === selectedContext?.id,
	);
	const automaticStatus: TaskStatus = parsed.person
		? "delegated"
		: selectedContext
			? "todo"
			: "inbox";
	const selectedStatus =
		statusOverride === "delegated" && !parsed.person
			? automaticStatus
			: (statusOverride ?? automaticStatus);
	const create = useMutation({
		mutationFn: () =>
			api.createTask({
				title: parsed.title || value.trim(),
				context_id: selectedContext?.id ?? null,
				project_id: selectedProject?.id ?? null,
				status: selectedStatus,
				delegated_to_id: parsed.person?.id,
				source: parsed.source,
				estimate_minutes: parsed.estimate_minutes,
				due_on: parsed.due_on,
				planned_on: parsed.planned_on,
				priority: priority || null,
				capture_method: "form",
			}),
		onSuccess: (task) => {
			setValue("");
			setPriority("");
			onOpenChange(false);
			refreshTaskQueries(queryClient, task);
		},
	});
	const chips = [
		selectedContext && `# ${selectedContext.name}`,
		selectedProject && `Project: ${selectedProject.name}`,
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
					<div className="flex flex-wrap items-center justify-between gap-3 px-3 pb-2 pt-1">
						<div className="flex flex-wrap items-center gap-2">
							<select
								value={selectedContext?.id ?? ""}
								onChange={(event) => {
									setContextOverrideId(event.target.value || null);
									setProjectOverrideId(null);
								}}
								className="h-8 max-w-40 rounded-md border border-border bg-background px-2 text-xs text-muted-foreground"
								aria-label="Task context"
							>
								<option value="">Inbox / no context</option>
								{contexts.map((context) => (
									<option key={context.id} value={context.id}>
										{context.name}
									</option>
								))}
							</select>
							<select
								value={selectedProject?.id ?? ""}
								onChange={(event) =>
									setProjectOverrideId(event.target.value || null)
								}
								disabled={!selectedContext}
								className="h-8 max-w-40 rounded-md border border-border bg-background px-2 text-xs text-muted-foreground disabled:cursor-not-allowed disabled:opacity-60"
								aria-label="Task project"
							>
								<option value="">
									{selectedContext ? "No project" : "Choose a context first"}
								</option>
								{availableProjects.map((project) => (
									<option key={project.id} value={project.id}>
										{project.name}
									</option>
								))}
							</select>
							<TaskStatusMenu
								status={selectedStatus}
								onStatusChange={setStatusOverride}
								taskTitle={parsed.title || value.trim() || "new task"}
								canDelegate={Boolean(parsed.person)}
							/>
							<select
								value={priority}
								onChange={(event) =>
									setPriority(event.target.value as TaskPriority | "")
								}
								className="h-8 rounded-md border border-border bg-background px-2 text-xs text-muted-foreground"
								aria-label="Task priority"
							>
								<option value="">No priority</option>
								{priorityOptions.map((option) => (
									<option key={option.value} value={option.value}>
										{option.label}
									</option>
								))}
							</select>
						</div>
						<Button
							disabled={!value.trim() || create.isPending}
							onClick={() => create.mutate()}
							className="gap-2 bg-[var(--coral)] hover:bg-[var(--coral-deep)]"
						>
							{create.isPending ? (
								<LoaderCircle className="size-4 animate-spin" />
							) : null}
							Add to {selectedProject?.name ?? selectedContext?.name ?? "Inbox"}
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
				<button
					type="button"
					onClick={onFinish}
					className="hover:text-foreground"
				>
					<ArrowLeft className="mr-1 inline size-4" />
					Inbox
				</button>
				<span>Triage · 1 of {total}</span>
				<button type="button" onClick={onFinish}>
					Done
				</button>
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
								type="button"
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

function CursorPagination({
	page,
	hasPrevious,
	hasNext,
	onPrevious,
	onNext,
}: {
	page: number;
	hasPrevious: boolean;
	hasNext: boolean;
	onPrevious: () => void;
	onNext: () => void;
}) {
	if (!hasPrevious && !hasNext) return null;

	return (
		<nav
			className="mt-5 flex items-center justify-between"
			aria-label="Pagination"
		>
			<Button variant="outline" disabled={!hasPrevious} onClick={onPrevious}>
				<ChevronLeft className="size-4" />
				Previous
			</Button>
			<span className="text-xs text-muted-foreground">Page {page}</span>
			<Button variant="outline" disabled={!hasNext} onClick={onNext}>
				Next
				<ChevronRight className="size-4" />
			</Button>
		</nav>
	);
}

function DonePage({ contexts }: { contexts: Context[] }) {
	const [cursorStack, setCursorStack] = useState([""]);
	const cursor = cursorStack.at(-1) ?? "";
	const tasks = useQuery({
		queryKey: ["done", cursor],
		queryFn: () => {
			const parameters = new URLSearchParams({
				status: "done",
				sort: "completed_at",
				order: "desc",
				limit: "100",
			});
			if (cursor) parameters.set("cursor", cursor);
			return api.tasks(parameters);
		},
	});
	const nextCursor = tasks.data?.next_cursor;

	return (
		<section>
			<div className="mb-8">
				<p className="mb-2 text-sm font-medium text-muted-foreground">
					Finished work, newest first.
				</p>
				<h1 className="font-display text-4xl tracking-tight">Done</h1>
			</div>
			<p className="mb-3 text-xs text-muted-foreground">
				{tasks.data?.data.length ?? 0} tasks on this page · ordered by
				completion time
			</p>
			{tasks.isLoading ? (
				<LoadingPage />
			) : tasks.error ? (
				<TaskQueryError onRetry={() => tasks.refetch()} />
			) : tasks.data?.data.length ? (
				<>
					<div className="overflow-hidden rounded-2xl border border-border bg-card">
						{tasks.data.data.map((task) => (
							<TaskRow
								key={task.id}
								task={task}
								contexts={contexts}
								showCompletedAt
							/>
						))}
					</div>
					<CursorPagination
						page={cursorStack.length}
						hasPrevious={cursorStack.length > 1}
						hasNext={Boolean(nextCursor)}
						onPrevious={() => setCursorStack((stack) => stack.slice(0, -1))}
						onNext={() => {
							if (nextCursor) {
								setCursorStack((stack) => [...stack, nextCursor]);
							}
						}}
					/>
				</>
			) : (
				<EmptyPage
					title="Nothing is done yet"
					description="Tasks appear here when you mark them Done."
				/>
			)}
		</section>
	);
}

const activityActionLabels: Record<TaskActivity["action"], string> = {
	created: "Created",
	updated: "Updated",
	deleted: "Deleted",
	restored: "Restored",
};

const activityFieldLabels: Record<string, string> = {
	blocked_by_id: "blocker",
	capture_method: "capture method",
	context_id: "context",
	day_slot: "day slot",
	delegated_to_id: "delegate",
	details: "details",
	due_on: "due date",
	estimate_minutes: "estimate",
	expired_at: "expiration",
	occurrence_on: "occurrence date",
	parent_id: "parent",
	planned_on: "planned date",
	priority: "priority",
	project_id: "project",
	recurrence_id: "recurrence",
	reference_label: "reference label",
	reference_url: "reference URL",
	slot_order: "slot order",
	source: "source",
	title: "title",
};

function ActivityPage() {
	const [cursorStack, setCursorStack] = useState([""]);
	const cursor = cursorStack.at(-1) ?? "";
	const activity = useQuery({
		queryKey: ["activity", cursor],
		queryFn: () => api.activity(cursor),
	});
	const nextCursor = activity.data?.next_cursor;

	return (
		<section>
			<div className="mb-8">
				<p className="mb-2 text-sm font-medium text-muted-foreground">
					Every change to your tasks, in one place.
				</p>
				<h1 className="font-display text-4xl tracking-tight">Activity</h1>
			</div>
			{activity.isLoading ? (
				<LoadingPage />
			) : activity.error ? (
				<TaskQueryError onRetry={() => activity.refetch()} />
			) : activity.data?.data.length ? (
				<>
					<div className="cm-activity-list">
						{activity.data.data.map((item) => (
							<ActivityRow key={item.id} item={item} />
						))}
					</div>
					<CursorPagination
						page={cursorStack.length}
						hasPrevious={cursorStack.length > 1}
						hasNext={Boolean(nextCursor)}
						onPrevious={() => setCursorStack((stack) => stack.slice(0, -1))}
						onNext={() => {
							if (nextCursor) {
								setCursorStack((stack) => [...stack, nextCursor]);
							}
						}}
					/>
				</>
			) : (
				<EmptyPage
					title="No activity yet"
					description="Task changes made after this update will appear here."
				/>
			)}
		</section>
	);
}

function ActivityRow({ item }: { item: TaskActivity }) {
	const otherFields: string[] = [];
	for (const field of item.changed_fields) {
		if (field !== "status" && field !== "deleted_at") {
			otherFields.push(
				activityFieldLabels[field] ?? field.replaceAll("_", " "),
			);
		}
	}
	const statusChanged =
		item.status_before &&
		item.status_after &&
		item.status_before !== item.status_after;

	return (
		<article className="cm-activity-row">
			<span className={`cm-activity-marker cm-activity-${item.action}`}>
				{item.action === "created" || item.action === "restored" ? (
					<CheckCheck className="size-4" />
				) : item.action === "deleted" ? (
					<X className="size-4" />
				) : (
					<History className="size-4" />
				)}
			</span>
			<div className="min-w-0 flex-1">
				<p className="text-sm">
					<span className="font-medium">
						{activityActionLabels[item.action]}
					</span>{" "}
					<span className="text-[var(--text-secondary)]">
						{item.task_title}
					</span>
				</p>
				{statusChanged ? (
					<div className="mt-2 flex flex-wrap items-center gap-2">
						<StatusBadge status={item.status_before as TaskStatus} />
						<ArrowRight
							className="size-3.5 text-[var(--text-tertiary)]"
							aria-hidden="true"
						/>
						<StatusBadge status={item.status_after as TaskStatus} />
					</div>
				) : null}
				{otherFields.length ? (
					<p className="mt-1.5 text-xs text-muted-foreground">
						Changed {otherFields.join(", ")}
					</p>
				) : null}
			</div>
			<time
				dateTime={item.occurred_at}
				className="shrink-0 text-right text-xs text-muted-foreground tabular-nums"
			>
				{formatTimestamp(item.occurred_at)}
			</time>
		</article>
	);
}

function TaskListPage({
	title,
	description,
	contexts,
	fixedStatus,
	kind,
	defaultStatus = "",
}: {
	title: string;
	description: string;
	contexts: Context[];
	fixedStatus?: TaskStatus;
	kind?: Task["kind"];
	defaultStatus?: string;
}) {
	const [query, setQuery] = useState("");
	const [status, setStatus] = useState(defaultStatus);
	const [priority, setPriority] = useState("");
	const [contextId, setContextId] = useState<string>();
	const parameters = new URLSearchParams({ limit: "200" });
	if (query) parameters.set("q", query);
	if (fixedStatus) parameters.set("status", fixedStatus);
	else if (status) parameters.set("status", status);
	if (priority) parameters.set("priority", priority);
	if (contextId) parameters.set("context_id", contextId);
	if (kind) parameters.set("kind", kind);
	const tasks = useQuery({
		queryKey: [
			"tasks",
			kind ?? "all-kinds",
			fixedStatus ?? status,
			query,
			priority,
			contextId ?? "all-contexts",
		],
		queryFn: () => api.tasks(parameters),
	});
	return (
		<section>
			<div className="mb-8">
				<p className="mb-2 text-sm font-medium text-muted-foreground">
					{description}
				</p>
				<h1 className="font-display text-4xl tracking-tight">{title}</h1>
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
				<ContextFilter
					contexts={contexts}
					value={contextId}
					onChange={setContextId}
					label={`Filter ${title.toLowerCase()} by context`}
					className="h-9 rounded-lg border border-border bg-background px-3 text-sm"
				/>
				{fixedStatus ? null : (
					<select
						value={status}
						onChange={(event) => setStatus(event.target.value)}
						className="h-9 rounded-lg border border-border bg-background px-3 text-sm"
						aria-label={`Filter ${title.toLowerCase()} by status`}
					>
						<option value="">Any status</option>
						<option value="todo,in_progress">Open work</option>
						{taskListStatusFilters.map((status) => (
							<option key={status} value={status}>
								{taskStatusOptions[status].label}
							</option>
						))}
					</select>
				)}
				<select
					value={priority}
					onChange={(event) => setPriority(event.target.value)}
					className="h-9 rounded-lg border border-border bg-background px-3 text-sm"
					aria-label={`Filter ${title.toLowerCase()} by priority`}
				>
					<option value="">Any priority</option>
					{priorityOptions.map((option) => (
						<option key={option.value} value={option.value}>
							{option.label}
						</option>
					))}
				</select>
			</div>
			<p className="mb-3 text-xs text-muted-foreground">
				{tasks.data?.data.length ?? 0} tasks · priority first · newest within
				each priority
			</p>
			{tasks.isLoading ? (
				<LoadingPage />
			) : tasks.error ? (
				<TaskQueryError onRetry={() => tasks.refetch()} />
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

function WaitingPage({ contexts }: { contexts: Context[] }) {
	const [contextId, setContextId] = useState<string>();
	const date = todayString();
	const query = useQuery({
		queryKey: briefQueryKey(date, contextId),
		queryFn: () => api.brief(date, contextId),
	});
	const data = query.data;
	return (
		<section>
			<div className="mb-8 flex flex-wrap items-end justify-between gap-4">
				<div>
					<p className="mb-2 text-sm font-medium text-muted-foreground">
						People, not loose ends.
					</p>
					<h1 className="font-display text-4xl tracking-tight">Waiting on</h1>
				</div>
				<ContextFilter
					contexts={contexts}
					value={contextId}
					onChange={setContextId}
					label="Filter waiting on by context"
				/>
			</div>
			{query.error ? (
				<ErrorState onRetry={() => query.refetch()} />
			) : !data ? (
				<LoadingPage />
			) : data.waiting_on.length ? (
				<WaitingSection groups={data.waiting_on} contexts={contexts} />
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
	projects: Project[];
	pathname: string;
}) {
	const navigate = useNavigate();
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
			<div className="cm-context-card mb-9 rounded-3xl px-7 py-7">
				<div className="flex items-start justify-between gap-4">
					<div>
						<div className="cm-context-kicker flex items-center gap-3 text-sm">
							<span
								className="block size-3 rounded-full"
								style={{ backgroundColor: color }}
							/>
							<span>One life at a time</span>
						</div>
						<h1 className="mt-4 font-display text-4xl tracking-tight">
							{context.name}
						</h1>
					</div>
					<ContextActionsMenu
						context={context}
						onSaved={(saved) => {
							if (saved.slug !== context.slug) {
								navigate({
									to: "/c/$slug",
									params: { slug: saved.slug },
								});
							}
						}}
						onRemoved={() => navigate({ to: "/settings/contexts" })}
					/>
				</div>
				<div className="cm-context-stats mt-7 flex gap-7 pt-4 text-sm">
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
				<NewProjectButton
					contexts={contexts}
					defaultContextId={context.id}
					onCreated={(project) =>
						navigate({
							to: "/p/$projectId",
							params: { projectId: project.id },
						})
					}
				/>
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
			{tasks.isLoading ? (
				<LoadingPage />
			) : tasks.error ? (
				<TaskQueryError onRetry={() => tasks.refetch()} />
			) : tasks.data?.data.length ? (
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
	contexts,
	projects,
	pathname,
	onAddTask,
}: {
	contexts: Context[];
	projects: Project[];
	pathname: string;
	onAddTask: (project: Project) => void;
}) {
	const navigate = useNavigate();
	const id = pathname.split("/").pop();
	const project = projects.find((candidate) => candidate.id === id);
	const owningContext = contexts.find(
		(context) => context.id === project?.context_id,
	);
	const tasks = useQuery({
		queryKey: ["project-tasks", project?.id],
		queryFn: () =>
			api.tasks(
				new URLSearchParams({
					project_id: project?.id ?? "",
					top_level: "true",
					limit: "200",
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
				to={owningContext ? "/c/$slug" : "/settings/contexts"}
				params={owningContext ? { slug: owningContext.slug } : undefined}
				className="mb-5 inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
			>
				<ChevronLeft className="size-4" />
				{owningContext?.name ?? "Contexts & projects"}
			</Link>
			<div className="mb-9 flex items-start justify-between gap-4">
				<div>
					<p className="mb-2 text-sm text-muted-foreground">{project.status}</p>
					<h1 className="font-display text-4xl tracking-tight">
						{project.name}
					</h1>
					{project.description ? (
						<p className="mt-3 max-w-2xl text-muted-foreground">
							{project.description}
						</p>
					) : null}
				</div>
				<ProjectActionsMenu
					project={project}
					contexts={contexts}
					onRemoved={() =>
						owningContext
							? navigate({
									to: "/c/$slug",
									params: { slug: owningContext.slug },
								})
							: navigate({ to: "/settings/contexts" })
					}
				/>
			</div>
			<div className="mb-4 flex items-center justify-between">
				<h2 className="font-display text-2xl">Tasks</h2>
				<Button variant="outline" size="sm" onClick={() => onAddTask(project)}>
					<Plus className="mr-1 size-4" />
					Add task
				</Button>
			</div>
			{tasks.isLoading ? (
				<LoadingPage />
			) : tasks.error ? (
				<TaskQueryError onRetry={() => tasks.refetch()} />
			) : tasks.data?.data.length ? (
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

function TaskDetail({
	taskId,
	contexts,
	projects,
	people,
	onClose,
}: {
	taskId: string;
	contexts: Context[];
	projects: Project[];
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
			refreshTaskQueries(queryClient, task);
			setEditing(false);
		},
	});
	const task = taskQuery.data;
	const availableProjects = task?.context_id
		? projects
				.filter((project) => project.context_id === task.context_id)
				.sort((a, b) => a.name.localeCompare(b.name))
		: [];
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
						<Button variant="ghost" size="icon" aria-label="More task actions">
							<MoreHorizontal />
						</Button>
					</div>
				</div>
				{taskQuery.isLoading ? (
					<LoadingPage />
				) : task ? (
					<div className="p-6">
						<div className="flex gap-3">
							<TaskStatusMenu
								status={task.status}
								onStatusChange={(status) => update.mutate({ status })}
								disabled={update.isPending}
								className="mt-1"
								taskTitle={task.title}
								canDelegate={Boolean(task.delegated_to_id)}
							/>
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
										type="button"
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
								<TaskStatusMenu
									status={task.status}
									onStatusChange={(status) => update.mutate({ status })}
									disabled={update.isPending}
									canDelegate={Boolean(task.delegated_to_id)}
								/>
							</Field>
							<Field label="Priority">
								<select
									value={task.priority ?? ""}
									onChange={(event) =>
										update.mutate({
											priority: event.target.value || null,
										})
									}
									className="bg-transparent text-sm outline-none"
								>
									<option value="">No priority</option>
									{priorityOptions.map((option) => (
										<option key={option.value} value={option.value}>
											{option.label}
										</option>
									))}
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
							<Field label="Project">
								<select
									value={task.project_id ?? ""}
									onChange={(event) =>
										update.mutate({
											project_id: event.target.value || null,
										})
									}
									disabled={!task.context_id || update.isPending}
									className="max-w-52 bg-transparent text-right text-sm outline-none disabled:cursor-not-allowed disabled:opacity-60"
								>
									<option value="">
										{task.context_id ? "No project" : "Choose a context first"}
									</option>
									{availableProjects.map((project) => (
										<option key={project.id} value={project.id}>
											{project.name}
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
								onChange={(value) =>
									update.mutate({
										planned_on: value,
										...(value ? {} : { day_slot: null }),
									})
								}
							/>
							<Field label="Day slot">
								<select
									value={task.day_slot ?? ""}
									onChange={(event) =>
										update.mutate({
											day_slot: event.target.value || null,
											...(!task.planned_on && event.target.value
												? { planned_on: todayString() }
												: {}),
										})
									}
									className="bg-transparent text-sm outline-none"
								>
									<option value="">No slot</option>
									{Object.entries(daySlotLabels).map(([value, label]) => (
										<option key={value} value={value}>
											{label}
										</option>
									))}
								</select>
							</Field>
							<Field label="Estimate">
								{task.estimate_minutes ? (
									<button
										type="button"
										onClick={() => update.mutate({ estimate_minutes: null })}
										className="text-sm text-muted-foreground"
									>
										{formatMinutes(task.estimate_minutes)} ×
									</button>
								) : (
									<div className="flex gap-1">
										{[15, 30, 60].map((minutes) => (
											<button
												type="button"
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
						{update.error ? (
							<p
								role="alert"
								className="mt-3 rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive"
							>
								{update.error.message}
							</p>
						) : null}
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
						type="button"
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
function TaskQueryError({ onRetry }: { onRetry: () => void }) {
	return (
		<ErrorState
			onRetry={onRetry}
			title="Tasks are unavailable"
			description="Check your connection and try loading this task list again."
		/>
	);
}
function ErrorState({
	onRetry,
	title = "The brief is unavailable",
	description = "Your last synced data remains readable when it is available. Try again when the server is reachable.",
}: {
	onRetry: () => void;
	title?: string;
	description?: string;
}) {
	return (
		<div className="rounded-2xl border border-[var(--overdue)]/30 bg-[var(--overdue-bg)] p-6">
			<h2 className="font-display text-2xl">{title}</h2>
			<p className="mt-2 text-sm text-muted-foreground">{description}</p>
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
