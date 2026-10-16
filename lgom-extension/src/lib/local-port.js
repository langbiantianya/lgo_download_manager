import {
	LOCAL_PORT_APP_ID,
	LOCAL_PORT_CACHE_KEY,
	LOCAL_PORT_CANDIDATES,
	LOCAL_PORT_CLIENT_HEADER,
	LOCAL_PORT_CLIENT_VALUE,
	LOCAL_PORT_HOST,
	LOCAL_PORT_PING_TIMEOUT,
	LOCAL_PORT_REQUEST_TIMEOUT
} from './constants.js';
import { browser } from './platform-api.js';

/**
 * Outcome of a local-port hand-off attempt.
 *
 * - `unreachable`: nothing answered on any candidate port, i.e. the desktop
 *   client is not running (or the port channel is blocked). The caller falls
 *   through to the next channel.
 * - `failed`: the client answered but refused the request (bad origin, wrong
 *   payload, no UI to show the dialog). Worth reporting.
 *
 * @typedef {{ ok: true, port: number } | { ok: false, kind: 'unreachable' | 'failed', error: string }} LocalPortResult
 */

/** Port discovered in this worker's lifetime (fast path before storage). */
/** @type {number | null} */
let memoryPort = null;

/**
 * Probe the desktop client's local-port channel.
 *
 * Returns the port the client is listening on, or `null` when no candidate
 * answers. The last known port is tried first so the common case costs one
 * request instead of one per candidate.
 *
 * @returns {Promise<number | null>}
 */
export async function probeLocalPort() {
	const known = await getKnownPort();
	if (known !== null && (await ping(known))) return known;
	if (known !== null) await forgetPort();

	const found = await findPort();
	if (found !== null) await rememberPort(found);
	return found;
}

/**
 * Hand a download to the desktop client over the local-port channel.
 *
 * The payload travels as a JSON body instead of an `lgom://` query string, so
 * it is not size-capped by the URL budget and the Cookie/User-Agent headers
 * never end up percent-encoded in something that can be navigated to.
 *
 * @param {import('./types.js').PortHandoffPayload} payload
 * @returns {Promise<LocalPortResult>}
 */
export async function sendToLocalPort(payload) {
	const port = await probeLocalPort();
	if (port === null) {
		return { ok: false, kind: 'unreachable', error: 'no local port answered' };
	}

	let response;
	try {
		response = await request(
			downloadUrl(port),
			{
				method: 'POST',
				headers: {
					'Content-Type': 'application/json',
					[LOCAL_PORT_CLIENT_HEADER]: LOCAL_PORT_CLIENT_VALUE
				},
				body: JSON.stringify(payload)
			},
			LOCAL_PORT_REQUEST_TIMEOUT
		);
	} catch (error) {
		// The client may have exited between the ping and the POST.
		await forgetPort();
		return { ok: false, kind: 'unreachable', error: describe(error) };
	}

	const body = await readJson(response);
	if (response.ok && body?.ok === true) return { ok: true, port };

	// The client answered but did not take the hand-off (forbidden origin,
	// malformed payload, no UI to open the dialog). Not a transport problem, so
	// the cached port stays valid for the next download.
	if (response.status >= 500) await forgetPort();
	return {
		ok: false,
		kind: 'failed',
		error: body?.error ?? `http ${response.status}`
	};
}

/**
 * @param {number} port
 * @returns {Promise<boolean>}
 */
async function ping(port) {
	try {
		const response = await request(
			`${origin(port)}/ping`,
			{ method: 'GET' },
			LOCAL_PORT_PING_TIMEOUT
		);
		if (!response.ok) return false;
		const info = await readJson(response);
		return info?.app === LOCAL_PORT_APP_ID;
	} catch {
		return false;
	}
}

/**
 * Probe every candidate in parallel, preferring the lowest port that answers —
 * the client binds the first free candidate, so that is the deterministic one.
 *
 * @returns {Promise<number | null>}
 */
async function findPort() {
	const answers = await Promise.all(
		LOCAL_PORT_CANDIDATES.map(async (port) => ((await ping(port)) ? port : null))
	);
	const found = answers.filter(/** @param {number | null} p */ (p) => p !== null);
	return found.length > 0 ? Math.min(...found) : null;
}

/**
 * @returns {Promise<number | null>}
 */
async function getKnownPort() {
	if (memoryPort !== null) return memoryPort;
	try {
		const stored = await browser.storage.session.get(LOCAL_PORT_CACHE_KEY);
		const port = stored[LOCAL_PORT_CACHE_KEY];
		memoryPort = typeof port === 'number' ? port : null;
	} catch {
		memoryPort = null;
	}
	return memoryPort;
}

/**
 * @param {number} port
 * @returns {Promise<void>}
 */
async function rememberPort(port) {
	memoryPort = port;
	try {
		await browser.storage.session.set({ [LOCAL_PORT_CACHE_KEY]: port });
	} catch {
		// A worker that cannot persist the port still works: discovery just
		// repeats on the next wake-up.
	}
}

/**
 * @returns {Promise<void>}
 */
async function forgetPort() {
	memoryPort = null;
	try {
		await browser.storage.session.remove(LOCAL_PORT_CACHE_KEY);
	} catch {
		// Same as rememberPort: persistence is an optimisation only.
	}
}

/**
 * @param {number} port
 * @returns {string}
 */
function origin(port) {
	return `http://${LOCAL_PORT_HOST}:${port}`;
}

/**
 * @param {number} port
 * @returns {string}
 */
function downloadUrl(port) {
	return `${origin(port)}/download`;
}

/**
 * `fetch` with a hard deadline. `AbortSignal.timeout` is not used because the
 * minimum Chrome version (102) predates it.
 *
 * @param {string} url
 * @param {RequestInit} init
 * @param {number} timeout
 * @returns {Promise<Response>}
 */
async function request(url, init, timeout) {
	const controller = new AbortController();
	const timer = setTimeout(() => controller.abort(), timeout);
	try {
		return await fetch(url, { ...init, signal: controller.signal, cache: 'no-store' });
	} finally {
		clearTimeout(timer);
	}
}

/**
 * @param {Response} response
 * @returns {Promise<any>}
 */
async function readJson(response) {
	try {
		return await response.json();
	} catch {
		return null;
	}
}

/**
 * @param {unknown} error
 * @returns {string}
 */
function describe(error) {
	return String(/** @type {{ message?: string }} */ (error)?.message ?? error);
}
