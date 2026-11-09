#!/bin/sh
# Linux install for the OpenCode integration: unpacks the official
# opencode-linux-x64.tar.gz and hands the binary to the bundle's
# runtime.js, as the macOS script does with its zip.
set -eu
test -f "$TIINY_APP_ARCHIVE"
tmp=$(mktemp -d "$TIINY_INSTALL_DIR.unpack.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
tar -xzf "$TIINY_APP_ARCHIVE" -C "$tmp"
binary=$(find "$tmp" -type f -name opencode -print -quit)
test -n "$binary"
ELECTRON_RUN_AS_NODE=1 "$TIINY_NODE_EXECUTABLE" "$(dirname "$0")/../runtime.js" install "$binary"
