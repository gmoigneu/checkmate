import { ChevronDown } from "lucide-react";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
	taskStatusEntries,
	taskStatusOption,
	taskStatusValue,
} from "@/lib/status";
import type { TaskStatus } from "@/lib/types";
import { cn } from "@/lib/utils";

function StatusBadge({
	status,
	menuTrigger,
}: {
	status: TaskStatus;
	menuTrigger?: boolean;
}) {
	const normalizedStatus = taskStatusValue(status);
	const option = taskStatusOption(normalizedStatus);
	const Icon = option.icon;

	return (
		<span className={cn("cm-status-badge", `cm-status-${normalizedStatus}`)}>
			<Icon className="size-3" />
			<span>{option.label}</span>
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
	const normalizedStatus = taskStatusValue(status);
	const currentLabel = taskStatusOption(normalizedStatus).label;

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
					value={normalizedStatus}
					onValueChange={(value) => {
						if (value !== normalizedStatus) onStatusChange(value as TaskStatus);
					}}
				>
					{taskStatusEntries.map(([value]) => (
						<DropdownMenuRadioItem
							key={value}
							value={value}
							disabled={value === "delegated" && !canDelegate}
							title={
								value === "delegated" && !canDelegate
									? "Assign a delegate in the task details first"
									: undefined
							}
							className="py-2"
						>
							<StatusBadge status={value} />
						</DropdownMenuRadioItem>
					))}
				</DropdownMenuRadioGroup>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
