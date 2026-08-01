import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
	Check,
	Copy,
	Download,
	FileText,
	LoaderCircle,
	RefreshCw,
	Trash2,
} from "lucide-react";
import { type ReactNode, useEffect, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { ApiError, api } from "@/lib/api";
import { displayDate, formatTimestamp, todayString } from "@/lib/format";
import type {
	Context,
	Report,
	ReportMetrics,
	ReportRequest,
} from "@/lib/types";
import { cn } from "@/lib/utils";

function offsetDate(day: string, offset: number) {
	const date = new Date(`${day}T12:00:00Z`);
	date.setUTCDate(date.getUTCDate() + offset);
	return date.toISOString().slice(0, 10);
}

function startOfWeek(day: string, previous = false) {
	const date = new Date(`${day}T12:00:00Z`);
	const weekday = date.getUTCDay() || 7;
	date.setUTCDate(date.getUTCDate() - weekday + 1 - (previous ? 7 : 0));
	return date.toISOString().slice(0, 10);
}

function reportRequestKey(request: ReportRequest) {
	return [
		request.start_on,
		request.end_on,
		[...request.context_ids].sort().join(","),
		request.include_inbox ? "inbox" : "",
	];
}

export function ReportPage({ timezone }: { timezone: string }) {
	const queryClient = useQueryClient();
	const today = todayString(timezone);
	const [startOn, setStartOn] = useState(offsetDate(today, -6));
	const [endOn, setEndOn] = useState(today);
	const [selectedContexts, setSelectedContexts] = useState<string[]>([]);
	const [includeInbox, setIncludeInbox] = useState(false);
	const [focus, setFocus] = useState("");
	const [contextsReady, setContextsReady] = useState(false);
	const [selectedReportID, setSelectedReportID] = useState("");

	const contexts = useQuery({
		queryKey: ["report-contexts"],
		queryFn: () => api.contexts(true),
	});
	useEffect(() => {
		if (contextsReady || !contexts.data) return;
		setSelectedContexts(
			contexts.data.data
				.filter((context) => !context.archived_at)
				.map((context) => context.id),
		);
		setContextsReady(true);
	}, [contexts.data, contextsReady]);

	const request = useMemo<ReportRequest>(
		() => ({
			start_on: startOn,
			end_on: endOn,
			context_ids: selectedContexts,
			include_inbox: includeInbox,
			focus,
		}),
		[startOn, endOn, selectedContexts, includeInbox, focus],
	);
	const canPreview =
		Boolean(startOn && endOn) &&
		endOn >= startOn &&
		endOn <= today &&
		(selectedContexts.length > 0 || includeInbox);
	const preview = useQuery({
		queryKey: ["report-preview", ...reportRequestKey(request)],
		queryFn: () => api.previewReport(request),
		enabled: canPreview && contextsReady,
		retry: false,
	});
	const config = useQuery({
		queryKey: ["report-config"],
		queryFn: api.reportConfig,
		retry: false,
	});
	const reports = useQuery({ queryKey: ["reports"], queryFn: api.reports });
	useEffect(() => {
		if (!selectedReportID && reports.data?.data.length) {
			setSelectedReportID(reports.data.data[0].id);
		}
	}, [reports.data, selectedReportID]);

	const generate = useMutation({
		mutationFn: () => api.generateReport(request),
		onSuccess: (report) => {
			queryClient.setQueryData(["report", report.id], report);
			queryClient.invalidateQueries({ queryKey: ["reports"] });
			setSelectedReportID(report.id);
		},
	});

	const setPreset = (preset: "this" | "last" | "seven" | "thirty") => {
		switch (preset) {
			case "this":
				setStartOn(startOfWeek(today));
				setEndOn(today);
				break;
			case "last":
				setStartOn(startOfWeek(today, true));
				setEndOn(offsetDate(startOfWeek(today), -1));
				break;
			case "seven":
				setStartOn(offsetDate(today, -6));
				setEndOn(today);
				break;
			case "thirty":
				setStartOn(offsetDate(today, -29));
				setEndOn(today);
		}
	};

	const activeContexts =
		contexts.data?.data.filter((item) => !item.archived_at) ?? [];
	const archivedContexts =
		contexts.data?.data.filter((item) => item.archived_at) ?? [];
	const previewCount = preview.data?.tasks.length ?? 0;

	return (
		<section className="cm-report-page">
			<div className="mb-8">
				<p className="mb-2 text-sm font-medium text-muted-foreground">
					Turn your activity into a meeting-ready update.
				</p>
				<h1 className="font-display text-4xl tracking-tight">Report</h1>
			</div>

			<div className="cm-report-generator">
				<div className="cm-report-generator-row">
					<label htmlFor="report-start-on">
						<span>Start</span>
						<Input
							id="report-start-on"
							type="date"
							value={startOn}
							max={endOn || today}
							onChange={(event) => setStartOn(event.target.value)}
						/>
					</label>
					<label htmlFor="report-end-on">
						<span>End</span>
						<Input
							id="report-end-on"
							type="date"
							value={endOn}
							min={startOn}
							max={today}
							onChange={(event) => setEndOn(event.target.value)}
						/>
					</label>
					<fieldset className="cm-report-presets">
						<legend className="sr-only">Date range presets</legend>
						<button type="button" onClick={() => setPreset("this")}>
							This week
						</button>
						<button type="button" onClick={() => setPreset("last")}>
							Last week
						</button>
						<button type="button" onClick={() => setPreset("seven")}>
							Last 7 days
						</button>
						<button type="button" onClick={() => setPreset("thirty")}>
							Last 30 days
						</button>
					</fieldset>
				</div>

				<div className="cm-report-contexts">
					<span className="cm-report-field-label">Contexts</span>
					<div className="cm-report-context-options">
						{activeContexts.map((context) => (
							<ContextOption
								key={context.id}
								context={context}
								checked={selectedContexts.includes(context.id)}
								onChange={() =>
									setSelectedContexts((current) =>
										current.includes(context.id)
											? current.filter((id) => id !== context.id)
											: [...current, context.id],
									)
								}
							/>
						))}
						<label className="cm-report-context-option">
							<input
								type="checkbox"
								checked={includeInbox}
								onChange={(event) => setIncludeInbox(event.target.checked)}
							/>
							Inbox
						</label>
					</div>
					{archivedContexts.length ? (
						<details className="cm-report-archived">
							<summary>Archived contexts</summary>
							<div className="cm-report-context-options mt-2">
								{archivedContexts.map((context) => (
									<ContextOption
										key={context.id}
										context={context}
										checked={selectedContexts.includes(context.id)}
										onChange={() =>
											setSelectedContexts((current) =>
												current.includes(context.id)
													? current.filter((id) => id !== context.id)
													: [...current, context.id],
											)
										}
									/>
								))}
							</div>
						</details>
					) : null}
				</div>

				<label className="cm-report-focus" htmlFor="report-focus">
					<span>
						Focus <small>Optional</small>
					</span>
					<Textarea
						id="report-focus"
						value={focus}
						maxLength={500}
						rows={2}
						placeholder="Emphasize launch risks and decisions needed."
						onChange={(event) => setFocus(event.target.value)}
					/>
				</label>

				<div className="cm-report-preview-strip">
					{preview.isLoading ? (
						<span>
							<LoaderCircle className="size-4 animate-spin" /> Checking
							activity…
						</span>
					) : preview.error ? (
						<span className="text-destructive">
							{errorMessage(preview.error)}
						</span>
					) : preview.data ? (
						<>
							<MetricSummary metrics={preview.data.metrics} />
							<details>
								<summary>
									{previewCount} source {previewCount === 1 ? "task" : "tasks"}
								</summary>
								<div className="cm-report-source-list">
									{preview.data.tasks.map((task) => (
										<a key={task.task_id} href={`/t/${task.task_id}`}>
											<span>{task.title}</span>
											<small>
												{task.category} · {task.context_name}
											</small>
										</a>
									))}
								</div>
							</details>
							{preview.data.legacy_history ? (
								<small>Some activity predates detailed history.</small>
							) : null}
						</>
					) : (
						<span>Select a valid date range and at least one context.</span>
					)}
					<Button
						disabled={
							!config.data?.configured ||
							!previewCount ||
							generate.isPending ||
							!canPreview
						}
						onClick={() => generate.mutate()}
					>
						{generate.isPending ? (
							<LoaderCircle className="animate-spin" />
						) : (
							<FileText />
						)}
						{generate.isPending ? "Generating…" : "Generate report"}
					</Button>
				</div>
				{config.data && !config.data.configured ? (
					<p className="cm-report-config-note">
						Report generation is not configured on this server. Set the
						OpenRouter API key to enable it.
					</p>
				) : null}
				{generate.error ? (
					<p className="cm-report-error">{errorMessage(generate.error)}</p>
				) : null}
			</div>

			<div className="cm-report-workspace">
				<SavedReportList
					reports={reports.data?.data ?? []}
					selectedID={selectedReportID}
					onSelect={setSelectedReportID}
				/>
				<div className="cm-report-editor-shell">
					{selectedReportID ? (
						<ReportEditor
							reportID={selectedReportID}
							onDeleted={() => setSelectedReportID("")}
						/>
					) : (
						<div className="cm-report-empty">
							<FileText className="size-8" />
							<h2>No saved report selected</h2>
							<p>
								Generate a report above or choose one from your saved reports.
							</p>
						</div>
					)}
				</div>
			</div>
		</section>
	);
}

function ContextOption({
	context,
	checked,
	onChange,
}: {
	context: Context;
	checked: boolean;
	onChange: () => void;
}) {
	return (
		<label className="cm-report-context-option">
			<input type="checkbox" checked={checked} onChange={onChange} />
			<span
				className="cm-context-dot"
				style={{ backgroundColor: context.color ?? "#8c8c8c" }}
			/>
			{context.name}
		</label>
	);
}

function MetricSummary({ metrics }: { metrics: ReportMetrics }) {
	return (
		<div className="cm-report-metrics">
			<span>
				<strong>{metrics.completed}</strong> completed
			</span>
			<span>
				<strong>{metrics.open}</strong> open
			</span>
			<span>
				<strong>{metrics.blocked}</strong> blocked
			</span>
			<span>
				<strong>{metrics.delegated}</strong> delegated
			</span>
			<span>
				<strong>{metrics.dropped}</strong> dropped
			</span>
		</div>
	);
}

function SavedReportList({
	reports,
	selectedID,
	onSelect,
}: {
	reports: Report[];
	selectedID: string;
	onSelect: (id: string) => void;
}) {
	return (
		<aside className="cm-saved-reports">
			<div className="cm-saved-reports-heading">
				<span>Saved reports</span>
				<small>{reports.length}</small>
			</div>
			{reports.length ? (
				reports.map((report) => (
					<button
						type="button"
						key={report.id}
						className={cn(
							"cm-saved-report",
							selectedID === report.id && "cm-saved-report-selected",
						)}
						onClick={() => onSelect(report.id)}
					>
						<strong>{report.title}</strong>
						<span>
							{displayDate(report.start_on)} – {displayDate(report.end_on)}
						</span>
						<small>Updated {formatTimestamp(report.updated_at)}</small>
					</button>
				))
			) : (
				<p className="cm-saved-reports-empty">
					Your generated reports will appear here.
				</p>
			)}
		</aside>
	);
}

function ReportEditor({
	reportID,
	onDeleted,
}: {
	reportID: string;
	onDeleted: () => void;
}) {
	const queryClient = useQueryClient();
	const detail = useQuery({
		queryKey: ["report", reportID],
		queryFn: () => api.report(reportID),
	});
	const [selectedVersion, setSelectedVersion] = useState(0);
	const [mode, setMode] = useState<"edit" | "preview">("preview");
	const [title, setTitle] = useState("");
	const [content, setContent] = useState("");
	const [savedTitle, setSavedTitle] = useState("");
	const [savedContent, setSavedContent] = useState("");
	const [copied, setCopied] = useState("");

	const versions = detail.data?.versions ?? [];
	const latest = versions.find(
		(version) => version.version_number === detail.data?.latest_version,
	);
	const loadedReportID = detail.data?.id ?? "";
	const loadedTitle = detail.data?.title ?? "";
	const latestID = latest?.id ?? "";
	const latestContent = latest?.content_markdown ?? "";
	const latestNumber = latest?.version_number ?? 0;
	// Autosave responses update cached content. Resetting on those updates would
	// overwrite newer local typing, so report/version identity is the deliberate
	// boundary for loading a draft into the editor.
	// biome-ignore lint/correctness/useExhaustiveDependencies: identity-only reset
	useEffect(() => {
		if (!loadedReportID || !latestID) return;
		setTitle(loadedTitle);
		setContent(latestContent);
		setSavedTitle(loadedTitle);
		setSavedContent(latestContent);
		setSelectedVersion(latestNumber);
	}, [loadedReportID, latestID]);

	const save = useMutation({
		mutationFn: (
			body: Partial<{
				title: string;
				content_markdown: string;
				version_number: number;
			}>,
		) => api.updateReport(reportID, body),
		onSuccess: (report, variables) => {
			queryClient.setQueryData(["report", reportID], report);
			queryClient.invalidateQueries({ queryKey: ["reports"] });
			if (variables.title !== undefined) setSavedTitle(variables.title);
			if (variables.content_markdown !== undefined) {
				setSavedContent(variables.content_markdown);
			}
		},
	});
	const saveMutate = save.mutate;
	useEffect(() => {
		if (
			!latestID ||
			save.isPending ||
			!title.trim() ||
			(title === savedTitle && content === savedContent)
		)
			return;
		const timeout = window.setTimeout(() => {
			const body: Partial<{
				title: string;
				content_markdown: string;
				version_number: number;
			}> = { version_number: latestNumber };
			if (title !== savedTitle) body.title = title;
			if (content !== savedContent) body.content_markdown = content;
			saveMutate(body);
		}, 700);
		return () => window.clearTimeout(timeout);
	}, [
		title,
		content,
		savedTitle,
		savedContent,
		latestID,
		latestNumber,
		save.isPending,
		saveMutate,
	]);

	const regenerate = useMutation({
		mutationFn: () => api.regenerateReport(reportID),
		onSuccess: (report) => {
			queryClient.setQueryData(["report", reportID], report);
			queryClient.invalidateQueries({ queryKey: ["reports"] });
			const next = report.versions?.find(
				(version) => version.version_number === report.latest_version,
			);
			if (next) {
				setSelectedVersion(next.version_number);
				setContent(next.content_markdown);
				setSavedContent(next.content_markdown);
			}
		},
	});
	const remove = useMutation({
		mutationFn: () => api.deleteReport(reportID),
		onSuccess: () => {
			queryClient.removeQueries({ queryKey: ["report", reportID] });
			queryClient.setQueryData<{
				data: Report[];
				next_cursor: string | null;
			}>(["reports"], (current) =>
				current
					? {
							...current,
							data: current.data.filter((report) => report.id !== reportID),
						}
					: current,
			);
			queryClient.invalidateQueries({ queryKey: ["reports"] });
			onDeleted();
		},
	});

	if (detail.isLoading)
		return (
			<div className="cm-report-empty">
				<LoaderCircle className="animate-spin" />
			</div>
		);
	if (detail.error || !detail.data || !latest) {
		return (
			<div className="cm-report-empty">
				<p>{errorMessage(detail.error)}</p>
			</div>
		);
	}
	const shownVersion =
		versions.find((version) => version.version_number === selectedVersion) ??
		latest;
	const isLatest = shownVersion.version_number === latest.version_number;
	const shownContent = isLatest ? content : shownVersion.content_markdown;
	const dirty = title !== savedTitle || content !== savedContent;
	const savingLabel = save.isPending ? "Saving…" : dirty ? "Unsaved" : "Saved";

	const markCopied = (value: string) => {
		setCopied(value);
		window.setTimeout(() => setCopied(""), 1600);
	};
	return (
		<div className="cm-report-editor">
			<div className="cm-report-editor-header">
				<Input
					className="cm-report-title-input"
					value={title}
					disabled={!isLatest}
					onChange={(event) => setTitle(event.target.value)}
				/>
				<div className="cm-report-editor-meta">
					<span className="cm-report-saved-state">
						<Check className="size-3" /> {savingLabel}
					</span>
					<select
						value={shownVersion.version_number}
						onChange={(event) => setSelectedVersion(Number(event.target.value))}
					>
						{versions.map((version) => (
							<option key={version.id} value={version.version_number}>
								Version {version.version_number} ·{" "}
								{formatTimestamp(version.created_at)}
							</option>
						))}
					</select>
					<span>{shownVersion.model}</span>
				</div>
			</div>
			<div className="cm-report-toolbar">
				<div className="cm-report-mode-switch">
					<button
						type="button"
						className={mode === "edit" ? "active" : ""}
						onClick={() => setMode("edit")}
						disabled={!isLatest}
					>
						Edit
					</button>
					<button
						type="button"
						className={mode === "preview" ? "active" : ""}
						onClick={() => setMode("preview")}
					>
						Preview
					</button>
				</div>
				<div className="cm-report-actions">
					<Button
						variant="ghost"
						size="sm"
						onClick={async () => {
							await navigator.clipboard.writeText(shownContent);
							markCopied("markdown");
						}}
					>
						{copied === "markdown" ? <Check /> : <Copy />} Copy Markdown
					</Button>
					<Button
						variant="ghost"
						size="sm"
						onClick={async () => {
							await copyFormatted(shownContent);
							markCopied("formatted");
						}}
					>
						{copied === "formatted" ? <Check /> : <Copy />} Copy formatted
					</Button>
					<Button
						variant="ghost"
						size="sm"
						onClick={() => downloadMarkdown(title, shownContent)}
					>
						<Download /> Download .md
					</Button>
					<Button
						variant="ghost"
						size="sm"
						disabled={regenerate.isPending || save.isPending || dirty}
						onClick={() => regenerate.mutate()}
					>
						<RefreshCw className={regenerate.isPending ? "animate-spin" : ""} />{" "}
						Regenerate
					</Button>
					<Button
						variant="ghost"
						size="icon-sm"
						aria-label="Delete report"
						disabled={remove.isPending}
						onClick={() => {
							if (window.confirm("Delete this report and all of its versions?"))
								remove.mutate();
						}}
					>
						<Trash2 />
					</Button>
				</div>
			</div>
			{regenerate.error ? (
				<p className="cm-report-error">{errorMessage(regenerate.error)}</p>
			) : null}
			{mode === "edit" && isLatest ? (
				<Textarea
					className="cm-report-markdown-editor"
					value={content}
					onChange={(event) => setContent(event.target.value)}
				/>
			) : (
				<MarkdownPreview markdown={shownContent} />
			)}
		</div>
	);
}

function inlineMarkdown(text: string): ReactNode[] {
	const nodes: ReactNode[] = [];
	const pattern = /\[([^\]]+)\]\((\/t\/[^)]+)\)/g;
	let last = 0;
	for (const match of text.matchAll(pattern)) {
		const index = match.index ?? 0;
		if (index > last) nodes.push(text.slice(last, index));
		nodes.push(
			<a key={`${index}-${match[2]}`} href={match[2]}>
				{match[1]}
			</a>,
		);
		last = index + match[0].length;
	}
	if (last < text.length) nodes.push(text.slice(last));
	return nodes;
}

function MarkdownPreview({ markdown }: { markdown: string }) {
	const lines = markdown.split("\n");
	const nodes: ReactNode[] = [];
	let bullets: string[] = [];
	let nodeID = 0;
	const nextKey = (kind: string) => `${kind}-${nodeID++}`;
	const flushBullets = () => {
		if (!bullets.length) return;
		nodes.push(
			<ul key={nextKey("list")}>
				{bullets.map((line) => (
					<li key={nextKey(`item-${line}`)}>{inlineMarkdown(line)}</li>
				))}
			</ul>,
		);
		bullets = [];
	};
	lines.forEach((line) => {
		if (line.startsWith("- ")) {
			bullets.push(line.slice(2));
			return;
		}
		flushBullets();
		if (line.startsWith("## "))
			nodes.push(<h2 key={nextKey("heading")}>{line.slice(3)}</h2>);
		else if (line === "---") nodes.push(<hr key={nextKey("rule")} />);
		else if (line.trim())
			nodes.push(
				<p key={nextKey("paragraph")}>
					{inlineMarkdown(line.replace(/^_(.*)_$/, "$1"))}
				</p>,
			);
	});
	flushBullets();
	return <article className="cm-report-preview">{nodes}</article>;
}

function escapedHTML(value: string) {
	return value
		.replaceAll("&", "&amp;")
		.replaceAll("<", "&lt;")
		.replaceAll(">", "&gt;")
		.replaceAll('"', "&quot;");
}

function markdownHTML(markdown: string) {
	let inList = false;
	let html = "";
	for (const rawLine of markdown.split("\n")) {
		const line = escapedHTML(rawLine).replace(
			/\[([^\]]+)\]\((\/t\/[^)]+)\)/g,
			'<a href="$2">$1</a>',
		);
		if (line.startsWith("- ")) {
			if (!inList) {
				html += "<ul>";
				inList = true;
			}
			html += `<li>${line.slice(2)}</li>`;
			continue;
		}
		if (inList) {
			html += "</ul>";
			inList = false;
		}
		if (line.startsWith("## ")) html += `<h2>${line.slice(3)}</h2>`;
		else if (line === "---") html += "<hr>";
		else if (line.trim()) html += `<p>${line.replace(/^_(.*)_$/, "$1")}</p>`;
	}
	if (inList) html += "</ul>";
	return html;
}

async function copyFormatted(markdown: string) {
	if (typeof ClipboardItem !== "undefined" && navigator.clipboard.write) {
		await navigator.clipboard.write([
			new ClipboardItem({
				"text/html": new Blob([markdownHTML(markdown)], { type: "text/html" }),
				"text/plain": new Blob([markdown], { type: "text/plain" }),
			}),
		]);
		return;
	}
	await navigator.clipboard.writeText(markdown);
}

function downloadMarkdown(title: string, markdown: string) {
	const blob = new Blob([markdown], { type: "text/markdown;charset=utf-8" });
	const url = URL.createObjectURL(blob);
	const anchor = document.createElement("a");
	anchor.href = url;
	anchor.download = `${
		title
			.toLowerCase()
			.replace(/[^a-z0-9]+/g, "-")
			.replace(/^-|-$/g, "") || "report"
	}.md`;
	anchor.click();
	URL.revokeObjectURL(url);
}

function errorMessage(error: unknown) {
	if (error instanceof ApiError) {
		const detail = Object.values(error.fields)[0];
		return detail || error.message;
	}
	if (error instanceof Error) return error.message;
	return "Something went wrong.";
}
