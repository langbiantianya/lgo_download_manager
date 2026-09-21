import { CONFIG_STORAGE_KEY, DEFAULT_CONFIG } from './constants.js';
import { browser } from './platform-api.js';

/**
 * Load the intercept config, merged over defaults.
 *
 * @returns {Promise<import('./types.js').InterceptConfig>}
 */
export async function getConfig() {
	const stored = await browser.storage.local.get(CONFIG_STORAGE_KEY);
	/** @type {unknown} */
	const raw = stored[CONFIG_STORAGE_KEY];
	return {
		...DEFAULT_CONFIG,
		.../** @type {Partial<import('./types.js').InterceptConfig>} */ (raw ?? {})
	};
}

/**
 * Persist a partial config update.
 *
 * @param {Partial<import('./types.js').InterceptConfig>} partial
 * @returns {Promise<void>}
 */
export async function setConfig(partial) {
	const current = await getConfig();
	await browser.storage.local.set({ [CONFIG_STORAGE_KEY]: { ...current, ...partial } });
}
