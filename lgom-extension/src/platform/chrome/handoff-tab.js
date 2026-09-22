import { HANDOFF_PAGE, HANDOFF_TAB_LOAD_TIMEOUT } from '$lib/constants.js';
import { browser } from '$lib/platform-api.js';

/** In-flight tab creation, so concurrent downloads share one hand-off tab. */
/** @type {Promise<number> | null} */
let creating = null;

/**
 * Hand the URL to the OS through the browser's external protocol launcher.
 *
 * Used when the native messaging host is not installed. Two things make this
 * channel bearable compared to a fresh tab per download:
 *
 * 1. **One tab, reused.** The hand-off tab is created once and navigated from
 *    then on. An external protocol navigation never commits — the browser bails
 *    out of it — so the tab falls back to the extension page it was showing and
 *    stays ready for the next URL. Opening a fresh tab per download (what this
 *    channel replaces) is what left the user with a tab per download.
 * 2. **`tabs.update` instead of `tabs.create`.** Chrome attaches an initiating
 *    origin to the navigation only for extension APIs that set one
 *    (`UpdateFunction` sets `initiator_origin = extension()->origin()`, while
 *    `CreateFunction` leaves it unset unless `setSelfAsOpener` is used). Its
 *    confirmation dialog offers "always allow" only for a trustworthy
 *    initiating origin (`MayRememberAllowDecisionsForThisOrigin` →
 *    `network::IsOriginPotentiallyTrustworthy`, and `chrome-extension://` is
 *    registered as a secure scheme), so the reused tab is what lets one click
 *    exempt every later hand-off. A tab opened directly on the `lgom://` URL
 *    has no origin to remember and prompts again every time.
 *
 * @param {string} url  The `lgom://download?…` hand-off URL.
 * @returns {Promise<void>}
 */
export async function handOffViaTab(url) {
	const tabId = await acquireHandoffTab();
	await browser.tabs.update(tabId, { url, active: false });
}

/**
 * Find the hand-off tab, creating and loading it on demand.
 *
 * Looked up by URL rather than cached: the service worker can be evicted
 * between downloads, and the user may close the tab at any point.
 *
 * @returns {Promise<number>}
 */
async function acquireHandoffTab() {
	const existing = await findHandoffTab();
	if (existing !== null) return existing;

	creating ??= createHandoffTab().finally(() => {
		creating = null;
	});
	return creating;
}

/**
 * @returns {Promise<number | null>}
 */
async function findHandoffTab() {
	try {
		const [tab] = await browser.tabs.query({ url: browser.runtime.getURL(HANDOFF_PAGE) });
		return tab?.id ?? null;
	} catch {
		// The URL filter is rejected (no matching origin scope): fall through to
		// creating a tab instead of failing the hand-off.
		return null;
	}
}

/**
 * Create the hand-off tab and wait until the page has committed.
 *
 * Navigating a tab that has no committed navigation to an external protocol
 * makes Chrome discard the tab instead of returning it to its page, which
 * would turn every hand-off into a fresh tab — hence the wait.
 *
 * @returns {Promise<number>}
 */
async function createHandoffTab() {
	const tab = await browser.tabs.create({
		url: browser.runtime.getURL(HANDOFF_PAGE),
		active: false
	});
	if (tab.id === undefined) throw new Error('hand-off tab has no id');

	const tabId = tab.id;
	await waitForCommit(tabId);
	return tabId;
}

/**
 * @param {number} tabId
 * @returns {Promise<void>}
 */
async function waitForCommit(tabId) {
	if (await hasCommitted(tabId)) return;

	/** @type {Promise<void>} */
	const committed = new Promise((resolve) => {
		/** @param {number} updatedId @param {{status?: string}} change */
		const onUpdated = (updatedId, change) => {
			if (updatedId === tabId && change.status === 'complete') finish();
		};
		const finish = () => {
			clearTimeout(timer);
			browser.tabs.onUpdated.removeListener(onUpdated);
			resolve();
		};
		const timer = setTimeout(finish, HANDOFF_TAB_LOAD_TIMEOUT);
		browser.tabs.onUpdated.addListener(onUpdated);
		// Re-check after subscribing: the page may have committed in between.
		hasCommitted(tabId).then((loaded) => loaded && finish());
	});

	await committed;
}

/**
 * @param {number} tabId
 * @returns {Promise<boolean>}
 */
async function hasCommitted(tabId) {
	try {
		return (await browser.tabs.get(tabId)).status === 'complete';
	} catch {
		// Tab is gone (closed while loading); the caller's update will report it.
		return true;
	}
}
