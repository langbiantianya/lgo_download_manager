import { sendToNativeHost } from './native-host.js';
import { handOffViaTab } from './handoff-tab.js';

/**
 * Hand the generated URL to the OS.
 *
 * Preferred channel is the native messaging host: `runtime.sendNativeMessage`
 * spawns the locally installed bridge, which starts (or forwards to) the LGOM
 * client. Chrome shows no confirmation for it, and no tab is involved.
 *
 * The host is optional — a machine that never ran `native-host/install.sh`
 * still has to work — so a failed attempt falls through to the external
 * protocol channel (`lgom://`), which the browser gates behind its own
 * "Open LGOM?" dialog. Missing hosts are the normal case for un-bridged
 * installs and stay silent; a registered-but-unusable host is reported, since
 * that is an install error the user can fix.
 *
 * @param {string} url
 * @returns {Promise<import('$lib/types.js').HandoffChannel>}
 */
export async function triggerProtocol(url) {
	const native = await sendToNativeHost(url);
	if (native.ok) return 'native';

	if (native.kind !== 'missing') {
		console.warn(`native hand-off failed (${native.kind}): ${native.error}; using lgom:// tab`);
	}

	await handOffViaTab(url);
	return 'tab';
}
