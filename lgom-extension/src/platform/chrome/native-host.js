import { NATIVE_HOST_NAME } from '$lib/constants.js';

/**
 * Outcome of a native messaging attempt.
 *
 * - `delivered`: the host accepted the URL and launched the client.
 * - `missing`:   no host is registered for this extension (never installed).
 *                Expected on machines without `native-host/install.sh`; the
 *                caller falls back to the tab hand-off without warning.
 * - `forbidden`: a host is registered but does not list this extension in
 *                `allowed_origins` — an install/ID mismatch worth reporting.
 * - `failed`:    anything else (host crashed, replied with an error, …).
 *
 * @typedef {{ ok: true } | { ok: false, error: string, kind: 'missing' | 'forbidden' | 'failed' }} NativeHandoffResult
 */

/**
 * Hand the URL to the desktop client through the native messaging host.
 *
 * `runtime.sendNativeMessage` starts the host process, writes one
 * length-prefixed JSON message to its stdin and resolves with the reply.
 * Chrome never shows a confirmation dialog for this channel: the host is a
 * locally installed program that the browser trusts because the extension ID
 * is listed in its manifest.
 *
 * @param {string} url
 * @returns {Promise<NativeHandoffResult>}
 */
export async function sendToNativeHost(url) {
	let response;
	try {
		response = await globalThis.chrome.runtime.sendNativeMessage(NATIVE_HOST_NAME, { url });
	} catch (error) {
		return { ok: false, ...classify(error) };
	}

	// The bridge answers `{"ok":true}` once the client has been started; any
	// other payload means it read our message but could not act on it, which
	// is a broken install rather than a missing one.
	if (isOkResponse(response)) return { ok: true };
	return { ok: false, kind: 'failed', error: `host replied ${JSON.stringify(response)}` };
}

/**
 * @param {unknown} response
 * @returns {boolean}
 */
function isOkResponse(response) {
	if (typeof response !== 'object' || response === null) return false;
	return /** @type {{ ok?: unknown }} */ (response).ok === true;
}

/**
 * Chrome reports native messaging failures as `runtime.lastError` messages;
 * the two we care about are stable user-visible strings.
 *
 * @param {unknown} error
 * @returns {{ error: string, kind: 'missing' | 'forbidden' | 'failed' }}
 */
function classify(error) {
	const message = String(/** @type {{message?: string}} */ (error)?.message ?? error);
	if (message.includes('not found')) return { error: message, kind: 'missing' };
	if (message.includes('forbidden')) return { error: message, kind: 'forbidden' };
	return { error: message, kind: 'failed' };
}
