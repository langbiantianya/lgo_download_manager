import {
	DOWNLOAD_HINT,
	HEADER_CACHE_KEY,
	HEADER_CACHE_LIMIT,
	HEADER_CACHE_TTL
} from '$lib/constants.js';
import { browser } from '$lib/platform-api.js';

/** @type {Map<string, { at: number, headers: Record<string, string | string[]> }>} */
const memory = new Map();

export function registerHeaderListener() {
	browser.webRequest.onBeforeSendHeaders.addListener(
		onBeforeSendHeaders,
		{ urls: ['<all_urls>'] },
		['requestHeaders']
	);
}

/**
 * @param {import('$lib/types.js').HeaderEvent} details
 */
function onBeforeSendHeaders(details) {
	if (!DOWNLOAD_HINT.test(details.url)) return;

	const now = Date.now();
	memory.set(details.url, { at: now, headers: toHeaderRecord(details.requestHeaders) });
	if (memory.size > HEADER_CACHE_LIMIT) {
		pruneMemory(now);
	}
	persist();
}

/**
 * @param {import('$lib/types.js').HeaderEvent['requestHeaders']} headers
 * @returns {Record<string, string | string[]>}
 */
function toHeaderRecord(headers) {
	/** @type {Record<string, string | string[]>} */
	const record = {};
	for (const header of headers ?? []) {
		const previous = record[header.name];
		if (previous === undefined) record[header.name] = header.value ?? '';
		else if (Array.isArray(previous)) previous.push(header.value ?? '');
		else record[header.name] = [previous, header.value ?? ''];
	}
	return record;
}

/**
 * Firefox background scripts persist for the browser session, so the cache can
 * live in memory; storage.session is still written so a restarted background
 * keeps the tail.
 *
 * @param {string} url
 * @returns {Promise<Record<string, string | string[]> | undefined>}
 */
export async function getCachedHeaders(url) {
	const hit = memory.get(url);
	if (hit && Date.now() - hit.at < HEADER_CACHE_TTL) return hit.headers;

	const stored = await browser.storage.session.get(HEADER_CACHE_KEY);
	/** @type {unknown} */
	const raw = stored[HEADER_CACHE_KEY];
	/** @type {Record<string, { at: number, headers: Record<string, string | string[]> }>} */
	const snapshot =
		/** @type {Record<string, { at: number, headers: Record<string, string | string[]> }>} */ (
			raw ?? {}
		);
	const entry = snapshot[url];
	if (entry && Date.now() - entry.at < HEADER_CACHE_TTL) {
		memory.set(url, entry);
		return entry.headers;
	}
	return undefined;
}

/**
 * @param {string} url
 * @returns {Promise<void>}
 */
export async function warmHeaderCache(url) {
	await getCachedHeaders(url);
}

/** @param {number} now */
function pruneMemory(now) {
	for (const [url, entry] of memory) {
		if (now - entry.at >= HEADER_CACHE_TTL) memory.delete(url);
	}
}

/** Debounced snapshot write. */
let flushQueued = false;
function persist() {
	if (flushQueued) return;
	flushQueued = true;
	setTimeout(async () => {
		flushQueued = false;
		const now = Date.now();
		pruneMemory(now);
		const snapshot = Object.fromEntries(memory);
		await browser.storage.session.set({ [HEADER_CACHE_KEY]: snapshot });
	}, 250);
}
