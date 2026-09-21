import { buildLgomUrl } from '$lib/url-builder.js';
import { normalizeMetadata, shouldIntercept } from '$lib/metadata.js';
import { getConfig } from '$lib/storage.js';
import { appendRecent } from '$lib/recent.js';
import { registerHeaderListener, getCachedHeaders, warmHeaderCache } from './headers.js';
import { triggerProtocol } from './protocol.js';

registerHeaderListener();

/** Default UA when the request carried none (should not happen in practice). */
const FALLBACK_UA =
	'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36';

/**
 * Chrome fires `onCreated` after the file has already started, so the
 * download is cancelled + erased and handed to LGOM through the URL protocol.
 */
globalThis.chrome.downloads.onCreated.addListener(async (downloadItem) => {
	const browser = globalThis.chrome;
	const config = await getConfig();
	if (!config.enabled) return;

	if (!shouldIntercept(downloadItem.url, config)) {
		await recordRecent(downloadItem.url, downloadItem.filename, false, 'skipped');
		return;
	}

	await browser.downloads.cancel(downloadItem.id).catch(() => {});
	await browser.downloads.erase({ id: downloadItem.id }).catch(() => {});

	await warmHeaderCache(downloadItem.url);
	const headers = await getHeaders(downloadItem.url);

	const metadata = normalizeMetadata(
		downloadItem.url,
		downloadItem.filename || undefined,
		headers ?? {},
		FALLBACK_UA
	);

	const lgomUrl = buildLgomUrl(metadata, config.maxUrlLength);
	await triggerProtocol(lgomUrl);

	await recordRecent(downloadItem.url, metadata.name, true, 'forwarded');
});

/**
 * @param {string} url
 * @returns {Promise<Record<string, string | string[]> | undefined>}
 */
async function getHeaders(url) {
	return getCachedHeaders(url);
}

/**
 * @param {string} url
 * @param {string} name
 * @param {boolean} ok
 * @param {string} reason
 */
async function recordRecent(url, name, ok, reason) {
	await appendRecent({ url, name, ok, reason, timestamp: Date.now() });
}
