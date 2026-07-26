import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
	GripVertical,
	MoreHorizontal,
	Pencil,
	Plus,
	RefreshCw,
	Trash2,
} from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import { exampleRoutineSeed } from "@/lib/routine-seed";
import type { Context, DaySlot, Project, Recurrence } from "@/lib/types";
import { cn } from "@/lib/utils";

const slots: Array<{ value: DaySlot; label: string; description: string }> = [
	{ value: "morning", label: "Morning", description: "Start with intention" },
	{ value: "midday", label: "Midday", description: "Reset and refocus" },
	{ value: "afternoon", label: "Afternoon", description: "Keep momentum" },
	{ value: "evening", label: "Evening", description: "Close the loop" },
	{ value: "night", label: "Night", description: "Wind down" },
];

const weekdays = [
	{ value: "MO", label: "M" },
	{ value: "TU", label: "T" },
	{ value: "WE", label: "W" },
	{ value: "TH", label: "T" },
	{ value: "FR", label: "F" },
	{ value: "SA", label: "S" },
	{ value: "SU", label: "S" },
];

const allDays = weekdays.map((day) => day.value);

function rruleForDays(days: string[]) {
	return days.length === allDays.length
		? "FREQ=DAILY"
		: `FREQ=WEEKLY;BYDAY=${weekdays
				.filter((day) => days.includes(day.value))
				.map((day) => day.value)
				.join(",")}`;
}

function daysForRRule(rrule: string) {
	if (/(^|;)FREQ=DAILY(;|$)/i.test(rrule)) return allDays;
	const match = rrule.match(/(?:^|;)BYDAY=([^;]+)/i);
	return (
		match?.[1]?.split(",").filter((day) => allDays.includes(day)) ?? allDays
	);
}

function daySummary(rrule: string) {
	const days = daysForRRule(rrule);
	if (days.length === 7) return "Every day";
	if (days.join(",") === "MO,TU,WE,TH,FR") return "Weekdays";

	return weekdays
		.filter((day) => days.includes(day.value))
		.map((day) => day.value.slice(0, 2))
		.join(" · ");
}

function nextOrder(items: Recurrence[], slot: DaySlot) {
	const current = items
		.filter((item) => item.day_slot === slot)
		.reduce((highest, item) => Math.max(highest, item.slot_order), 0);

	return current + 10;
}

export function RoutinePage({
	contexts,
	projects,
	timezone,
	today,
}: {
	contexts: Context[];
	projects: Project[];
	timezone: string;
	today: string;
}) {
	const queryClient = useQueryClient();
	const routines = useQuery({ queryKey: ["routines"], queryFn: api.routines });
	const items = routines.data?.data ?? [];
	const [editing, setEditing] = useState<Recurrence | null>();
	const [newSlot, setNewSlot] = useState<DaySlot>("morning");
	const [dragging, setDragging] = useState<string>();

	const reorder = useMutation({
		mutationFn: async ({
			draggedID,
			targetID,
			slot,
		}: {
			draggedID: string;
			targetID?: string;
			slot: DaySlot;
		}) => {
			const dragged = items.find((item) => item.id === draggedID);
			if (!dragged) return;

			const reordered = items
				.filter((item) => item.id !== draggedID && item.day_slot === slot)
				.sort(
					(a, b) =>
						a.slot_order - b.slot_order || a.title.localeCompare(b.title),
				);
			const targetIndex = targetID
				? reordered.findIndex((item) => item.id === targetID)
				: reordered.length;
			reordered.splice(targetIndex < 0 ? reordered.length : targetIndex, 0, {
				...dragged,
				day_slot: slot,
			});

			const changed = reordered
				.map((item, index) => ({ item, order: (index + 1) * 10 }))
				.filter(
					({ item, order }) =>
						item.slot_order !== order || item.id === draggedID,
				);

			await Promise.all(
				changed.map(({ item, order }) =>
					api.updateRoutine(item.id, {
						day_slot: item.day_slot,
						slot_order: order,
					}),
				),
			);
		},
		onSuccess: () => {
			setDragging(undefined);
			queryClient.invalidateQueries({ queryKey: ["routines"] });
			queryClient.invalidateQueries({ queryKey: ["brief"] });
		},
	});

	const remove = useMutation({
		mutationFn: api.deleteRoutine,
		onSuccess: () => {
			queryClient.invalidateQueries({ queryKey: ["routines"] });
			queryClient.invalidateQueries({ queryKey: ["brief"] });
		},
	});

	const seed = useMutation({
		mutationFn: async () => {
			if (!contexts.length)
				throw new Error("Create a context before loading the example.");

			const contextFor = (name: string) =>
				contexts.find(
					(context) => context.name.toLowerCase() === name.toLowerCase(),
				)?.id ??
				(name === "Work"
					? contexts.find((context) => context.name === "Upsun")?.id
					: contexts.find((context) => context.name === "Personal")?.id) ??
				contexts[0].id;
			const orders = new Map<DaySlot, number>();

			for (const item of exampleRoutineSeed) {
				const order = (orders.get(item.slot) ?? 0) + 10;
				orders.set(item.slot, order);
				await api.createRoutine({
					title: item.title,
					context_id: contextFor(item.context),
					day_slot: item.slot,
					slot_order: order,
					rrule: rruleForDays(item.days),
					timezone,
					starts_on: today,
					lead_days: 0,
				});
			}
		},
		onSuccess: () => {
			queryClient.invalidateQueries({ queryKey: ["routines"] });
			queryClient.invalidateQueries({ queryKey: ["brief"] });
		},
	});

	return (
		<section>
			<div className="mb-8 flex flex-wrap items-end justify-between gap-4">
				<div>
					<p className="mb-2 text-sm font-medium text-muted-foreground">
						Small actions, in the order your day needs them.
					</p>
					<h1 className="font-display text-4xl tracking-tight">
						Daily Routine
					</h1>
				</div>
				{items.length ? (
					<p className="text-sm text-muted-foreground">
						{items.filter((item) => item.active).length} active items
					</p>
				) : null}
			</div>

			{routines.isLoading ? (
				<div className="rounded-2xl border border-border bg-card p-8 text-sm text-muted-foreground">
					Loading your routine…
				</div>
			) : (
				<div className="grid gap-4 xl:grid-cols-2">
					{slots.map((slot) => {
						const slotItems = items
							.filter((item) => item.day_slot === slot.value)
							.sort(
								(a, b) =>
									a.slot_order - b.slot_order || a.title.localeCompare(b.title),
							);
						return (
							<section key={slot.value} className="cm-routine-slot">
								<header className="cm-routine-slot-header">
									<div>
										<h2>{slot.label}</h2>
										<p>{slot.description}</p>
									</div>
									<Button
										variant="ghost"
										size="icon-sm"
										onClick={() => {
											setNewSlot(slot.value);
											setEditing(null);
										}}
										aria-label={`Add a ${slot.label.toLowerCase()} routine item`}
									>
										<Plus />
									</Button>
								</header>
								<ul
									className="cm-routine-items"
									onDragOver={(event) => event.preventDefault()}
									onDrop={(event) => {
										event.preventDefault();
										if (dragging)
											reorder.mutate({
												draggedID: dragging,
												slot: slot.value,
											});
									}}
								>
									{slotItems.length ? (
										slotItems.map((item) => (
											<li
												key={item.id}
												className={cn(
													"cm-routine-item",
													!item.active && "opacity-55",
													dragging === item.id && "opacity-40",
												)}
												draggable
												onDragStart={() => setDragging(item.id)}
												onDragEnd={() => setDragging(undefined)}
												onDragOver={(event) => event.preventDefault()}
												onDrop={(event) => {
													event.preventDefault();
													event.stopPropagation();
													if (dragging && dragging !== item.id)
														reorder.mutate({
															draggedID: dragging,
															targetID: item.id,
															slot: slot.value,
														});
												}}
											>
												<GripVertical className="size-4 shrink-0 text-muted-foreground" />
												<div className="min-w-0 flex-1">
													<p className="truncate font-medium">{item.title}</p>
													<p className="mt-0.5 text-xs text-muted-foreground">
														{daySummary(item.rrule)}
														{item.estimate_minutes
															? ` · ${item.estimate_minutes}m`
															: ""}
														{!item.active ? " · Paused" : ""}
													</p>
												</div>
												<Button
													variant="ghost"
													size="icon-xs"
													onClick={() => setEditing(item)}
													aria-label={`Edit ${item.title}`}
												>
													<Pencil />
												</Button>
												<Button
													variant="ghost"
													size="icon-xs"
													className="text-muted-foreground hover:text-destructive"
													onClick={() => {
														if (
															window.confirm(
																`Remove “${item.title}” from your routine?`,
															)
														)
															remove.mutate(item.id);
													}}
													aria-label={`Remove ${item.title}`}
												>
													<Trash2 />
												</Button>
											</li>
										))
									) : (
										<li>
											<button
												type="button"
												className="cm-routine-empty w-full"
												onClick={() => {
													setNewSlot(slot.value);
													setEditing(null);
												}}
											>
												<Plus className="size-4" />
												Add the first item
											</button>
										</li>
									)}
								</ul>
							</section>
						);
					})}
				</div>
			)}

			{!routines.isLoading && !items.length ? (
				<div className="mt-6 rounded-2xl border border-dashed border-border bg-card p-6 text-center">
					<MoreHorizontal className="mx-auto mb-2 size-5 text-muted-foreground" />
					<p className="font-medium">
						Start from a blank day or use the example
					</p>
					<p className="mx-auto mt-1 max-w-lg text-sm text-muted-foreground">
						The example adds the handwritten morning, midday, and evening
						routine. You can edit every item afterwards.
					</p>
					<Button
						variant="outline"
						className="mt-4"
						onClick={() => seed.mutate()}
						disabled={seed.isPending || !contexts.length}
					>
						<RefreshCw className={cn(seed.isPending && "animate-spin")} />
						Load example routine
					</Button>
					{seed.error ? (
						<p className="mt-3 text-sm text-destructive">
							{seed.error.message}
						</p>
					) : null}
				</div>
			) : null}

			<RoutineEditor
				key={editing?.id ?? `new-${newSlot}`}
				open={editing !== undefined}
				item={editing ?? undefined}
				initialSlot={newSlot}
				contexts={contexts}
				projects={projects}
				timezone={timezone}
				today={today}
				nextOrderBySlot={
					Object.fromEntries(
						slots.map((slot) => [slot.value, nextOrder(items, slot.value)]),
					) as Record<DaySlot, number>
				}
				onClose={() => setEditing(undefined)}
			/>
		</section>
	);
}

function RoutineEditor({
	open,
	item,
	initialSlot,
	contexts,
	projects,
	timezone,
	today,
	nextOrderBySlot,
	onClose,
}: {
	open: boolean;
	item?: Recurrence;
	initialSlot: DaySlot;
	contexts: Context[];
	projects: Project[];
	timezone: string;
	today: string;
	nextOrderBySlot: Record<DaySlot, number>;
	onClose: () => void;
}) {
	const queryClient = useQueryClient();
	const [title, setTitle] = useState(item?.title ?? "");
	const [details, setDetails] = useState(item?.details ?? "");
	const [slot, setSlot] = useState<DaySlot>(item?.day_slot ?? initialSlot);
	const [days, setDays] = useState(daysForRRule(item?.rrule ?? "FREQ=DAILY"));
	const [contextID, setContextID] = useState(
		item?.context_id ?? contexts[0]?.id ?? "",
	);
	const [projectID, setProjectID] = useState(item?.project_id ?? "");
	const [estimate, setEstimate] = useState(
		item?.estimate_minutes?.toString() ?? "",
	);
	const [active, setActive] = useState(item?.active ?? true);

	const save = useMutation({
		mutationFn: () => {
			const body = {
				title: title.trim(),
				details: details.trim() || null,
				context_id: contextID,
				project_id: projectID || null,
				day_slot: slot,
				slot_order:
					item?.day_slot === slot ? item.slot_order : nextOrderBySlot[slot],
				rrule: rruleForDays(days),
				estimate_minutes: estimate ? Number(estimate) : null,
				active,
			};
			return item
				? api.updateRoutine(item.id, body)
				: api.createRoutine({
						...body,
						timezone,
						starts_on: today,
						lead_days: 0,
					});
		},
		onSuccess: () => {
			queryClient.invalidateQueries({ queryKey: ["routines"] });
			queryClient.invalidateQueries({ queryKey: ["brief"] });
			onClose();
		},
	});

	const availableProjects = projects.filter(
		(project) => project.context_id === contextID,
	);

	return (
		<Dialog open={open} onOpenChange={(next) => !next && onClose()}>
			<DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-xl">
				<DialogHeader>
					<DialogTitle>
						{item ? "Edit routine item" : "Add routine item"}
					</DialogTitle>
					<DialogDescription>
						Choose where it belongs in the day and which weekdays it appears.
					</DialogDescription>
				</DialogHeader>
				<div className="grid gap-5 py-2">
					<label
						htmlFor="routine-title"
						className="grid gap-1.5 text-sm font-medium"
					>
						Task
						<Input
							id="routine-title"
							autoFocus
							value={title}
							onChange={(event) => setTitle(event.target.value)}
							placeholder="What do you want to repeat?"
						/>
					</label>
					<label
						htmlFor="routine-details"
						className="grid gap-1.5 text-sm font-medium"
					>
						Details
						<Textarea
							id="routine-details"
							value={details}
							onChange={(event) => setDetails(event.target.value)}
							placeholder="Optional notes"
						/>
					</label>
					<div className="grid gap-3 sm:grid-cols-2">
						<label className="grid gap-1.5 text-sm font-medium">
							Slot
							<select
								value={slot}
								onChange={(event) => setSlot(event.target.value as DaySlot)}
								className="h-9 rounded-md border border-input bg-transparent px-3 text-sm"
							>
								{slots.map((option) => (
									<option key={option.value} value={option.value}>
										{option.label}
									</option>
								))}
							</select>
						</label>
						<label
							htmlFor="routine-estimate"
							className="grid gap-1.5 text-sm font-medium"
						>
							Estimate
							<Input
								id="routine-estimate"
								type="number"
								min="1"
								value={estimate}
								onChange={(event) => setEstimate(event.target.value)}
								placeholder="Minutes"
							/>
						</label>
					</div>
					<div>
						<p className="mb-2 text-sm font-medium">Days</p>
						<div className="grid grid-cols-7 gap-1.5">
							{weekdays.map((day, index) => {
								const selected = days.includes(day.value);
								return (
									<button
										type="button"
										key={day.value}
										onClick={() =>
											setDays((current) =>
												selected
													? current.filter((value) => value !== day.value)
													: allDays.filter(
															(value) =>
																current.includes(value) || value === day.value,
														),
											)
										}
										className={cn(
											"rounded-lg border py-2 text-xs font-medium",
											selected
												? "border-[var(--accent)] bg-[var(--accent-soft)] text-[var(--text-accent)]"
												: "border-border text-muted-foreground",
										)}
										aria-pressed={selected}
										aria-label={`${selected ? "Remove" : "Add"} ${["Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"][index]}`}
									>
										{day.label}
									</button>
								);
							})}
						</div>
					</div>
					<div className="grid gap-3 sm:grid-cols-2">
						<label className="grid gap-1.5 text-sm font-medium">
							Context
							<select
								value={contextID}
								onChange={(event) => {
									setContextID(event.target.value);
									setProjectID("");
								}}
								className="h-9 rounded-md border border-input bg-transparent px-3 text-sm"
							>
								{contexts.map((context) => (
									<option key={context.id} value={context.id}>
										{context.name}
									</option>
								))}
							</select>
						</label>
						<label className="grid gap-1.5 text-sm font-medium">
							Project
							<select
								value={projectID}
								onChange={(event) => setProjectID(event.target.value)}
								className="h-9 rounded-md border border-input bg-transparent px-3 text-sm"
							>
								<option value="">No project</option>
								{availableProjects.map((project) => (
									<option key={project.id} value={project.id}>
										{project.name}
									</option>
								))}
							</select>
						</label>
					</div>
					<label className="flex items-center gap-2 text-sm">
						<input
							type="checkbox"
							checked={active}
							onChange={(event) => setActive(event.target.checked)}
						/>
						Active
					</label>
				</div>
				{save.error ? (
					<p className="text-sm text-destructive">{save.error.message}</p>
				) : null}
				<DialogFooter>
					<Button variant="ghost" onClick={onClose}>
						Cancel
					</Button>
					<Button
						onClick={() => save.mutate()}
						disabled={
							save.isPending || !title.trim() || !contextID || days.length === 0
						}
					>
						{save.isPending ? "Saving…" : item ? "Save changes" : "Add item"}
					</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}
