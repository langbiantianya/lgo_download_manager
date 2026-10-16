// JSDoc type definitions shared across the extension.

/**
 * @typedef {object} DownloadMetadata
 * @property {string} url           Resource URL.
 * @property {string} name          Display filename.
 * @property {string} ua            User-Agent of the request.
 * @property {string} headers       Forwarded request headers, serialized.
 * @property {string} cookies       Cookie header, serialized.
 * @property {string} referer       Referer header.
 * @property {number} timestamp     Interception time (ms since epoch).
 */

/**
 * @typedef {object} HeaderEvent
 * @property {string} url
 * @property {Array<{name: string, value?: string}>} [requestHeaders]
 */

/**
 * How a hand-off reached the desktop client. Tried in this order, cheapest and
 * quietest first (see each platform's `triggerProtocol`).
 *
 * - `port`:   through the desktop client's loopback HTTP port. No tab, no
 *             confirmation, no extra install — but the client has to be running.
 * - `tab`:    by navigating an extension tab to `lgom://`, which the browser
 *             gates behind its own confirmation dialog. Last resort, and the
 *             only channel that can cold-start the client when it is not
 *             running.
 *
 * @typedef {'port' | 'tab'} HandoffChannel
 */

/**
 * @typedef {object} PlatformBridge
 * @property {() => string} getPlatformName
 * @property {(meta: DownloadMetadata, url: string) => Promise<HandoffChannel>} triggerProtocol
 * @property {() => Promise<string>} getActiveTabUrl
 */

export {};

/**
 * Body of a local-port hand-off request (`POST /download`): the same fields as
 * the `lgom://download?…` query string, without the URL encoding or the size
 * budget.
 *
 * @typedef {object} PortHandoffPayload
 * @property {string} url
 * @property {string} name
 * @property {string} ua
 * @property {string} headers
 * @property {string} cookies
 */

/**
 * @typedef {object} LgomParams
 * @property {string} url
 * @property {string} name
 * @property {string} ua
 * @property {string} headers
 * @property {string} cookies
 */

/**
 * @typedef {object} InterceptConfig
 * @property {boolean} enabled            Whether interception is active.
 * @property {'all' | 'whitelist' | 'blacklist'} mode
 * @property {string[]} patterns          URL patterns for the current mode.
 * @property {number} maxUrlLength        Budget for the generated lgom:// URL.
 */

/**
 * @typedef {object} RecentDownload
 * @property {string} url
 * @property {string} name
 * @property {boolean} ok
 * @property {string} reason
 * @property {number} timestamp
 */
