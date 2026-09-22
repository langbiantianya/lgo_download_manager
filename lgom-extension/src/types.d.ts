/**
 * Minimal ambient declarations for the WebExtension API surface this extension
 * touches. There is no `@types/chrome` dependency in the project, so the parts
 * used are declared here: the promise-first `browser` object and the
 * callback-first `chrome` object are structurally interchangeable for these
 * call sites, so both are declared as `WebExtensionApi`.
 */

interface WebExtensionRequestHeader {
	name: string;
	value?: string;
}

interface WebExtensionHeaderEvent {
	url: string;
	requestHeaders?: WebExtensionRequestHeader[];
}

interface WebExtensionDownloadItem {
	id: string;
	url: string;
	filename: string;
	referrer: string;
	mime: string;
}

interface WebExtensionTab {
	id?: number;
	url?: string;
}

interface WebExtensionApi {
	i18n: {
		getMessage(messageName: string, substitutions?: string | string[]): string;
	};
	runtime: {
		getManifest(): { manifest_version?: number };
		getBrowserInfo?(): Promise<{ name: string; vendor: string }>;
	};
	downloads: {
		onCreated: {
			addListener(listener: (item: WebExtensionDownloadItem) => void): void;
		};
		cancel(id: string): Promise<void>;
		erase(ids: { id: string }): Promise<void>;
	};
	webRequest: {
		onBeforeSendHeaders: {
			addListener(
				listener: (event: WebExtensionHeaderEvent) => void,
				filter: { urls: string[] },
				extra?: string[]
			): void;
		};
	};
	storage: {
		local: WebExtensionStorageArea;
		session: WebExtensionStorageArea;
	};
	tabs: {
		create(properties: { url: string; active?: boolean }): Promise<WebExtensionTab>;
		query(properties: { active?: boolean; currentWindow?: boolean }): Promise<WebExtensionTab[]>;
		remove(id: number): Promise<void>;
	};
}

interface WebExtensionStorageArea {
	get(keys?: string | string[]): Promise<Record<string, unknown>>;
	set(items: Record<string, unknown>): Promise<void>;
	remove(keys: string | string[]): Promise<void>;
}

declare global {
	// eslint-disable-next-line no-var
	var chrome: WebExtensionApi;
	// eslint-disable-next-line no-var
	var browser: WebExtensionApi;
}

export {};
