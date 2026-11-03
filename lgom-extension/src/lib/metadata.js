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
	const name = pickFilename(filename, url, headers);

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
 * @param {Record<string, string | string[] | undefined>} headers
 * @returns {string}
 */
function pickFilename(filename, url, headers) {
	const suggested = sanitizeFilename(filename);
	const fromDisposition = parseContentDisposition(headerValue(headers, 'Content-Disposition'));
	if (suggested && (!isWeakFilename(suggested) || !fromDisposition)) return suggested;
	if (fromDisposition) return fromDisposition;

	try {
		const parsed = new URL(url);
		const query = parsed.searchParams;
		const queryDisposition = parseContentDisposition(
			query.get('response-content-disposition') ?? ''
		);
		if (queryDisposition) return queryDisposition;
		for (const key of FILENAME_QUERY_KEYS) {
			const value = sanitizeFilename(queryValue(query, key));
			if (value) return value;
		}
		const fromPath = sanitizeFilename(decodeURISafe(parsed.pathname));
		if (fromPath) return fromPath;
	} catch {
		const fallback = sanitizeFilename(decodeURISafe(url.split(/[?#]/, 1)[0]));
		if (fallback) return fallback;
	}
	return UNKNOWN_FILENAME;
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

const FILENAME_QUERY_KEYS = [
	'filename',
	'file',
	'name',
	'dn',
	'download',
	'fn',
	'fname',
	'output',
	'target'
];
const WEAK_FILENAMES = new Set(['download', 'file', 'unknown']);
const WINDOWS_RESERVED_NAME = /^(con|prn|aux|nul|com[1-9]|lpt[1-9])(?:\.|$)/i;

/**
 * @param {string | undefined | null} raw
 * @returns {string}
 */
function sanitizeFilename(raw) {
	if (!raw) return '';
	let name = decodeURISafe(String(raw)).trim();
	name = name.split(/[/\\]/).pop() ?? '';
	name = [...name].filter((char) => char.charCodeAt(0) >= 0x20).join('');
	name = name
		.replace(/[<>:"/\\|?*]/g, '_')
		.replace(/[\r\n\t]+/g, ' ')
		.replace(/^ +|[ .]+$/g, '');
	if (!name || name === '.' || name === '..') return '';
	if (WINDOWS_RESERVED_NAME.test(name)) name = `_${name}`;
	return [...name]
		.slice(0, 180)
		.join('')
		.replace(/[ .]+$/g, '');
}

/**
 * @param {string} name
 * @returns {boolean}
 */
function isWeakFilename(name) {
	const stem = name.replace(/\.[^.]+$/, '').toLowerCase();
	return WEAK_FILENAMES.has(stem);
}

/**
 * @param {Record<string, string | string[] | undefined>} headers
 * @param {string} wanted
 * @returns {string}
 */
function headerValue(headers, wanted) {
	for (const [key, value] of Object.entries(headers)) {
		if (key.toLowerCase() !== wanted.toLowerCase()) continue;
		if (Array.isArray(value)) return value.find((item) => item) ?? '';
		return value ?? '';
	}
	return '';
}

/**
 * Parse RFC 6266 `filename` / `filename*` without requiring the whole header
 * to be well-formed; servers commonly omit a closing quote or add unknown
 * parameters.
 *
 * @param {string} raw
 * @returns {string}
 */
function parseContentDisposition(raw) {
	if (!raw) return '';
	const params = new Map();
	for (const part of splitHeaderParams(raw).slice(1)) {
		const eq = part.indexOf('=');
		if (eq < 0) continue;
		const key = part.slice(0, eq).trim().toLowerCase();
		let value = part.slice(eq + 1).trim();
		if (value.startsWith('"')) value = value.slice(1);
		if (value.endsWith('"')) value = value.slice(0, -1);
		value = value.replace(/\\"/g, '"').replace(/\\\\/g, '\\');
		params.set(key, value);
	}
	for (const key of ['filename*', 'filename']) {
		const value = params.get(key);
		if (!value) continue;
		const encoded = value.replace(/^[^']*'[^']*'/, '');
		const name = sanitizeFilename(encoded);
		if (name) return name;
	}
	return '';
}

/**
 * @param {URLSearchParams} query
 * @param {string} wanted
 * @returns {string}
 */
function queryValue(query, wanted) {
	for (const [key, value] of query) {
		if (key.toLowerCase() === wanted) return value;
	}
	return '';
}

/**
 * Split semicolon-delimited disposition parameters while respecting quotes.
 *
 * @param {string} raw
 * @returns {string[]}
 */
function splitHeaderParams(raw) {
	const parts = [];
	let start = 0;
	let quoted = false;
	let escaped = false;
	for (let i = 0; i < raw.length; i += 1) {
		const char = raw[i];
		if (escaped) {
			escaped = false;
		} else if (char === '\\' && quoted) {
			escaped = true;
		} else if (char === '"') {
			quoted = !quoted;
		} else if (char === ';' && !quoted) {
			parts.push(raw.slice(start, i));
			start = i + 1;
		}
	}
	parts.push(raw.slice(start));
	return parts;
}
