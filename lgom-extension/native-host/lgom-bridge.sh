#!/bin/sh
#
# LGOM native messaging host.
#
# The LGOM browser extension talks to this script instead of navigating a tab
# to `lgom://`, which is what lets a download reach the desktop client without
# a tab and without the browser's "Open LGOM?" confirmation.
#
# Responsibilities:
#
#   1. Read one native messaging frame from stdin: a 4-byte length prefix in
#      the host's native byte order followed by a UTF-8 JSON payload. The
#      extension always sends `{"url":"lgom://download?…"}`; the URL is
#      percent-encoded by the extension (`buildLgomUrl`), so the payload never
#      contains a quote or a backslash and can be read with sed.
#   2. Start the desktop client with that URL as an argument. This is the same
#      invocation the `.desktop` file uses (`Exec=lgo_download_manager %u`), so
#      an already running client receives it over its own single-instance
#      socket — this bridge never touches that protocol.
#   3. Answer with one framed JSON message, then exit.
#
# The client is started in its own session so it outlives this process: Chrome
# kills the host as soon as the reply arrives.
#
# POSIX sh on purpose — no Python/Node dependency. Reading assumes a
# little-endian host, which is every platform Chrome ships for.
#
# Options come from `bridge.conf` next to this script (written by install.sh):
#
#   LGDM_BIN=/path/to/lgo_download_manager
#
# Without it the client is resolved from PATH, then Flatpak, then `xdg-open`.

set -eu

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
if [ -f "$SCRIPT_DIR/bridge.conf" ]; then
	# shellcheck source=/dev/null
	. "$SCRIPT_DIR/bridge.conf"
fi

# Frame a JSON document and write it to stdout. The document must be ASCII and
# free of backslashes so the length in bytes equals the character count.
emit() {
	bytes=$(printf '%s' "$1" | wc -c | tr -d ' \n')
	b0=$((bytes % 256))
	b1=$((bytes / 256 % 256))
	b2=$((bytes / 65536 % 256))
	b3=$((bytes / 16777216 % 256))
	printf "\\$(printf '%03o' "$b0")\\$(printf '%03o' "$b1")\\$(printf '%03o' "$b2")\\$(printf '%03o' "$b3")%s" "$1"
}

fail() {
	emit "{\"ok\":false,\"error\":\"$1\"}"
	exit 0
}

# Resolve the desktop client: explicit configuration, PATH, Flatpak, then the
# desktop's own handler (which also covers clients installed outside PATH).
resolve_client() {
	if [ -n "${LGDM_BIN:-}" ] && [ -x "$LGDM_BIN" ]; then
		printf '%s' "$LGDM_BIN"
		return 0
	fi
	if command -v lgo_download_manager >/dev/null 2>&1; then
		command -v lgo_download_manager
		return 0
	fi
	if command -v flatpak >/dev/null 2>&1 && flatpak info org.langbiantianya.LGDM >/dev/null 2>&1; then
		printf 'flatpak'
		return 0
	fi
	if command -v xdg-open >/dev/null 2>&1; then
		printf 'xdg-open'
		return 0
	fi
	return 1
}

launch() {
	# Detach from this process group: Chrome signals the host tree when it
	# tears the pipe down, and the client must survive that.
	if command -v setsid >/dev/null 2>&1; then
		setsid "$@" >/dev/null 2>&1 &
	else
		"$@" >/dev/null 2>&1 &
	fi
}

len=$(dd bs=1 count=4 2>/dev/null | od -An -tu4 2>/dev/null | tr -d ' \n')
# Chrome closed the pipe without sending anything.
[ -n "$len" ] || exit 0
case "$len" in
*[!0-9]*) exit 0 ;;
esac
# Chrome caps messages from the extension at 1 MiB; anything larger is not ours.
[ "$len" -le 1048576 ] || exit 0

body=$(dd bs=1 count="$len" 2>/dev/null)
url=$(printf '%s' "$body" | sed -n 's/.*"url"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')

[ -n "$url" ] || fail 'no-url'
case "$url" in
lgom://*) ;;
*) fail 'not-lgom' ;;
esac

client=$(resolve_client) || fail 'no-client'

case "$client" in
flatpak) launch flatpak run org.langbiantianya.LGDM "$url" ;;
*) launch "$client" "$url" ;;
esac

emit '{"ok":true}'
