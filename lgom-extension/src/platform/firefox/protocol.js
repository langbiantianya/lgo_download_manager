import { PROTOCOL_TAB_TIMEOUT } from '$lib/constants.js';
import { browser } from '$lib/platform-api.js';
import { sendToLocalPort } from '$lib/local-port.js';
import { buildPortPayload } from '$lib/url-builder.js';

/**
 * Hand a download to the desktop client.
 *
 * 1. **Local port** — a loopback HTTP request to the already running client,
 *    silent and tab-free. Requires the client to be running.
 * 2. **External protocol (`lgom://`)** — last resort, and the only channel that
 *    can cold-start the client. Firefox turns the top-level navigation of an
 *    extension tab to an unknown scheme into an external protocol launch: the
 *    OS handler runs, or Firefox raises its own browser-wide "launch
 *    application?" prompt that does not belong to the tab. The tab has served
 *    its purpose once the navigation has been handed off, so it is removed
 *    again.
 *
 * @param {import('$lib/types.js').DownloadMetadata} meta
 * @param {string} url
 * @returns {Promise<import('$lib/types.js').HandoffChannel>}
 */
export async function triggerProtocol(meta, url) {
	const port = await sendToLocalPort(buildPortPayload(meta));
	if (port.ok) return 'port';
	if (port.kind !== 'unreachable') {
		console.warn(`local port hand-off failed (${port.error}); using the lgom:// tab`);
	}

	const tab = await browser.tabs.create({ url, active: false });
	if (tab.id === undefined) return 'tab';

	const tabId = tab.id;
	setTimeout(() => {
		browser.tabs.remove(tabId).catch(() => {});
	}, PROTOCOL_TAB_TIMEOUT);

	return 'tab';
}
