import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createRootRoute, HeadContent, Scripts } from "@tanstack/react-router";
import { type ReactNode, useEffect } from "react";
import { TooltipProvider } from "@/components/ui/tooltip";

import appCss from "../styles.css?url";

const queryClient = new QueryClient({
	defaultOptions: {
		queries: { staleTime: 20_000, refetchOnWindowFocus: true },
	},
});

export const Route = createRootRoute({
	head: () => ({
		meta: [
			{
				charSet: "utf-8",
			},
			{
				name: "viewport",
				content: "width=device-width, initial-scale=1",
			},
			{
				title: "Checkmate — a calm place for work",
			},
		],
		links: [
			{
				rel: "stylesheet",
				href: appCss,
			},
		],
	}),
	shellComponent: RootDocument,
});

function RootDocument({ children }: { children: ReactNode }) {
	useEffect(() => {
		const root = document.documentElement;
		root.dataset.appearance =
			window.localStorage.getItem("checkmate:appearance") ?? "system";
		root.dataset.density =
			window.localStorage.getItem("checkmate:density") ?? "comfortable";
		root.dataset.reduceMotion =
			window.localStorage.getItem("checkmate:reduce-motion") ?? "false";
	}, []);

	return (
		<html lang="en">
			<head>
				<HeadContent />
			</head>
			<body>
				<QueryClientProvider client={queryClient}>
					<TooltipProvider>{children}</TooltipProvider>
				</QueryClientProvider>

				<Scripts />
			</body>
		</html>
	);
}
