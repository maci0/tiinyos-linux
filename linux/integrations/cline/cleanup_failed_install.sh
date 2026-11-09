#!/bin/sh
# Removes what a failed install left and undoes its settings changes.
set -eu
. "$(dirname "$0")/cline.sh"
stop_cline
node_action rollback
frontend_action restore
rm -rf "$TIINY_INSTALL_DIR/.app.new" "$TIINY_INSTALL_DIR/.deb"
