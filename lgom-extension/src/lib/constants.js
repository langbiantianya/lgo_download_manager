// Shared constants for the LGOM interception extension.

/** Protocol scheme handled by the LGOM desktop client. */
export const LGOM_SCHEME = 'lgom';

/** Host part of the download route. */
export const LGOM_HOST = 'download';

/** storage.local key holding the persisted InterceptConfig. */
export const CONFIG_STORAGE_KEY = 'lgom_config';

/**
 * Defaults applied to every loaded config. Enabled interception, all URLs,
 * no filter patterns, and a 2000-character budget for the generated URL.
 *
 * @type {import('./types.js').InterceptConfig}
 */
export const DEFAULT_CONFIG = {
	enabled: true,
	mode: 'all',
	patterns: [],
	maxUrlLength: 2000
};

/** Hop-by-hop and host-bound headers that must never be forwarded. */
export const EXCLUDED_HEADERS = new Set([
	'host',
	'connection',
	'content-length',
	'accept-encoding',
	'transfer-encoding',
	'te',
	'trailer',
	'upgrade'
]);

/** Cache entry lifetime (ms). Chrome MV3 service workers get evicted, so the
 * cache is mirrored into storage.session; TTL still applies to both copies. */
export const HEADER_CACHE_TTL = 120_000;

/** Upper bound for the in-memory header cache before pruning kicks in. */
export const HEADER_CACHE_LIMIT = 1000;

/** storage.session key holding the header-cache snapshot. */
export const HEADER_CACHE_KEY = 'lgom_header_cache';

/** Filename fallback when nothing better is known. */
export const UNKNOWN_FILENAME = 'unknown';

/** How long the Firefox hand-off tab stays open before being removed (ms).
 * Firefox's "launch application?" prompt belongs to the browser window, not to
 * the tab, so the tab is disposable once the navigation has been handed off. */
export const PROTOCOL_TAB_TIMEOUT = 1000;

/** URL size ceiling accepted by the LGOM IPC forwarder. */
export const MAX_FRAME_LEN = 64 * 1024;
