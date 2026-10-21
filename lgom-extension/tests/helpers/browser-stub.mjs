// 打桩浏览器扩展 API。
//
// 扩展源码通过 `globalThis.browser || globalThis.chrome` 取 API
// （src/lib/platform-api.js 在 import 时求值），所以桩必须在 import
// 扩展模块之前装好。Chrome 只暴露 chrome，Firefox 只暴露 browser，
// 两边分开装，免得 platform-api 缓存到另一个平台的桩。
//
// 桩同时记录调用，供断言使用（下载有没有被取消、标签页有没有被导航到
// lgom:// 等等）。

/**
 * @param {Map<string, unknown>} store
 */
function makeStorage(store) {
	return {
		/**
		 * @param {string} key
		 * @returns {Promise<Record<string, unknown>>}
		 */
		async get(key) {
			return store.has(key) ? { [key]: store.get(key) } : {};
		},
		/**
		 * @param {Record<string, unknown>} obj
		 * @returns {Promise<void>}
		 */
		async set(obj) {
			for (const [k, v] of Object.entries(obj)) store.set(k, v);
		},
		/**
		 * @param {string} key
		 * @returns {Promise<void>}
		 */
		async remove(key) {
			store.delete(key);
		}
	};
}

/**
 * @param {{ platform?: 'chrome' | 'firefox' }} [options]
 */
export function createBrowserStub(options = {}) {
	const platform = options.platform ?? 'chrome';
	const origin =
		platform === 'firefox'
			? 'moz-extension://lgomtestextensionid'
			: 'chrome-extension://lgomtestextensionid';

	/** @type {Map<string, unknown>} */
	const local = new Map();
	/** @type {Map<string, unknown>} */
	const session = new Map();

	const calls = {
		/** @type {unknown[]} */
		cancelled: [],
		/** @type {unknown[]} */
		erased: [],
		/** @type {unknown[]} */
		tabsCreated: [],
		/** @type {unknown[]} */
		tabsUpdated: [],
		/** @type {unknown[]} */
		tabsRemoved: [],
		/** @type {unknown[]} */
		tabsQueried: []
	};

	/** @type {Array<(item: any) => Promise<void> | void>} */
	const downloadListeners = [];

	const api = {
		storage: {
			local: makeStorage(local),
			session: makeStorage(session)
		},
		runtime: {
			/** @param {string} path */
			getURL: (path) => `${origin}/${path}`
		},
		i18n: {
			/** @param {string} key */
			getMessage: (key) => key
		},
		downloads: {
			onCreated: {
				/** @param {(item: any) => Promise<void> | void} fn */
				addListener: (fn) => {
					downloadListeners.push(fn);
				}
			},
			/** @param {unknown} id */
			cancel: async (id) => {
				calls.cancelled.push(id);
			},
			/** @param {unknown} query */
			erase: async (query) => {
				calls.erased.push(query);
			}
		},
		webRequest: {
			onBeforeSendHeaders: {
				addListener: () => {}
			}
		},
		tabs: {
			onUpdated: {
				addListener: () => {},
				removeListener: () => {}
			},
			/** @param {unknown} query */
			query: async (query) => {
				calls.tabsQueried.push(query);
				return [];
			},
			/** @param {{ url?: string, active?: boolean }} props */
			create: async (props) => {
				calls.tabsCreated.push(props);
				return { id: 4242, status: 'complete' };
			},
			/** @param {number} id */
			get: async (id) => ({ id, status: 'complete' }),
			/** @param {number} id @param {{ url?: string, active?: boolean }} props */
			update: async (id, props) => {
				calls.tabsUpdated.push({ id, ...props });
				return { id };
			},
			/** @param {number} id */
			remove: async (id) => {
				calls.tabsRemoved.push(id);
			}
		}
	};

	return {
		api,
		calls,
		/** @type {Map<string, unknown>} */
		local,
		/** @type {Map<string, unknown>} */
		session,
		/**
		 * 触发一次浏览器下载事件，等扩展处理完。
		 * @param {{ id: number, url: string, filename?: string }} item
		 */
		async emitDownload(item) {
			for (const listener of downloadListeners) await listener(item);
		},
		/** 清空存储与调用记录，供下一个用例复用。 */
		reset() {
			local.clear();
			session.clear();
			calls.cancelled.length = 0;
			calls.erased.length = 0;
			calls.tabsCreated.length = 0;
			calls.tabsUpdated.length = 0;
			calls.tabsRemoved.length = 0;
			calls.tabsQueried.length = 0;
		}
	};
}

/**
 * 装上 fetch 桩。扩展的端口通道完全走 fetch，所以这一层能同时覆盖
 * 「客户端在跑」和「客户端没在跑」两种情形，而且不碰真实端口。
 *
 * @param {(url: string, init: RequestInit) => Promise<Response>} handler
 * @returns {() => void} 还原函数
 */
export function installFetchStub(handler) {
	const original = globalThis.fetch;
	globalThis.fetch = /** @type {typeof fetch} */ (
		async (input, init) => handler(String(input), init ?? {})
	);
	return () => {
		globalThis.fetch = original;
	};
}

/**
 * 一个「客户端在跑」的 fetch 桩：/ping 回 app id，/download 回 ok。
 * 记录收到的 URL 与请求头，供断言使用。
 *
 * @returns {{ handler: (url: string, init: RequestInit) => Promise<Response>, requests: {url: string, headers: Record<string, string>}[], restore: () => void }}
 */
export function installLiveClientStub() {
	/** @type {{url: string, headers: Record<string, string>}[]} */
	const requests = [];

	const handler = async (url, init) => {
		const headers = {};
		const raw = init?.headers;
		if (raw) {
			for (const [k, v] of new Headers(raw).entries()) headers[k.toLowerCase()] = v;
		}
		requests.push({ url, headers });

		if (url.endsWith('/ping')) {
			return new Response(
				JSON.stringify({
					app: 'lgo_download_manager',
					api: 1,
					port: 17821,
					pid: 1,
					version: 'test'
				}),
				{ status: 200, headers: { 'Content-Type': 'application/json' } }
			);
		}
		return new Response(JSON.stringify({ ok: true }), {
			status: 200,
			headers: { 'Content-Type': 'application/json' }
		});
	};

	const restore = installFetchStub(handler);
	return { handler, requests, restore };
}

/**
 * 一个「客户端没在跑」的 fetch 桩：所有请求都失败。
 *
 * @returns {{ requests: string[], restore: () => void }}
 */
export function installDeadClientStub() {
	/** @type {string[]} */
	const requests = [];
	const handler = async (url) => {
		requests.push(url);
		throw new TypeError('fetch failed');
	};
	const restore = installFetchStub(handler);
	return { requests, restore };
}
