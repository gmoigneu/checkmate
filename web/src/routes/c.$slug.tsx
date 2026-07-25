import { createFileRoute } from "@tanstack/react-router";
import { CheckmateApp } from "@/components/checkmate-app";
export const Route = createFileRoute("/c/$slug")({
	component: () => <CheckmateApp />,
});
