#!/bin/sh
# Linux open for the OpenCode integration: applies the Tiiny provider
# (the bundle's configure.sh) and starts opencode in a terminal.
set -eu
here=$(dirname "$0")
sh "$here/../configure.sh"
binary="$TIINY_INSTALL_DIR/opencode"
test -x "$binary"
. "$here/terminal.sh"
run_in_terminal "$binary"
