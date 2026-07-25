import { createFileRoute } from "@tanstack/react-router";
import { CheckmateApp } from "@/components/checkmate-app";
export const Route = createFileRoute("/t/$taskId")({ component: TaskPage });
function TaskPage() {
	const { taskId } = Route.useParams();
	return <CheckmateApp detailId={taskId} />;
}
