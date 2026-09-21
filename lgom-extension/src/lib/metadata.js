import {
	extractCookies,
	extractReferer,
	extractUserAgent,
	serializeForwardHeaders
} from './url-builder.js';
import { UNKNOWN_FILENAME } from './constants.js';

/**
 * Turn a raw download item into forwardable metadata.
 *
 * @param {string} url
 * @param {string | undefined} filename  Suggested name from the browser.
 * @param {Record<string, string | string[] | undefined>} rawHeaders
 * @param {string} fallbackUa
 * @returns {import('./types.js').DownloadMetadata}
 */
export function normalizeMetadata(url, filename, rawHeaders, fallbackUa) {
	const headers = /** @type {Record<string, string | string[] | undefined>} */ (rawHeaders ?? {});
	const name = decodeURISafe(pickFilename(filename, url));

	return {
		url,
		name,
		ua: extractUserAgent(headers) || fallbackUa,
		headers: serializeForwardHeaders(headers),
		cookies: extractCookies(headers),
		referer: extractReferer(headers),
		timestamp: Date.now()
	};
}

/**
 * Decide whether a URL should be intercepted under the active config.
 *
 * - `all`: intercept everything.
 * - `whitelist`: intercept only matches.
 * - `blacklist`: intercept everything except matches.
 *
 * Patterns are regexes; a broken pattern degrades to substring matching.
 *
 * @param {string} url
 * @param {import('./types.js').InterceptConfig} config
 * @returns {boolean}
 */
export function shouldIntercept(url, config) {
	if (config.mode === 'all') return true;

	const matches = config.patterns.some((pattern) => matchesPattern(url, pattern));

	if (config.mode === 'whitelist') return matches;
	return !matches; // blacklist
}

/**
 * @param {string} url
 * @param {string} pattern
 * @returns {boolean}
 */
function matchesPattern(url, pattern) {
	try {
		return new RegExp(pattern).test(url);
	} catch {
		return url.includes(pattern);
	}
}

/**
 * @param {string | undefined} filename
 * @param {string} url
 * @returns {string}
 */
function pickFilename(filename, url) {
	if (filename) return filename.split('/').pop() || filename;
	const fromUrl = url.split('/').pop()?.split('?')[0];
	return fromUrl || UNKNOWN_FILENAME;
}

/**
 * @param {string} raw
 * @returns {string}
 */
function decodeURISafe(raw) {
	try {
		return decodeURIComponent(raw);
	} catch {
		return raw;
	}
}
