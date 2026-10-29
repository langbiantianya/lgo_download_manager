import { DOWNLOAD_SESSION_STARTED_KEY } from './constants.js';
import { browser } from './platform-api.js';

/**
 * Start timestamp for this background context. storage.session supplies the
 * same boundary after the background page or service worker wakes up again;
 * the in-memory promise avoids racing multiple download events on first start.
 *
 * @type {Promise<number | null> | null}
 */
let sessionStartedAt = null;

/**
 * Return the point in the current browser session after which a download must
 * have started to be considered new.
 *
 * Chrome restores queued/interrupted items during browser startup and emits
 * `downloads.onCreated` for them again. Their `startTime` belongs to an older
 * browser session. storage.session is cleared on restart, so the first
 * background start in the new session establishes a fresh boundary.
 *
 * @returns {Promise<number | null>} Null when storage is unavailable; callers
 * should fail open and handle downloads normally rather than silently stopping
 * interception.
 */
export function getDownloadSessionStartedAt() {
	if (sessionStartedAt === null) {
		sessionStartedAt = loadSessionStartedAt();
	}
	return sessionStartedAt;
}

/**
 * @returns {Promise<number | null>}
 */
async function loadSessionStartedAt() {
	try {
		const stored = await browser.storage.session.get(DOWNLOAD_SESSION_STARTED_KEY);
		const existing = stored[DOWNLOAD_SESSION_STARTED_KEY];
		if (typeof existing === 'number' && Number.isFinite(existing)) return existing;

		const startedAt = Date.now();
		await browser.storage.session.set({ [DOWNLOAD_SESSION_STARTED_KEY]: startedAt });
		return startedAt;
	} catch {
		// Losing the session marker is not a reason to stop intercepting new
		// downloads. Without a trustworthy boundary, fail open.
		return null;
	}
}

/**
 * Whether a `downloads.onCreated` item is a download restored from an earlier
 * browser session rather than one initiated since this session started.
 *
 * @param {{ startTime?: string | number }} downloadItem
 * @param {number | null} sessionStart
 * @returns {boolean}
 */
export function isRestoredDownload(downloadItem, sessionStart) {
	if (sessionStart === null) return false;

	const startedAt =
		typeof downloadItem.startTime === 'number'
			? downloadItem.startTime
			: Date.parse(String(downloadItem.startTime ?? ''));
	if (!Number.isFinite(startedAt)) return false;

	return startedAt < sessionStart;
}
