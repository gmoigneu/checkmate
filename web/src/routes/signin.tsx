import { useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { LoaderCircle, ShieldCheck } from "lucide-react";
import { useEffect } from "react";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
export const Route = createFileRoute("/signin")({ component: SignIn });
function SignIn() {
	const search = Route.useSearch() as { redirect_to?: string };
	const config = useQuery({
		queryKey: ["auth-config"],
		queryFn: api.authConfig,
		retry: false,
	});
	useEffect(() => {
		void api
			.me()
			.then(() => {
				window.location.assign(search.redirect_to || "/");
			})
			.catch(() => undefined);
	}, [search.redirect_to]);
	return (
		<main className="grid min-h-screen place-items-center bg-[var(--page)] p-5">
			<section className="w-full max-w-md rounded-3xl border border-border bg-card p-8 text-center shadow-xl shadow-black/5">
				<div className="mx-auto mb-6 grid size-14 place-items-center rounded-2xl bg-[var(--coral)] text-2xl font-black text-white">
					C
				</div>
				<p className="mb-2 font-display text-3xl tracking-tight">Checkmate</p>
				<p className="mb-8 text-sm leading-6 text-muted-foreground">
					One calm place for all the work that crosses your mind.
				</p>
				{config.isLoading ? (
					<div className="grid min-h-11 place-items-center rounded-xl bg-muted">
						<LoaderCircle className="size-5 animate-spin text-muted-foreground" />
					</div>
				) : config.data?.providers.length ? (
					<div className="space-y-3">
						{config.data.providers.map((provider) => (
							<Button
								key={provider}
								className="h-11 w-full bg-[var(--ink)] hover:bg-[var(--ink)]/90"
								onClick={() => {
									window.location.assign(
										`/auth/login/${provider}?redirect_to=${encodeURIComponent(search.redirect_to || "/")}`,
									);
								}}
							>
								Continue with {provider === "google" ? "Google" : provider}
							</Button>
						))}
					</div>
				) : (
					<div className="rounded-2xl bg-muted p-4 text-left text-sm leading-6 text-muted-foreground">
						<ShieldCheck className="mb-2 size-5 text-[var(--coral)]" />
						This server has no identity provider set up. Configure{" "}
						<code>CHECKMATE_GOOGLE_CLIENT_ID</code> and{" "}
						<code>CHECKMATE_GOOGLE_CLIENT_SECRET</code>, or use a device token
						through the CLI.
					</div>
				)}
				<p className="mt-8 text-xs text-muted-foreground">
					Self-hosted and private by design.
				</p>
			</section>
		</main>
	);
}
