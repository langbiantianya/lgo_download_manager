import { handOffViaTab } from './handoff-tab.js';
import { sendToLocalPort } from '$lib/local-port.js';
import { buildPortPayload } from '$lib/url-builder.js';

/**
 * Hand a download to the desktop client, quietest channel first.
 *
 * 1. **Local port** — a loopback HTTP request to the already running client.
 *    Nothing to install, no tab, no confirmation, and the metadata travels as
 *    a JSON body instead of a navigable URL. Only works while the client runs.
 * 2. **External protocol (`lgom://`)** — last resort. Chrome gates it behind
 *    its own "Open LGOM?" dialog, but it is the only channel that works while
 *    the client is not running, since it is what cold-starts it.
 *
 * A client that is not running is the normal case for the port channel, so
 * that stays silent; anything else is reported because it means a broken
 * install the user can fix.
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

	await handOffViaTab(url);
	return 'tab';
}
