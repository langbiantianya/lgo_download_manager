import { registerHeaderListener, getCachedHeaders, warmHeaderCache } from './headers.js';
import { triggerProtocol } from './protocol.js';
import { buildLgomUrl } from '$lib/url-builder.js';
import { normalizeMetadata, shouldIntercept } from '$lib/metadata.js';
import { getConfig } from '$lib/storage.js';
import { appendRecent } from '$lib/recent.js';
import { browser } from '$lib/platform-api.js';
import { handoffReason } from '$lib/channels.js';
import { getDownloadSessionStartedAt, isRestoredDownload } from '$lib/download-session.js';

registerHeaderListener();

/** Default UA when the request carried none. */
const FALLBACK_UA =
	'Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:120.0) Gecko/20100101 Firefox/120.0';

/**
 * Firefox also exposes `onCreated`; the download is cancelled and erased
 * before being handed to LGOM.
 */
browser.downloads.onCreated.addListener(async (downloadItem) => {
	const sessionStartedAt = await getDownloadSessionStartedAt();
	if (isRestoredDownload(downloadItem, sessionStartedAt)) return;

	const config = await getConfig();
	if (!config.enabled) return;

	if (!shouldIntercept(downloadItem.url, config)) {
		await appendRecent({
			url: downloadItem.url,
			name: downloadItem.filename,
			ok: false,
			reason: 'skipped',
			timestamp: Date.now()
		});
		return;
	}

	await browser.downloads.cancel(downloadItem.id).catch(() => {});
	await browser.downloads.erase({ id: downloadItem.id }).catch(() => {});

	// The hand-off can fail (no protocol handler registered, tab creation
	// refused). Record what happened instead of aborting silently, so the
	// popup's recent list reflects reality.
	try {
		await warmHeaderCache(downloadItem.url);
		const headers = await getCachedHeaders(downloadItem.url);

		const metadata = normalizeMetadata(
			downloadItem.url,
			downloadItem.filename || undefined,
			headers ?? {},
			FALLBACK_UA
		);

		const lgomUrl = buildLgomUrl(metadata, config.maxUrlLength);
		const channel = await triggerProtocol(metadata, lgomUrl);

		await appendRecent({
			url: downloadItem.url,
			name: metadata.name,
			ok: true,
			// `forwarded-port` = the client's local port took it;
			// `forwarded-tab` = the lgom:// fallback was used instead.
			reason: handoffReason(channel),
			timestamp: Date.now()
		});
	} catch (error) {
		await appendRecent({
			url: downloadItem.url,
			name: downloadItem.filename,
			ok: false,
			reason: describeError(error),
			timestamp: Date.now()
		});
	}
});

/**
 * @param {unknown} error
 * @returns {string}
 */
function describeError(error) {
	return `failed: ${String(/** @type {{message?: string}} */ (error)?.message ?? error)}`;
}
