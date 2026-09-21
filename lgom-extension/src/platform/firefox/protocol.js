import { PROTOCOL_TAB_TIMEOUT } from '$lib/constants.js';
import { browser } from '$lib/platform-api.js';

/**
 * Firefox blocks top-level navigation to unknown external protocols from an
 * extension tab in some configurations, so a hidden iframe is injected as a
 * fallback after the tab attempt.
 *
 * @param {string} url
 * @returns {Promise<void>}
 */
export async function triggerProtocol(url) {
	const tab = await browser.tabs.create({ url, active: false }).catch(() => undefined);

	try {
		await fallbackIframeTrigger(url);
	} catch {
		// The tab navigation above already covers the common case.
	}

	setTimeout(() => {
		if (tab?.id !== undefined) browser.tabs.remove(tab.id).catch(() => {});
	}, PROTOCOL_TAB_TIMEOUT);
}

/**
 * Inject a hidden iframe pointing at the protocol URL into the active tab.
 *
 * @param {string} url
 * @returns {Promise<void>}
 */
async function fallbackIframeTrigger(url) {
	const [tab] = await browser.tabs.query({ active: true, currentWindow: true });
	if (tab?.id === undefined) return;

	await browser.scripting.executeScript({
		target: { tabId: tab.id },
		func: (/** @param {string} protocolUrl */ protocolUrl) => {
			const frame = document.createElement('iframe');
			frame.src = protocolUrl;
			frame.style.display = 'none';
			document.body.appendChild(frame);
			setTimeout(() => frame.remove(), 1000);
		},
		args: [url]
	});
}
