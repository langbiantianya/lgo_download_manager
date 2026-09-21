import { browser } from './platform-api.js';

const KEY = 'lgom_recent';
const LIMIT = 50;

/**
 * Append an entry to the recent-interception log (newest first).
 *
 * @param {import('./types.js').RecentDownload} entry
 * @returns {Promise<void>}
 */
export async function appendRecent(entry) {
	const stored = await browser.storage.local.get(KEY);
	const list = Array.isArray(stored[KEY]) ? stored[KEY] : [];
	list.unshift(entry);
	await browser.storage.local.set({ [KEY]: list.slice(0, LIMIT) });
}

/**
 * @returns {Promise<import('./types.js').RecentDownload[]>}
 */
export async function getRecent() {
	const stored = await browser.storage.local.get(KEY);
	return Array.isArray(stored[KEY]) ? stored[KEY] : [];
}

/**
 * @returns {Promise<void>}
 */
export async function clearRecent() {
	await browser.storage.local.remove(KEY);
}
