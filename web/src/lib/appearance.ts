export type Appearance = "system" | "light" | "dark";
export type Density = "comfortable" | "compact";

export interface AppearancePreferences {
	appearance: Appearance;
	density: Density;
	reduceMotion: boolean;
}

export const defaultAppearancePreferences: AppearancePreferences = {
	appearance: "system",
	density: "comfortable",
	reduceMotion: false,
};

export function browserAppearanceStorage(): Storage | undefined {
	if (typeof window === "undefined") return undefined;
	try {
		return window.localStorage;
	} catch {
		return undefined;
	}
}

export function readAppearancePreferences(
	storage?: Pick<Storage, "getItem">,
): AppearancePreferences {
	if (!storage) return defaultAppearancePreferences;
	try {
		const appearance = storage.getItem("checkmate:appearance");
		return {
			appearance:
				appearance === "light" || appearance === "dark" ? appearance : "system",
			density:
				storage.getItem("checkmate:density") === "compact"
					? "compact"
					: "comfortable",
			reduceMotion: storage.getItem("checkmate:reduce-motion") === "true",
		};
	} catch {
		return defaultAppearancePreferences;
	}
}

export function applyAppearancePreferences(
	preferences: AppearancePreferences,
	root: HTMLElement,
	prefersDark: boolean,
) {
	root.dataset.appearance = preferences.appearance;
	root.dataset.effectiveAppearance =
		preferences.appearance === "system"
			? prefersDark
				? "dark"
				: "light"
			: preferences.appearance;
	root.dataset.density = preferences.density;
	root.dataset.reduceMotion = String(preferences.reduceMotion);
}

export function saveAppearancePreferences(
	preferences: AppearancePreferences,
	storage?: Pick<Storage, "setItem">,
) {
	if (!storage) return;
	try {
		storage.setItem("checkmate:appearance", preferences.appearance);
		storage.setItem("checkmate:density", preferences.density);
		storage.setItem(
			"checkmate:reduce-motion",
			String(preferences.reduceMotion),
		);
	} catch {
		// The current page still applies preferences when persistence is blocked.
	}
}
