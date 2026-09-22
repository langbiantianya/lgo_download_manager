#!/bin/sh
#
# Register the LGOM native messaging host with the Chromium-family browsers
# installed for the current user.
#
# The extension can send messages to a native host, but nothing in the
# extension ecosystem can *install* one — the manifest has to be placed by a
# local program. That is all this script does: copy the bridge into a stable
# user directory and point every browser at it.
#
# Usage:
#   ./install.sh --extension-id <id>[,<id>…] [options]
#   ./install.sh --uninstall
#
# Options:
#   --extension-id <ids>   Extension ID(s) allowed to use the host. Chrome's
#                          unpacked ID depends on the extension directory, the
#                          packaged ID on the signing key; take it from
#                          chrome://extensions while the extension is loaded.
#   --lgdm-bin <path>      Absolute path to the desktop client, if it is not on
#                          PATH (Flatpak installs are detected automatically).
#   --browsers <list>      Comma-separated browser keys to register with.
#                          Default: every browser with a profile directory.
#   --user-data-dir <dir>  Also register with a custom profile directory, i.e.
#                          the `--user-data-dir` a browser was started with.
#   --uninstall            Remove the manifests and the installed bridge.
#
# Browser keys: google-chrome, google-chrome-beta, google-chrome-unstable,
# google-chrome-for-testing, chromium, microsoft-edge, brave-browser, vivaldi.

set -eu

HOST_NAME=org.langbiantianya.lgom
SOURCE_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
INSTALL_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/lgom-native-host"

# Where a browser keeps its profile directory (mirrors chrome::DIR_USER_DATA).
# Windows is not supported: a native messaging host there has to be an
# executable, not a shell script.
case "$(uname -s)" in
Linux) CONFIG_BASE="${XDG_CONFIG_HOME:-$HOME/.config}" ;;
Darwin) CONFIG_BASE="$HOME/Library/Application Support" ;;
*) CONFIG_BASE='' ;;
esac

DEFAULT_BROWSERS='google-chrome,google-chrome-beta,google-chrome-unstable,google-chrome-for-testing,chromium,microsoft-edge,brave-browser,vivaldi'

EXTENSION_IDS=''
LGDM_BIN=''
BROWSERS=''
CUSTOM_DIRS=''
UNINSTALL=0

usage() {
	sed -n '2,/^set -eu$/p' "$0" | sed -e 's/^# \{0,1\}//' -e '$d'
}

die() {
	printf 'install.sh: %s\n' "$1" >&2
	exit 1
}

note() {
	printf '%s\n' "$1"
}

# Profile directory of a known browser key.
browser_config_dir() {
	[ -n "$CONFIG_BASE" ] || return 1
	case "$1" in
	google-chrome) printf '%s/google-chrome' "$CONFIG_BASE" ;;
	google-chrome-beta) printf '%s/google-chrome-beta' "$CONFIG_BASE" ;;
	google-chrome-unstable) printf '%s/google-chrome-unstable' "$CONFIG_BASE" ;;
	google-chrome-for-testing) printf '%s/google-chrome-for-testing' "$CONFIG_BASE" ;;
	chromium) printf '%s/chromium' "$CONFIG_BASE" ;;
	microsoft-edge) printf '%s/microsoft-edge' "$CONFIG_BASE" ;;
	brave-browser) printf '%s/BraveSoftware/Brave-Browser' "$CONFIG_BASE" ;;
	vivaldi) printf '%s/vivaldi' "$CONFIG_BASE" ;;
	*) return 1 ;;
	esac
}

# Write the host manifest into <profile dir>/NativeMessagingHosts.
# $1: browser profile directory, $2: the JSON `allowed_origins` entries.
write_manifest() {
	manifest_dir=$1/NativeMessagingHosts
	mkdir -p "$manifest_dir"
	cat >"$manifest_dir/$HOST_NAME.json" <<-EOF
	{
	  "name": "$HOST_NAME",
	  "description": "Hands LGOM browser-extension downloads to the LGOM desktop client",
	  "path": "$INSTALL_DIR/lgom-bridge.sh",
	  "type": "stdio",
	  "allowed_origins": [$2
	  ]
	}
	EOF
	printf '  %s\n' "$manifest_dir/$HOST_NAME.json"
}

while [ $# -gt 0 ]; do
	case "$1" in
	--extension-id)
		EXTENSION_IDS=$2
		shift 2
		;;
	--lgdm-bin)
		LGDM_BIN=$2
		shift 2
		;;
	--browsers)
		BROWSERS=$2
		shift 2
		;;
	--user-data-dir)
		CUSTOM_DIRS="$CUSTOM_DIRS$2
"
		shift 2
		;;
	--uninstall)
		UNINSTALL=1
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		die "unknown option '$1' (try --help)"
		;;
	esac
done

if [ -z "$CONFIG_BASE" ]; then
	die "unsupported platform '$(uname -s)': on Windows a native messaging host must be an executable, and Chrome reads its manifest from the registry — this installer only handles Linux and macOS"
fi

if [ "$UNINSTALL" -eq 1 ]; then
	removed=0
	old_ifs=$IFS
	IFS=','
	for browser in ${BROWSERS:-$DEFAULT_BROWSERS}; do
		dir=$(browser_config_dir "$browser") || {
			IFS=$old_ifs
			die "unknown browser '$browser' (try --help)"
		}
		manifest="$dir/NativeMessagingHosts/$HOST_NAME.json"
		if [ -f "$manifest" ]; then
			rm -f "$manifest"
			removed=$((removed + 1))
		fi
	done
	IFS=$old_ifs
	for dir in $CUSTOM_DIRS; do
		manifest="$dir/NativeMessagingHosts/$HOST_NAME.json"
		if [ -f "$manifest" ]; then
			rm -f "$manifest"
			removed=$((removed + 1))
		fi
	done
	rm -rf "$INSTALL_DIR"
	note "removed $removed manifest(s) and $INSTALL_DIR"
	exit 0
fi

[ -n "$EXTENSION_IDS" ] || die '--extension-id is required (try --help)'
[ -f "$SOURCE_DIR/lgom-bridge.sh" ] || die "lgom-bridge.sh not found next to $0"

if [ -n "$LGDM_BIN" ]; then
	[ -x "$LGDM_BIN" ] || die "--lgdm-bin '$LGDM_BIN' is not executable"
fi

origins=''
old_ifs=$IFS
IFS=','
for id in $EXTENSION_IDS; do
	[ -n "$id" ] || continue
	origins="$origins
    \"chrome-extension://$id/\","
done
IFS=$old_ifs
[ -n "$origins" ] || die "no extension id in '$EXTENSION_IDS'"
# `allowed_origins` needs their separator, not their trailing comma.
origins=$(printf '%s' "$origins" | sed '$ s/,$//')

note "installing bridge -> $INSTALL_DIR"
mkdir -p "$INSTALL_DIR"
cp "$SOURCE_DIR/lgom-bridge.sh" "$INSTALL_DIR/lgom-bridge.sh"
chmod 755 "$INSTALL_DIR/lgom-bridge.sh"

if [ -n "$LGDM_BIN" ]; then
	printf 'LGDM_BIN=%s\n' "$LGDM_BIN" >"$INSTALL_DIR/bridge.conf"
	chmod 644 "$INSTALL_DIR/bridge.conf"
else
	# Left absent on purpose: the bridge then resolves the client from PATH,
	# Flatpak, or the desktop's `lgom://` handler.
	rm -f "$INSTALL_DIR/bridge.conf"
fi

registered=0
skipped=''
IFS=','
for browser in ${BROWSERS:-$DEFAULT_BROWSERS}; do
	dir=$(browser_config_dir "$browser") || {
		IFS=$old_ifs
		die "unknown browser '$browser' (try --help)"
	}
	# Only touch profile directories that exist: registering with a browser
	# that is not installed would just leave litter behind.
	if [ ! -d "$dir" ]; then
		skipped="$skipped $browser"
		continue
	fi
	write_manifest "$dir" "$origins"
	registered=$((registered + 1))
done
IFS=$old_ifs

for dir in $CUSTOM_DIRS; do
	write_manifest "$dir" "$origins"
	registered=$((registered + 1))
done

if [ "$registered" -eq 0 ]; then
	die 'no browser profile directory found; pass --user-data-dir or --browsers'
fi

note "registered with $registered profile dir(s)"
if [ -n "$skipped" ]; then
	note "skipped (not installed):$skipped"
fi
note ''
note 'Next: reload the LGOM extension. Hand-offs then skip the tab and the'
note "browser's confirmation dialog; without the host the extension falls back"
note 'to the lgom:// tab.'
