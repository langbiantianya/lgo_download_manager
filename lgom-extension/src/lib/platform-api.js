// Platform bridge. Both Chrome and Firefox expose a `browser`/`chrome`
// extension API surface; the promise-first `browser` object is preferred and
// Chrome falls back to its callback-based `chrome` object. No polyfill needed.

/** @type {typeof globalThis.chrome & typeof globalThis.browser} */
export const browser = globalThis.browser || globalThis.chrome;

/**
 * @param {string} key  Message key from _locales/<lang>/messages.json.
 * @param {string[]} [subs]  Substitution strings.
 * @returns {string} Localized message, or the key itself if unavailable.
 */
export function i18nGetMessage(key, subs) {
	try {
		return browser.i18n.getMessage(key, subs) ?? key;
	} catch {
		return key;
	}
}

/**
 * @returns {string} 'chrome' | 'firefox' | 'unknown'
 */
export function getPlatformName() {
	if (
		globalThis.browser &&
		globalThis.browser.runtime &&
		globalThis.browser.runtime.getBrowserInfo
	) {
		return 'firefox';
	}
	if (globalThis.chrome) {
		return 'chrome';
	}
	return 'unknown';
}
