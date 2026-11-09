#!/bin/sh
# Linux open for the Pi integration: the bundle's runtime.js prepares the
# managed install and prints Pi's command line, which runs in a terminal.
set -eu
here=$(dirname "$0")
runtime="$here/../runtime.js"
ELECTRON_RUN_AS_NODE=1 "$TIINY_NODE_EXECUTABLE" "$runtime" migrate
command=$(ELECTRON_RUN_AS_NODE=1 "$TIINY_NODE_EXECUTABLE" "$runtime" command)
. "$here/terminal.sh"
run_in_terminal sh -c "$command"
