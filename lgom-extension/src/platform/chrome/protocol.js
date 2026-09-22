/**
 * Hand the generated URL to the OS.
 *
 * Chrome resolves `lgom://` from a normal navigation, but an unapproved scheme
 * first turns that navigation into an "Open LGOM?" confirmation dialog that
 * belongs to the new tab's WebContents. Destroying the tab answers nothing and
 * drops the launch outright, so the tab is deliberately left alone: Chrome
 * closes it on its own once the external handler has been launched.
 *
 * A tab whose dialog the user dismissed stays behind, because an unanswered
 * dialog and a dismissed one look identical from the extension side, and a
 * batch download may legitimately leave several prompts queued at once. The
 * tab is left for the user to close rather than swept.
 *
 * `active: false` keeps the user's focus where it was; Chrome presents the
 * dialog through the browser window and activates the tab itself when an
 * answer is needed.
 *
 * @param {string} url
 * @returns {Promise<void>}
 */
export async function triggerProtocol(url) {
	await globalThis.chrome.tabs.create({ url, active: false });
}
