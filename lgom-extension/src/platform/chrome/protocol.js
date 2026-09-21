import { PROTOCOL_TAB_TIMEOUT } from '$lib/constants.js';

/**
 * Hand the generated URL to the OS. Chrome resolves `lgom://` from a normal
 * navigation; opening an inactive tab avoids stealing focus and the tab is
 * removed once the handoff has happened.
 *
 * @param {string} url
 * @returns {Promise<void>}
 */
export async function triggerProtocol(url) {
	const browser = globalThis.chrome;
	const tab = await browser.tabs.create({ url, active: false });
	setTimeout(() => {
		if (tab.id !== undefined) browser.tabs.remove(tab.id).catch(() => {});
	}, PROTOCOL_TAB_TIMEOUT);
}
