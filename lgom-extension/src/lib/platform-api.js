// Platform bridge. Both Chrome and Firefox expose a `browser`/`chrome`
// extension API surface; the promise-first `browser` object is preferred and
// Chrome falls back to its callback-based `chrome` object. No polyfill needed.

/** @type {typeof globalThis.chrome & typeof globalThis.browser} */
export const browser = globalThis.browser || globalThis.chrome;

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
