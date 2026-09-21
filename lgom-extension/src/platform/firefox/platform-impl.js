import { browser, getPlatformName } from '$lib/platform-api.js';
import { triggerProtocol } from './protocol.js';

/** @type {import('$lib/types.js').PlatformBridge} */
export const platformBridge = {
	getPlatformName,
	triggerProtocol,
	async getActiveTabUrl() {
		const [tab] = await browser.tabs.query({ active: true, currentWindow: true });
		return tab?.url ?? '';
	}
};
