#!/bin/sh
# Cline is installed when the unpacked app and CLI are there.
set -eu
. "$(dirname "$0")/cline.sh"
test -x "$CLINE_APP" && test -x "$CLINE_CLI"
