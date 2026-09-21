import { EXCLUDED_HEADERS, LGOM_HOST, LGOM_SCHEME } from './constants.js';

/**
 * Serialize headers into a `Name: value` line list suitable for the
 * `headers` query parameter.
 *
 * @param {Record<string, string | string[] | undefined>} headers
 * @returns {string}
 */
function serializeHeaders(headers) {
	const lines = [];
	for (const [name, value] of Object.entries(headers)) {
		if (value === undefined) continue;
		const lower = name.toLowerCase();
		if (EXCLUDED_HEADERS.has(lower)) continue;
		lines.push(`${name}: ${Array.isArray(value) ? value.join(', ') : value}`);
	}
	return lines.join('\n');
}

/**
 * Case-insensitive header lookup. `chrome.webRequest` reports lowercase names;
 * direct object lookups must stay tolerant either way.
 *
 * @param {Record<string, string | string[] | undefined>} headers
 * @param {string} name
 * @returns {string | undefined}
 */
function findHeader(headers, name) {
	const target = name.toLowerCase();
	for (const [key, value] of Object.entries(headers)) {
		if (key.toLowerCase() === target) {
			if (Array.isArray(value)) return value.join('; ');
			return value;
		}
	}
	return undefined;
}

/**
 * @param {Record<string, string | string[] | undefined>} headers
 * @returns {string}
 */
export function extractCookies(headers) {
	return findHeader(headers, 'cookie') ?? '';
}

/**
 * @param {Record<string, string | string[] | undefined>} headers
 * @returns {string}
 */
export function extractUserAgent(headers) {
	return findHeader(headers, 'user-agent') ?? '';
}

/**
 * @param {Record<string, string | string[] | undefined>} headers
 * @returns {string}
 */
export function extractReferer(headers) {
	return findHeader(headers, 'referer') ?? '';
}

/**
 * Build the `lgom://download?...` URL. Degrades to the core params
 * (url/name/ua) whenever the full URL would exceed `maxLength`, keeping the
 * handoff inside the LGOM IPC frame limit.
 *
 * @param {import('./types.js').DownloadMetadata} meta
 * @param {number} [maxLength=2000]
 * @returns {string}
 */
export function buildLgomUrl(meta, maxLength = 2000) {
	/** @type {Record<string, string>} */
	const core = {
		url: meta.url,
		name: meta.name,
		ua: meta.ua
	};

	/** @type {Record<string, string>} */
	const full = {
		...core,
		headers: meta.headers,
		cookies: meta.cookies
	};

	const params = new URLSearchParams(full);
	const url = `${LGOM_SCHEME}://${LGOM_HOST}?${params.toString()}`;
	if (url.length <= maxLength) return url;

	const fallback = new URLSearchParams(core);
	return `${LGOM_SCHEME}://${LGOM_HOST}?${fallback.toString()}`;
}

/**
 * Serialize a header map for transport, dropping hop-by-hop headers.
 *
 * @param {Record<string, string | string[] | undefined>} headers
 * @returns {string}
 */
export function serializeForwardHeaders(headers) {
	return serializeHeaders(headers);
}
