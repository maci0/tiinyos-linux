#!/bin/sh
# Goose is installed when the unpacked app is there.
set -eu
. "$(dirname "$0")/goose.sh"
test -x "$GOOSE_BINARY"
