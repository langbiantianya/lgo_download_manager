/**
 * Channel bookkeeping shared by the interceptors, the popup and the options
 * page: the recent-log `reason` strings and the popup badges both derive from
 * the same {@link import('./types.js').HandoffChannel} value.
 */

/** @type {Record<import('./types.js').HandoffChannel, string>} */
const REASONS = {
	port: 'forwarded-port',
	tab: 'forwarded-tab'
};

/**
 * Recent-log reason for a hand-off channel.
 *
 * @param {import('./types.js').HandoffChannel} channel
 * @returns {string}
 */
export function handoffReason(channel) {
	return REASONS[channel] ?? 'forwarded';
}

/**
 * i18n message key for the channel badge shown in the popup's recent list, or
 * `null` for entries that are not hand-offs (`skipped`, `failed: …`).
 *
 * `forwarded-native` and its older name `forwarded` only appear in entries
 * written before the native messaging host was dropped; they are still labelled
 * so an old log does not turn into a blank badge.
 *
 * @param {string} reason  A recent-log reason.
 * @returns {string | null}
 */
export function handoffChannelLabel(reason) {
	switch (reason) {
		case 'forwarded-port':
			return 'channelPort';
		case 'forwarded-native':
		case 'forwarded': // legacy: the native messaging host
			return 'channelNative';
		case 'forwarded-tab':
			return 'channelTab';
		default:
			return null;
	}
}
