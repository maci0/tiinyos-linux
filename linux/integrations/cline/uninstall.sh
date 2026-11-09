#!/bin/sh
# Stops Cline, puts the user's own provider and UI settings back, and
# removes the app and its menu entry.
set -eu
. "$(dirname "$0")/cline.sh"
stop_cline
node_action restore
frontend_action restore
rm -rf "$TIINY_INSTALL_DIR/app" "$CLINE_DESKTOP"
