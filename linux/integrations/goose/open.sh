#!/bin/sh
# Applies the Tiiny provider (the bundle's configure.sh), then starts
# Goose detached from TiinyOS.
set -eu
here=$(dirname "$0")
. "$here/goose.sh"
test -x "$GOOSE_BINARY"
sh "$here/../configure.sh"
cd "$HOME"
unset ELECTRON_RUN_AS_NODE
setsid "$GOOSE_BINARY" >/dev/null 2>&1 &
