#!/bin/sh
# Stops Goose, puts the user's own Goose configuration back (the bundle's
# rollback.sh), and removes the app and its menu entry.
set -eu
here=$(dirname "$0")
. "$here/goose.sh"
stop_goose
sh "$here/../rollback.sh" >/dev/null 2>&1 || true
rm -rf "$TIINY_INSTALL_DIR/app" "$GOOSE_DESKTOP"
