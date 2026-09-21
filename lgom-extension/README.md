# LGOM browser extension

Intercepts browser downloads and hands them to the LGOM desktop client
(`lgo_download_manager`) through the `lgom://` URL scheme.

One codebase, two platform bundles: shared logic in `src/lib`, per-browser
manifests, background scripts and protocol triggers in `src/platform/<target>`,
and a Svelte 5 popup + options surface in `src/popup` / `src/options`.

## Requirements

| Host                          | Manifest | Background context       |
| ----------------------------- | -------- | ------------------------ |
| Chrome / Chromium / Edge 102+ | MV3      | ES module service worker |
| Firefox 140+                  | MV3      | ES module event page     |

The Chrome floor is set by `chrome.storage.session` (102+) for the header-cache
mirror, and is enforced by `minimum_chrome_version` in that manifest.

Firefox does not implement MV3 service workers, so its background has to be
declared as an event page (`background.scripts`) instead of
`background.service_worker`; declaring the latter makes Firefox refuse to
install with _"background.service_worker is currently disabled. Add
background.scripts."_ Firefox's `strict_min_version` is 140.0 because that is
the version that introduced `data_collection_permissions`, which is mandatory
for new add-ons.

Both hosts load the background bundle as an **ES module**. Vite emits it with
`import ... from "./chunks/..."` statements, which a classic background script
cannot execute — silently killing the extension. Keep `"type": "module"` in
both manifests in sync with the bundler output.

## Build

```sh
npm install
npm run build:all        # -> build/chrome/ and build/firefox/
```

| Script                  | Effect                               |
| ----------------------- | ------------------------------------ |
| `npm run build:chrome`  | Chrome bundle into `build/chrome/`   |
| `npm run build:firefox` | Firefox bundle into `build/firefox/` |
| `npm run build:all`     | Both                                 |
| `npm run dev`           | Rebuild the Chrome bundle on change  |
| `npm run dev:firefox`   | Same, for Firefox                    |
| `npm run check`         | `svelte-check` over `jsconfig.json`  |
| `npm run lint`          | `prettier --check` + `eslint`        |
| `npm run format`        | `prettier --write`                   |

Each build stages Vite output in a temp directory and assembles the extension
root, so the two platform trees never mix. Result:

```
build/<platform>/
  manifest.json      # src/platform/<platform>/manifest.json
  background.js      # bundled background entry
  chunks/            # shared chunks statically imported by the entries
  popup.html|css|js  # src/popup
  options.html|css|js
  icons/             # src/static/icons
  _locales/          # src/static/_locales
```

## Loading

```sh
npm run build:chrome

# Chromium-family browsers (see the caveat below)
chrome  --load-extension="$PWD/build/chrome"
# or: chrome://extensions -> enable Developer mode -> Load unpacked -> build/chrome
```

```sh
npm run build:firefox
# about:debugging#/runtime/this-firefox -> Load Temporary Add-on -> build/firefox/manifest.json
```

Branded Google Chrome ≥ 137 ignores `--load-extension` (the command line flag is
only honoured by Chromium and Chrome for Testing builds). Load unpacked through
`chrome://extensions`, or drive a Chromium-family build such as
`/usr/bin/microsoft-edge --load-extension=...` for scripted testing.

The unpacked Chrome extension ID is derived from the directory path, so moving
`build/chrome` changes the ID. Pin it with a `key` in the Chrome manifest before
anything starts whitelisting that ID (for example a future native messaging
host manifest).

## How interception works

1. `webRequest.onBeforeSendHeaders` (observe-only, no `blocking`) caches the
   request headers per URL in an in-memory `Map`, mirrored into
   `storage.session` behind a 250 ms debounce. Both hosts evict the background
   context when idle, so the in-memory map alone is not enough; Chrome only
   grants `Cookie`/`User-Agent` visibility with the `extraHeaders` opt-in.
2. `downloads.onCreated` fires. If interception is disabled or the URL does not
   match the active filter mode, nothing is touched.
3. The download is cancelled and erased.
4. The cached headers are turned into metadata (`normalizeMetadata`), and
   `buildLgomUrl` produces the hand-off URL.
5. `triggerProtocol` opens an inactive tab on the `lgom://` URL and removes the
   tab after 1 s. Firefox additionally injects a hidden iframe as a fallback,
   because it can refuse top-level navigation to an unknown external scheme.
6. The outcome is appended to the recent-interception log (newest first, last
   50 entries) — including hand-off failures, so the popup never reports
   "no interceptions yet" for a download that was actually intercepted and then
   failed to forward.

## Hand-off URL contract

```
lgom://download?url=<encoded>[&name=<encoded>[&ua=<encoded>][&headers=<encoded>&cookies=<encoded>]]
```

`headers` is a `Name: value` line list; hop-by-hop and host-bound headers
(`host`, `connection`, `content-length`, `accept-encoding`, `transfer-encoding`,
`te`, `trailer`, `upgrade`) are stripped. When the fully encoded URL would
exceed `maxUrlLength`, it degrades to the core params (`url`, `name`, `ua`) so
the hand-off stays inside the LGOM IPC frame limit (`MAX_FRAME_LEN`, 64 KiB).

### The browser confirmation prompt

Handing off via `lgom://` is an **external protocol launch**, which the browser
gates behind a confirmation dialog ("Open LGOM?"). That dialog cannot be
suppressed from extension code — it is browser security policy, not an
extension behaviour. One-time opt-outs:

- **Chrome:** tick _Always allow … to open links of this type_ in the dialog.
  Administrators can pre-allow with the `AutoLaunchProtocolsFromOrigins`
  policy.
- **Firefox:** set `network.protocol-handler.warn-external.lgom` to `false` in
  `about:config`.

Removing the prompt entirely requires a channel the browser does not gate —
`runtime.sendNativeMessage` with a native messaging host installed by the
desktop app, or a loopback HTTP bridge. Neither is implemented yet.

## Configuration

Settings live on the options page (`chrome://extensions` → Details → Extension
options; `about:addons` → Preferences).

| Field          | Default | Notes                                                      |
| -------------- | ------- | ---------------------------------------------------------- |
| `enabled`      | `true`  | Master switch; when off no download is touched             |
| `mode`         | `all`   | `all` \| `whitelist` \| `blacklist`                        |
| `patterns`     | `[]`    | Regexes; an invalid pattern degrades to substring matching |
| `maxUrlLength` | `2000`  | Clamped to 200–8000 on save                                |

Filter semantics: `all` intercepts everything; `whitelist` intercepts only
matching URLs; `blacklist` intercepts everything except matches.

Storage keys: `lgom_config` (`storage.local`), `lgom_recent`
(`storage.local`), `lgom_header_cache` (`storage.session`).

## Permissions

| Permission                 | Why                                            |
| -------------------------- | ---------------------------------------------- |
| `downloads`                | `onCreated` plus `cancel` / `erase`            |
| `webRequest`               | observe request headers for the hand-off       |
| `storage`                  | config, recent log, header-cache mirror        |
| `tabs`                     | open the hand-off tab and remove it            |
| `scripting` (Firefox only) | hidden-iframe fallback for the protocol launch |
| `<all_urls>`               | intercept downloads from any origin            |

Permissions are declared per platform: Chrome omits `scripting` because it has
no iframe fallback, and neither manifest requests `webRequestBlocking` since
requests are only observed.

## Localisation

UI strings live in `src/static/_locales/<locale>/messages.json` and are read
through `i18nGetMessage()` (`src/lib/platform-api.js`), which wraps
`browser.i18n.getMessage` and falls back to the key when the API is absent.
`en` is `default_locale`; `zh_CN` is complete. To add a locale, copy
`_locales/en` under the new tag and translate the values — the manifest's
`__MSG_extensionName__` / `__MSG_extensionDescription__` placeholders resolve
from the same file.

## Layout

| Path                      | Contents                                                                   |
| ------------------------- | -------------------------------------------------------------------------- |
| `src/lib/constants.js`    | protocol, storage keys, cache tuning, defaults                             |
| `src/lib/types.js`        | JSDoc typedefs (no runtime exports)                                        |
| `src/lib/platform-api.js` | `browser`/`chrome` handle, `i18nGetMessage`, platform name                 |
| `src/lib/url-builder.js`  | header serialisation, `buildLgomUrl`                                       |
| `src/lib/metadata.js`     | `normalizeMetadata`, `shouldIntercept`                                     |
| `src/lib/storage.js`      | config load/save                                                           |
| `src/lib/recent.js`       | recent-interception log                                                    |
| `src/platform/chrome/`    | MV3 manifest, module service worker, header cache, protocol trigger        |
| `src/platform/firefox/`   | MV3 manifest, event page, header cache, protocol trigger + iframe fallback |
| `src/popup/`              | toolbar popup (status, master toggle, recent list)                         |
| `src/options/`            | options page (protocol, filter rules, about)                               |
| `src/static/`             | icons and `_locales` copied verbatim into the build                        |
| `src/types.d.ts`          | ambient `chrome`/`browser` declarations (no `@types/chrome`)               |
| `src/routes/layout.css`   | the Tailwind v4 entry imported by both UI surfaces                         |

The project is JavaScript with JSDoc types, not TypeScript; `jsconfig.json` has
`checkJs` enabled and `svelte-check` is the type gate.

## Verifying a build

```sh
npm run check && npm run lint
```

For runtime checks, load the built extension and drive it through the DevTools
protocol — the background script's storage is the easiest signal:

```js
// in the service worker context
await chrome.downloads.download({ url: 'https://example.com/file.zip' });
await chrome.downloads.search({}); // [] once the interception cancelled + erased it
await chrome.storage.local.get('lgom_recent'); // [{ ok: true, reason: 'forwarded' }]
```
