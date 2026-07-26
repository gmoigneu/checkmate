import {
	ArrowRight,
	Check,
	ChevronDown,
	CircleDotDashed,
	Inbox,
	LoaderCircle,
	OctagonX,
	X,
} from "lucide-react";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { taskStatusLabels } from "@/lib/status";
import type { TaskStatus } from "@/lib/types";
import { cn } from "@/lib/utils";

const statusOptions: Array<{
	value: TaskStatus;
	icon: typeof Inbox;
}> = [
	{ value: "inbox", icon: Inbox },
	{ value: "todo", icon: CircleDotDashed },
	{ value: "in_progress", icon: LoaderCircle },
	{ value: "blocked", icon: OctagonX },
	{ value: "delegated", icon: ArrowRight },
	{ value: "done", icon: Check },
	{ value: "cancelled", icon: X },
];

function StatusBadge({
	status,
	menuTrigger,
}: {
	status: TaskStatus;
	menuTrigger?: boolean;
}) {
	const option =
		statusOptions.find((candidate) => candidate.value === status) ??
		statusOptions[1];
	const Icon = option.icon;

	return (
		<span className={cn("cm-status-badge", `cm-status-${status}`)}>
			<Icon className="size-3" />
			<span>{taskStatusLabels[status]}</span>
			{menuTrigger ? (
				<ChevronDown className="cm-status-chevron size-3" />
			) : null}
		</span>
	);
}

export function TaskStatusMenu({
	status,
	onStatusChange,
	disabled,
	className,
	taskTitle,
	canDelegate,
}: {
	status: TaskStatus;
	onStatusChange: (status: TaskStatus) => void;
	disabled?: boolean;
	className?: string;
	taskTitle?: string;
	canDelegate: boolean;
}) {
	const currentLabel = taskStatusLabels[status];

	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				<button
					type="button"
					className={cn("cm-status-trigger", className)}
					disabled={disabled}
					aria-label={`Change${taskTitle ? ` ${taskTitle}` : ""} status from ${currentLabel}`}
				>
					<StatusBadge status={status} menuTrigger />
				</button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="start" className="cm-status-menu w-48">
				<DropdownMenuRadioGroup
					value={status}
					onValueChange={(value) => {
						if (value !== status) onStatusChange(value as TaskStatus);
					}}
				>
					{statusOptions.map((option) => (
						<DropdownMenuRadioItem
							key={option.value}
							value={option.value}
							disabled={option.value === "delegated" && !canDelegate}
							title={
								option.value === "delegated" && !canDelegate
									? "Assign a delegate in the task details first"
									: undefined
							}
							className="py-2"
						>
							<StatusBadge status={option.value} />
						</DropdownMenuRadioItem>
					))}
				</DropdownMenuRadioGroup>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
