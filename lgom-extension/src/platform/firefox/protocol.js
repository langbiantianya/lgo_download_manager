import { PROTOCOL_TAB_TIMEOUT } from '$lib/constants.js';
import { browser } from '$lib/platform-api.js';

/**
 * Hand the generated URL to the OS.
 *
 * Firefox turns the top-level navigation of an extension tab to an unknown
 * scheme into an external protocol launch: the OS handler runs, or Firefox
 * raises its own browser-wide "launch application?" prompt that does not
 * belong to the tab. The tab has served its purpose once the navigation has
 * been handed off, so it is removed again.
 *
 * @param {string} url
 * @returns {Promise<import('$lib/types.js').HandoffChannel>}
 */
export async function triggerProtocol(url) {
	const tab = await browser.tabs.create({ url, active: false });
	if (tab.id === undefined) return 'tab';

	const tabId = tab.id;
	setTimeout(() => {
		browser.tabs.remove(tabId).catch(() => {});
	}, PROTOCOL_TAB_TIMEOUT);

	return 'tab';
}
