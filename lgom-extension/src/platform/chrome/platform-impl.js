import { getPlatformName } from '$lib/platform-api.js';
import { triggerProtocol } from './protocol.js';

/** @type {import('$lib/types.js').PlatformBridge} */
export const platformBridge = {
	getPlatformName,
	triggerProtocol,
	async getActiveTabUrl() {
		const [tab] = await globalThis.chrome.tabs.query({ active: true, currentWindow: true });
		return tab?.url ?? '';
	}
};
