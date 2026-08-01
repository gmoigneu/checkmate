import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import {
	ArrowLeft,
	Check,
	ChevronRight,
	CircleUserRound,
	Copy,
	Database,
	ExternalLink,
	Eye,
	Info,
	KeyRound,
	Laptop,
	LoaderCircle,
	LogOut,
	MonitorCog,
	Palette,
	Plus,
	RefreshCw,
	Repeat2,
	Server,
	ShieldCheck,
	Smartphone,
	Trash2,
	UsersRound,
} from "lucide-react";
import { useEffect, useState } from "react";
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
import { ApiError, api } from "@/lib/api";
import { formatTimestamp } from "@/lib/format";
import type { CreatedDeviceToken, Me, OAuthGrant } from "@/lib/types";
import { cn } from "@/lib/utils";

type SettingsSection =
	| "account"
	| "contexts"
	| "people"
	| "repeating"
	| "devices"
	| "apps"
	| "appearance"
	| "about";

const settingsSections: Array<{
	id: SettingsSection;
	label: string;
	description: string;
	icon: typeof CircleUserRound;
}> = [
	{
		id: "account",
		label: "Account",
		description: "Profile and sessions",
		icon: CircleUserRound,
	},
	{
		id: "contexts",
		label: "Contexts",
		description: "Organize your work",
		icon: MonitorCog,
	},
	{
		id: "people",
		label: "People",
		description: "Delegation contacts",
		icon: UsersRound,
	},
	{
		id: "repeating",
		label: "Repeating",
		description: "Recurring work",
		icon: Repeat2,
	},
	{
		id: "devices",
		label: "Devices & tokens",
		description: "Long-lived access",
		icon: KeyRound,
	},
	{
		id: "apps",
		label: "Connected apps",
		description: "OAuth connections",
		icon: Laptop,
	},
	{
		id: "appearance",
		label: "Appearance",
		description: "Theme and motion",
		icon: Palette,
	},
	{
		id: "about",
		label: "About",
		description: "Server and sync health",
		icon: Info,
	},
];

function sectionFromPath(pathname: string): SettingsSection | undefined {
	const section = pathname.split("/")[2];
	return settingsSections.some((item) => item.id === section)
		? (section as SettingsSection)
		: undefined;
}

export function SettingsPage({ me, pathname }: { me?: Me; pathname: string }) {
	const selected = sectionFromPath(pathname);
	const desktopSection = selected ?? "account";

	return (
		<section className="mx-auto max-w-6xl">
			<div className={cn("mb-8", selected && "hidden md:block")}>
				<p className="mb-2 text-sm font-medium text-muted-foreground">
					Your space, your rules.
				</p>
				<h1 className="m-0 font-display text-4xl tracking-tight">Settings</h1>
			</div>

			<div className="md:grid md:grid-cols-[230px_minmax(0,1fr)] md:gap-10">
				<nav
					aria-label="Settings"
					className={cn(
						"overflow-hidden rounded-2xl border border-border bg-card md:self-start",
						selected && "hidden md:block",
					)}
				>
					{settingsSections.map((item) => (
						<Link
							key={item.id}
							to="/settings/$section"
							params={{ section: item.id }}
							className={cn(
								"flex min-h-15 items-center gap-3 border-b border-border px-4 py-3 text-left no-underline last:border-0 hover:bg-muted/50",
								desktopSection === item.id &&
									"bg-[var(--surface-selected)] text-[var(--text-accent)]",
							)}
						>
							<item.icon className="size-4 shrink-0" />
							<span className="min-w-0 flex-1">
								<span className="block font-medium text-[var(--text-primary)]">
									{item.label}
								</span>
								<span className="block truncate text-xs text-muted-foreground md:hidden">
									{item.description}
								</span>
							</span>
							<ChevronRight className="size-4 text-muted-foreground md:hidden" />
						</Link>
					))}
				</nav>

				<div className={cn(!selected && "hidden md:block")}>
					{selected ? (
						<Link
							to="/settings"
							className="mb-5 inline-flex items-center gap-2 text-sm text-muted-foreground no-underline hover:text-foreground md:hidden"
						>
							<ArrowLeft className="size-4" /> Settings
						</Link>
					) : null}
					<SettingsSectionContent section={desktopSection} me={me} />
				</div>
			</div>
		</section>
	);
}

function SettingsSectionContent({
	section,
	me,
}: {
	section: SettingsSection;
	me?: Me;
}) {
	switch (section) {
		case "account":
			return <AccountSettings me={me} />;
		case "contexts":
			return <ContextsSettings />;
		case "people":
			return <PeopleSettings />;
		case "repeating":
			return <RepeatingSettings />;
		case "devices":
			return <DeviceTokenSettings me={me} />;
		case "apps":
			return <ConnectedAppsSettings />;
		case "appearance":
			return <AppearanceSettings />;
		case "about":
			return <AboutSettings />;
	}
}

function SectionHeading({
	title,
	description,
	action,
}: {
	title: string;
	description: string;
	action?: React.ReactNode;
}) {
	return (
		<div className="mb-6 flex items-start justify-between gap-4">
			<div>
				<h2 className="m-0 text-3xl tracking-tight">{title}</h2>
				<p className="mt-1.5 mb-0 text-sm text-muted-foreground">
					{description}
				</p>
			</div>
			{action}
		</div>
	);
}

function SettingsCard({ children }: { children: React.ReactNode }) {
	return (
		<div className="overflow-hidden rounded-2xl border border-border bg-card">
			{children}
		</div>
	);
}

function DetailRow({
	label,
	value,
	icon: Icon,
}: {
	label: string;
	value: React.ReactNode;
	icon?: typeof CircleUserRound;
}) {
	return (
		<div className="flex min-h-15 items-center gap-3 border-b border-border px-5 py-3 last:border-0">
			{Icon ? <Icon className="size-4 text-muted-foreground" /> : null}
			<span className="font-medium">{label}</span>
			<span className="ml-auto min-w-0 text-right text-sm text-muted-foreground">
				{value}
			</span>
		</div>
	);
}

function AccountSettings({ me }: { me?: Me }) {
	const [confirmEverywhere, setConfirmEverywhere] = useState(false);
	const logout = useMutation({
		mutationFn: (everywhere: boolean) => api.logout(everywhere),
		onSuccess: () => window.location.assign("/signin"),
	});

	return (
		<>
			<SectionHeading
				title="Account"
				description="Your identity and signed-in browser sessions."
			/>
			<SettingsCard>
				<DetailRow label="Name" value={me?.name ?? "Loading…"} />
				<DetailRow label="Email" value={me?.email ?? "Loading…"} />
				<DetailRow label="Timezone" value={me?.timezone ?? "Loading…"} />
				<DetailRow
					label="Authentication"
					value={
						me?.auth_via === "session" ? "Browser session" : "Access token"
					}
				/>
			</SettingsCard>
			<p className="mt-3 text-xs text-muted-foreground">
				Profile fields are managed by your identity provider. This server does
				not yet expose an account-update endpoint.
			</p>
			<div className="mt-8 flex flex-wrap gap-3">
				<Button
					variant="outline"
					disabled={logout.isPending || me?.auth_via !== "session"}
					onClick={() => logout.mutate(false)}
				>
					<LogOut /> Sign out
				</Button>
				<Button
					variant="destructive"
					disabled={logout.isPending || me?.auth_via !== "session"}
					onClick={() => setConfirmEverywhere(true)}
				>
					<ShieldCheck /> Sign out everywhere
				</Button>
			</div>
			{logout.error ? <InlineError error={logout.error} /> : null}
			<Dialog open={confirmEverywhere} onOpenChange={setConfirmEverywhere}>
				<DialogContent>
					<DialogHeader>
						<DialogTitle>Sign out of every browser?</DialogTitle>
						<DialogDescription>
							Every Checkmate browser session will be revoked. Device tokens and
							connected apps are not affected.
						</DialogDescription>
					</DialogHeader>
					<DialogFooter>
						<Button
							variant="outline"
							onClick={() => setConfirmEverywhere(false)}
						>
							Cancel
						</Button>
						<Button variant="destructive" onClick={() => logout.mutate(true)}>
							Sign out everywhere
						</Button>
					</DialogFooter>
				</DialogContent>
			</Dialog>
		</>
	);
}

function ContextsSettings() {
	return (
		<>
			<SectionHeading
				title="Contexts"
				description="Create, reorder, archive, and manage the projects inside each context."
			/>
			<EntryCard
				to="/settings/contexts"
				icon={MonitorCog}
				title="Manage contexts & projects"
				description="Archived contexts remain available from the management screen."
			/>
		</>
	);
}

function PeopleSettings() {
	return (
		<>
			<SectionHeading
				title="People"
				description="People you delegate work to and follow up with."
			/>
			<EntryCard
				to="/waiting"
				icon={UsersRound}
				title="Open delegated work"
				description="Review every person and the tasks currently waiting on them."
			/>
		</>
	);
}

function RepeatingSettings() {
	return (
		<>
			<SectionHeading
				title="Repeating"
				description="Manage recurring tasks and your daily routine."
			/>
			<div className="grid gap-3 sm:grid-cols-2">
				<EntryCard
					to="/repeating"
					icon={Repeat2}
					title="Recurring tasks"
					description="Review work generated on a schedule."
				/>
				<EntryCard
					to="/routine"
					icon={Smartphone}
					title="Daily routine"
					description="Edit time-of-day routine templates."
				/>
			</div>
		</>
	);
}

function EntryCard({
	to,
	icon: Icon,
	title,
	description,
}: {
	to: "/settings/contexts" | "/waiting" | "/repeating" | "/routine";
	icon: typeof CircleUserRound;
	title: string;
	description: string;
}) {
	return (
		<Link
			to={to}
			className="flex items-center gap-4 rounded-2xl border border-border bg-card p-5 text-left no-underline hover:bg-muted/40"
		>
			<span className="grid size-10 shrink-0 place-items-center rounded-xl bg-[var(--accent-soft)] text-[var(--text-accent)]">
				<Icon className="size-5" />
			</span>
			<span className="min-w-0 flex-1">
				<span className="block font-medium text-foreground">{title}</span>
				<span className="mt-1 block text-sm text-muted-foreground">
					{description}
				</span>
			</span>
			<ChevronRight className="size-4 text-muted-foreground" />
		</Link>
	);
}

function DeviceTokenSettings({ me }: { me?: Me }) {
	const queryClient = useQueryClient();
	const tokens = useQuery({
		queryKey: ["tokens"],
		queryFn: api.tokens,
		retry: false,
	});
	const [creating, setCreating] = useState(false);
	const [created, setCreated] = useState<CreatedDeviceToken>();
	const revoke = useMutation({
		mutationFn: api.revokeToken,
		onSuccess: () => queryClient.invalidateQueries({ queryKey: ["tokens"] }),
	});
	const canCreate = me?.auth_via === "session" && me.scopes.includes("write");

	return (
		<>
			<SectionHeading
				title="Devices & tokens"
				description="Credentials for native apps, scripts, and devices that cannot use browser sign-in."
				action={
					<Button disabled={!canCreate} onClick={() => setCreating(true)}>
						<Plus /> New token
					</Button>
				}
			/>
			{!canCreate ? (
				<Notice>
					New tokens can only be created from a write-enabled browser session. A
					device token cannot mint another credential.
				</Notice>
			) : null}
			{tokens.isLoading ? (
				<LoadingState label="Loading devices…" />
			) : tokens.error ? (
				<ErrorState error={tokens.error} onRetry={() => tokens.refetch()} />
			) : tokens.data?.data.length ? (
				<div className="grid gap-3">
					{tokens.data.data.map((token) => {
						const inactive = Boolean(token.revoked_at);
						return (
							<article
								key={token.id}
								className={cn(
									"rounded-2xl border border-border bg-card p-5",
									inactive && "opacity-65",
								)}
							>
								<div className="flex items-start gap-3">
									<span className="grid size-9 shrink-0 place-items-center rounded-xl bg-muted text-muted-foreground">
										<KeyRound className="size-4" />
									</span>
									<div className="min-w-0 flex-1">
										<div className="flex flex-wrap items-center gap-2">
											<h3 className="m-0 font-sans text-base font-medium">
												{token.name}
											</h3>
											<StatusPill active={!inactive} />
										</div>
										<p className="mt-1 mb-0 text-sm text-muted-foreground">
											{token.scopes.join(" + ")} access · created{" "}
											{formatTimestamp(token.created_at)}
										</p>
										<p className="mt-2 mb-0 text-sm">
											<strong className="font-medium">Last used:</strong>{" "}
											{token.last_used_at
												? formatTimestamp(token.last_used_at)
												: "Never"}
											<span className="text-muted-foreground">
												{" "}
												· Expires{" "}
												{token.expires_at
													? formatTimestamp(token.expires_at)
													: "never"}
											</span>
										</p>
									</div>
									{!inactive ? (
										<Button
											variant="ghost"
											size="icon-sm"
											disabled={revoke.isPending}
											onClick={() => {
												if (
													window.confirm(
														`Revoke “${token.name}”? It will stop working immediately.`,
													)
												)
													revoke.mutate(token.id);
											}}
											aria-label={`Revoke ${token.name}`}
										>
											<Trash2 />
										</Button>
									) : null}
								</div>
							</article>
						);
					})}
				</div>
			) : (
				<EmptyState
					icon={KeyRound}
					title="No device tokens"
					description="Create one when a device or script cannot connect with OAuth."
				/>
			)}
			{revoke.error ? <InlineError error={revoke.error} /> : null}
			<CreateTokenDialog
				open={creating}
				onOpenChange={setCreating}
				onCreated={(value) => {
					setCreating(false);
					setCreated(value);
					queryClient.invalidateQueries({ queryKey: ["tokens"] });
				}}
			/>
			<CreatedTokenDialog
				token={created}
				onDismiss={() => setCreated(undefined)}
			/>
		</>
	);
}

function CreateTokenDialog({
	open,
	onOpenChange,
	onCreated,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	onCreated: (token: CreatedDeviceToken) => void;
}) {
	const [name, setName] = useState("");
	const [scopes, setScopes] = useState<Array<"read" | "write">>([
		"read",
		"write",
	]);
	const [expiresOn, setExpiresOn] = useState("");
	const mutation = useMutation({
		mutationFn: api.createToken,
		onSuccess: onCreated,
	});
	const fields =
		mutation.error instanceof ApiError ? mutation.error.fields : {};

	useEffect(() => {
		if (!open) return;
		setName("");
		setScopes(["read", "write"]);
		setExpiresOn("");
		mutation.reset();
	}, [open, mutation.reset]);

	const toggleScope = (scope: "read" | "write") =>
		setScopes((current) =>
			current.includes(scope)
				? current.filter((item) => item !== scope)
				: [...current, scope],
		);

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent>
				<DialogHeader>
					<DialogTitle>Create a device token</DialogTitle>
					<DialogDescription>
						Name the device that will hold this credential and grant only the
						access it needs.
					</DialogDescription>
				</DialogHeader>
				<form
					className="grid gap-5"
					onSubmit={(event) => {
						event.preventDefault();
						mutation.mutate({
							name: name.trim(),
							scopes,
							...(expiresOn
								? { expires_at: `${expiresOn}T23:59:59.000Z` }
								: {}),
						});
					}}
				>
					<label
						htmlFor="token-name"
						className="grid gap-1.5 text-sm font-medium"
					>
						Device or integration name
						<Input
							id="token-name"
							value={name}
							onChange={(event) => setName(event.target.value)}
							placeholder="iPhone, Raycast, home server…"
							autoFocus
							aria-invalid={Boolean(fields.name)}
						/>
						{fields.name ? <FieldError>{fields.name}</FieldError> : null}
					</label>
					<fieldset className="grid gap-2">
						<legend className="mb-1 text-sm font-medium">Scopes</legend>
						{(["read", "write"] as const).map((scope) => (
							<label key={scope} className="flex items-start gap-3 text-sm">
								<input
									type="checkbox"
									checked={scopes.includes(scope)}
									onChange={() => toggleScope(scope)}
									className="mt-1 accent-[var(--accent)]"
								/>
								<span>
									<strong className="capitalize">{scope}</strong>
									<span className="block text-muted-foreground">
										{scope === "read"
											? "See tasks, projects, contexts, and people."
											: "Create, change, and delete Checkmate data."}
									</span>
								</span>
							</label>
						))}
						{fields.scopes ? <FieldError>{fields.scopes}</FieldError> : null}
					</fieldset>
					<label
						htmlFor="token-expires-on"
						className="grid gap-1.5 text-sm font-medium"
					>
						Expires on{" "}
						<span className="font-normal text-muted-foreground">
							(optional)
						</span>
						<Input
							id="token-expires-on"
							type="date"
							value={expiresOn}
							onChange={(event) => setExpiresOn(event.target.value)}
							min={new Date().toISOString().slice(0, 10)}
							aria-invalid={Boolean(fields.expires_at)}
						/>
						{fields.expires_at ? (
							<FieldError>{fields.expires_at}</FieldError>
						) : null}
					</label>
					{mutation.error ? <InlineError error={mutation.error} /> : null}
					<DialogFooter>
						<Button
							type="button"
							variant="outline"
							onClick={() => onOpenChange(false)}
						>
							Cancel
						</Button>
						<Button
							type="submit"
							disabled={
								!name.trim() || scopes.length === 0 || mutation.isPending
							}
						>
							{mutation.isPending ? (
								<LoaderCircle className="animate-spin" />
							) : (
								<KeyRound />
							)}
							Create token
						</Button>
					</DialogFooter>
				</form>
			</DialogContent>
		</Dialog>
	);
}

function CreatedTokenDialog({
	token,
	onDismiss,
}: {
	token?: CreatedDeviceToken;
	onDismiss: () => void;
}) {
	const [copied, setCopied] = useState(false);
	const [acknowledged, setAcknowledged] = useState(false);

	useEffect(() => {
		if (!token) return;
		setCopied(false);
		setAcknowledged(false);
	}, [token]);

	return (
		<Dialog
			open={Boolean(token)}
			onOpenChange={(open) => {
				if (!open && acknowledged) onDismiss();
			}}
		>
			<DialogContent
				showCloseButton={false}
				onEscapeKeyDown={(event) => event.preventDefault()}
				onPointerDownOutside={(event) => event.preventDefault()}
			>
				<DialogHeader>
					<DialogTitle>Save this token now</DialogTitle>
					<DialogDescription>
						This is the only time Checkmate can show the secret. Store it in the
						device or password manager that will use it.
					</DialogDescription>
				</DialogHeader>
				<div className="flex items-center gap-2 rounded-xl border border-border bg-muted p-3">
					<code className="min-w-0 flex-1 overflow-hidden text-ellipsis whitespace-nowrap text-sm">
						{token?.token}
					</code>
					<Button
						variant="outline"
						size="sm"
						onClick={async () => {
							if (!token) return;
							await navigator.clipboard.writeText(token.token);
							setCopied(true);
						}}
					>
						{copied ? <Check /> : <Copy />} {copied ? "Copied" : "Copy"}
					</Button>
				</div>
				<label className="flex items-start gap-3 text-sm">
					<input
						type="checkbox"
						checked={acknowledged}
						onChange={(event) => setAcknowledged(event.target.checked)}
						className="mt-1 accent-[var(--accent)]"
					/>
					<span>
						I saved this token. I understand it cannot be shown again.
					</span>
				</label>
				<DialogFooter>
					<Button disabled={!acknowledged} onClick={onDismiss}>
						Done
					</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}

function ConnectedAppsSettings() {
	const queryClient = useQueryClient();
	const grants = useQuery({
		queryKey: ["grants"],
		queryFn: api.grants,
		retry: false,
	});
	const revoke = useMutation({
		mutationFn: api.revokeGrant,
		onSuccess: () => queryClient.invalidateQueries({ queryKey: ["grants"] }),
	});

	return (
		<>
			<SectionHeading
				title="Connected apps"
				description="Applications you have authorised through OAuth."
			/>
			{grants.isLoading ? (
				<LoadingState label="Loading connected apps…" />
			) : grants.error ? (
				<ErrorState error={grants.error} onRetry={() => grants.refetch()} />
			) : grants.data?.data.length ? (
				<div className="grid gap-3">
					{grants.data.data.map((grant) => (
						<GrantCard
							key={grant.id}
							grant={grant}
							disconnecting={revoke.isPending}
							onDisconnect={() => {
								if (
									window.confirm(
										`Disconnect ${safeClientName(grant.client_name)}? It will need your approval to connect again.`,
									)
								)
									revoke.mutate(grant.id);
							}}
						/>
					))}
				</div>
			) : (
				<EmptyState
					icon={Laptop}
					title="No connected apps"
					description="Apps will appear here after you approve their access."
				/>
			)}
			{revoke.error ? <InlineError error={revoke.error} /> : null}
		</>
	);
}

function GrantCard({
	grant,
	disconnecting,
	onDisconnect,
}: {
	grant: OAuthGrant;
	disconnecting: boolean;
	onDisconnect: () => void;
}) {
	const name = safeClientName(grant.client_name);
	return (
		<article className="rounded-2xl border border-border bg-card p-5">
			<div className="flex items-start gap-3">
				<span className="grid size-9 shrink-0 place-items-center rounded-xl bg-muted text-muted-foreground">
					<Laptop className="size-4" />
				</span>
				<div className="min-w-0 flex-1">
					<h3
						dir="auto"
						className="m-0 max-w-full overflow-hidden font-sans text-base font-medium text-ellipsis whitespace-nowrap [unicode-bidi:plaintext]"
						title={name}
					>
						{name}
					</h3>
					<p className="mt-1 mb-0 text-sm text-muted-foreground">
						{grant.scopes.join(" + ")} access · connected{" "}
						{formatTimestamp(grant.created_at)}
					</p>
					<p className="mt-2 mb-0 truncate font-mono text-xs text-muted-foreground">
						Audience: {grant.audience}
					</p>
					{safeClientURL(grant.client_uri) ? (
						<a
							href={safeClientURL(grant.client_uri) ?? undefined}
							target="_blank"
							rel="noreferrer"
							className="mt-2 inline-flex items-center gap-1 text-xs text-[var(--text-accent)]"
						>
							Client website <ExternalLink className="size-3" />
						</a>
					) : null}
				</div>
				<Button
					variant="outline"
					size="sm"
					disabled={disconnecting}
					onClick={onDisconnect}
				>
					Disconnect
				</Button>
			</div>
		</article>
	);
}

function safeClientName(value: string) {
	const normalized = value.trim();
	if (!normalized) return "Unnamed client";
	return Array.from(normalized).slice(0, 200).join("");
}

function safeClientURL(value: string | null) {
	if (!value) return null;
	try {
		const parsed = new URL(value);
		return parsed.protocol === "https:" || parsed.protocol === "http:"
			? parsed.toString()
			: null;
	} catch {
		return null;
	}
}

type Appearance = "system" | "light" | "dark";
type Density = "comfortable" | "compact";

function AppearanceSettings() {
	const [appearance, setAppearance] = useState<Appearance>("system");
	const [density, setDensity] = useState<Density>("comfortable");
	const [reduceMotion, setReduceMotion] = useState(false);

	useEffect(() => {
		const savedAppearance = window.localStorage.getItem("checkmate:appearance");
		const savedDensity = window.localStorage.getItem("checkmate:density");
		setAppearance(
			savedAppearance === "light" || savedAppearance === "dark"
				? savedAppearance
				: "system",
		);
		setDensity(savedDensity === "compact" ? "compact" : "comfortable");
		setReduceMotion(
			window.localStorage.getItem("checkmate:reduce-motion") === "true",
		);
	}, []);

	useEffect(() => {
		window.localStorage.setItem("checkmate:appearance", appearance);
		window.localStorage.setItem("checkmate:density", density);
		window.localStorage.setItem(
			"checkmate:reduce-motion",
			String(reduceMotion),
		);
		document.documentElement.dataset.appearance = appearance;
		document.documentElement.dataset.density = density;
		document.documentElement.dataset.reduceMotion = String(reduceMotion);
	}, [appearance, density, reduceMotion]);

	return (
		<>
			<SectionHeading
				title="Appearance"
				description="These preferences are stored in this browser."
			/>
			<div className="grid gap-6">
				<ChoiceGroup
					label="Theme"
					value={appearance}
					onChange={(value) => setAppearance(value as Appearance)}
					options={[
						{ value: "system", label: "System", icon: MonitorCog },
						{ value: "light", label: "Light", icon: Eye },
						{ value: "dark", label: "Dark", icon: Palette },
					]}
				/>
				<ChoiceGroup
					label="Density"
					value={density}
					onChange={(value) => setDensity(value as Density)}
					options={[
						{ value: "comfortable", label: "Comfortable", icon: Smartphone },
						{ value: "compact", label: "Compact", icon: Database },
					]}
				/>
				<label className="flex items-center gap-4 rounded-2xl border border-border bg-card p-5">
					<span className="min-w-0 flex-1">
						<span className="block font-medium">Reduce motion</span>
						<span className="mt-1 block text-sm text-muted-foreground">
							Minimize non-essential interface animation in addition to your OS
							setting.
						</span>
					</span>
					<input
						type="checkbox"
						checked={reduceMotion}
						onChange={(event) => setReduceMotion(event.target.checked)}
						className="size-4 accent-[var(--accent)]"
					/>
				</label>
			</div>
		</>
	);
}

function ChoiceGroup({
	label,
	value,
	onChange,
	options,
}: {
	label: string;
	value: string;
	onChange: (value: string) => void;
	options: Array<{
		value: string;
		label: string;
		icon: typeof CircleUserRound;
	}>;
}) {
	return (
		<fieldset>
			<legend className="mb-2 text-sm font-medium">{label}</legend>
			<div className="grid gap-2 sm:grid-cols-3">
				{options.map((option) => (
					<label
						key={option.value}
						className={cn(
							"flex cursor-pointer items-center gap-3 rounded-xl border bg-card p-4",
							value === option.value
								? "border-[var(--accent)] bg-[var(--surface-selected)]"
								: "border-border",
						)}
					>
						<input
							type="radio"
							name={label}
							value={option.value}
							checked={value === option.value}
							onChange={() => onChange(option.value)}
							className="sr-only"
						/>
						<option.icon className="size-4 text-muted-foreground" />
						<span className="font-medium">{option.label}</span>
						{value === option.value ? (
							<Check className="ml-auto size-4 text-[var(--text-accent)]" />
						) : null}
					</label>
				))}
			</div>
		</fieldset>
	);
}

function AboutSettings() {
	const queryClient = useQueryClient();
	const health = useQuery({
		queryKey: ["health"],
		queryFn: api.health,
		retry: false,
	});
	const [refreshedAt, setRefreshedAt] = useState<Date>();

	return (
		<>
			<SectionHeading
				title="About"
				description="Build information and the current connection to your Checkmate server."
			/>
			<SettingsCard>
				<DetailRow
					label="Version"
					value={health.data?.version ?? "—"}
					icon={Info}
				/>
				<DetailRow
					label="Server"
					value={health.data?.status === "ok" ? "Healthy" : "Unavailable"}
					icon={Server}
				/>
				<DetailRow
					label="Database"
					value={health.data?.database === "ok" ? "Connected" : "Unknown"}
					icon={Database}
				/>
				<DetailRow
					label="Web data"
					value={
						refreshedAt
							? `Refreshed ${refreshedAt.toLocaleTimeString()}`
							: "Live from server"
					}
					icon={RefreshCw}
				/>
			</SettingsCard>
			{health.error ? <InlineError error={health.error} /> : null}
			<div className="mt-6 flex flex-wrap gap-3">
				<Button
					variant="outline"
					onClick={async () => {
						await queryClient.invalidateQueries();
						setRefreshedAt(new Date());
					}}
				>
					<RefreshCw /> Refresh all data
				</Button>
				<a
					href="https://github.com/nls/checkmate/blob/main/specs/openapi.yaml"
					target="_blank"
					rel="noreferrer"
					className="inline-flex h-9 items-center justify-center gap-2 rounded-md px-4 text-sm font-medium text-[var(--text-accent)] hover:bg-muted"
				>
					API documentation <ExternalLink className="size-4" />
				</a>
			</div>
		</>
	);
}

function StatusPill({ active }: { active: boolean }) {
	return (
		<span
			className={cn(
				"rounded-full px-2 py-0.5 text-[11px] font-medium",
				active
					? "bg-[var(--olive-050)] text-[var(--olive-600)]"
					: "bg-muted text-muted-foreground",
			)}
		>
			{active ? "Active" : "Revoked"}
		</span>
	);
}

function LoadingState({ label }: { label: string }) {
	return (
		<div className="flex items-center justify-center gap-2 rounded-2xl border border-border bg-card p-10 text-sm text-muted-foreground">
			<LoaderCircle className="size-4 animate-spin" /> {label}
		</div>
	);
}

function EmptyState({
	icon: Icon,
	title,
	description,
}: {
	icon: typeof CircleUserRound;
	title: string;
	description: string;
}) {
	return (
		<div className="rounded-2xl border border-dashed border-border bg-card p-10 text-center">
			<Icon className="mx-auto size-7 text-muted-foreground" />
			<h3 className="mt-3 mb-1 font-sans text-base font-medium">{title}</h3>
			<p className="m-0 text-sm text-muted-foreground">{description}</p>
		</div>
	);
}

function Notice({ children }: { children: React.ReactNode }) {
	return (
		<div className="mb-5 rounded-xl border border-[var(--ochre-300)] bg-[var(--ochre-050)] px-4 py-3 text-sm text-[var(--ochre-600)]">
			{children}
		</div>
	);
}

function ErrorState({ error, onRetry }: { error: Error; onRetry: () => void }) {
	return (
		<div className="rounded-2xl border border-[var(--task-overdue-border)] bg-[var(--task-overdue-bg)] p-6">
			<p className="mt-0 mb-4 text-sm text-[var(--task-overdue-fg)]">
				{error.message}
			</p>
			<Button variant="outline" size="sm" onClick={onRetry}>
				<RefreshCw /> Try again
			</Button>
		</div>
	);
}

function InlineError({ error }: { error: Error }) {
	return (
		<p className="mt-3 text-sm text-[var(--task-overdue-fg)]" role="alert">
			{error.message}
		</p>
	);
}

function FieldError({ children }: { children: React.ReactNode }) {
	return (
		<span className="text-xs text-[var(--task-overdue-fg)]">{children}</span>
	);
}
