// Shared constants for the LGOM interception extension.

/** Protocol scheme handled by the LGOM desktop client. */
export const LGOM_SCHEME = 'lgom';

/** Host part of the download route. */
export const LGOM_HOST = 'download';

/** Extension page kept open as the Chrome fallback hand-off tab. */
export const HANDOFF_PAGE = 'handoff.html';

/**
 * Loopback host of the desktop client's local-port channel.
 *
 * `127.0.0.1` on purpose (not `localhost`): the client binds IPv4 loopback
 * only, and this skips any DNS resolution in the hot path of every download.
 */
export const LOCAL_PORT_HOST = '127.0.0.1';

/**
 * Candidate ports for the local-port channel.
 *
 * Must stay in sync with `handoff.PortCandidates` on the Go side
 * (`internal/handoff/handoff.go`): the desktop client binds the first free
 * candidate and the extension probes this same list to discover it.
 */
export const LOCAL_PORT_CANDIDATES = [17821, 17822, 17823, 17824, 17825];

/**
 * Header every port-channel request must carry.
 *
 * A custom header turns the request into a non-simple one, so a web page would
 * have to pass a CORS preflight first — and the desktop client only answers
 * preflights for extension origins. A constant value is enough; presence is
 * what the client checks.
 */
export const LOCAL_PORT_CLIENT_HEADER = 'X-LGOM-Client';

/** Value sent with {@link LOCAL_PORT_CLIENT_HEADER}. */
export const LOCAL_PORT_CLIENT_VALUE = 'extension';

/**
 * App identifier reported by the client's `GET /ping`.
 *
 * Also validates the *scheme* of the response: `chrome-extension://` (and
 * Firefox's `moz-extension://`) pages are secure origins, so a page cannot
 * fake this reply for a port that is not LGOM.
 */
export const LOCAL_PORT_APP_ID = 'lgo_download_manager';

/** Port-channel API version the extension speaks. */
export const LOCAL_PORT_API_VERSION = 1;

/** storage.session key caching the discovered port. */
export const LOCAL_PORT_CACHE_KEY = 'lgom_local_port';

/** Per-candidate ping timeout (ms). Candidates are probed in parallel, so this
 * is also the worst-case discovery time when the client is not running. */
export const LOCAL_PORT_PING_TIMEOUT = 400;

/** Timeout for one hand-off request (ms). */
export const LOCAL_PORT_REQUEST_TIMEOUT = 5000;

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

/** How long the Chrome fallback waits for the hand-off page to load (ms).
 * Chrome discards a tab that has nothing committed when its only pending
 * navigation turns into an external protocol launch, so the first hand-off
 * must not navigate before the page has committed. */
export const HANDOFF_TAB_LOAD_TIMEOUT = 3000;

/** URL size ceiling accepted by the LGOM IPC forwarder. */
export const MAX_FRAME_LEN = 64 * 1024;
