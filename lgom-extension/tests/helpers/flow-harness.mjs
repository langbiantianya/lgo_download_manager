// 端到端跑一次「浏览器下载 → 扩展拦截 → 交接」的流程。
//
// 扩展的平台层（src/platform/<target>/background.js）在 import 时就注册
// downloads.onCreated 监听，所以 import 一次即可；之后的每个用例只要重置
// 桩里的存储与调用记录，再触发一次下载事件。
import { register } from 'node:module';

import { createBrowserStub } from './browser-stub.mjs';

// 平台层用 $lib/… 引用 src/lib/，那是 Vite 的别名，Node 得自己接上。
register('./alias-loader.mjs', import.meta.url);

/**
 * 每个平台一份桩。
 *
 * 扩展的后台模块只 import 一次，downloads.onCreated 的监听器就注册在
 * 第一次 import 时用的那个桩上——所以同一个平台的用例必须共用同一个桩，
 * 否则监听器写的是旧桩、断言看的是新桩。
 *
 * @type {Map<string, ReturnType<typeof createBrowserStub>>}
 */
const stubs = new Map();

/**
 * @param {'chrome' | 'firefox'} platform
 */
export async function createFlow(platform) {
	const existing = stubs.get(platform);
	if (existing) return existing;

	const stub = createBrowserStub({ platform });

	// platform-api.js 取 globalThis.browser || globalThis.chrome：
	// Chrome 只有 chrome，Firefox 只有 browser（两边分开装，免得取到对方的桩）。
	if (platform === 'firefox') {
		globalThis.browser = stub.api;
	} else {
		globalThis.chrome = stub.api;
	}

	await import(new URL(`../../src/platform/${platform}/background.js`, import.meta.url).href);
	stubs.set(platform, stub);
	return stub;
}

/**
 * 触发一次下载，等扩展处理完，返回最近拦截日志。
 *
 * @param {ReturnType<typeof createBrowserStub>} stub
 * @param {{ url: string, filename?: string, id?: number, config?: object }} download
 * @returns {Promise<{ recent: any[], calls: any }>}
 */
export async function runDownload(stub, download) {
	stub.reset();
	if (download.config) stub.local.set('lgom_config', download.config);
	await stub.emitDownload({
		id: download.id ?? 7,
		url: download.url,
		filename: download.filename
	});
	return {
		recent: stub.local.get('lgom_recent') ?? [],
		calls: stub.calls
	};
}
