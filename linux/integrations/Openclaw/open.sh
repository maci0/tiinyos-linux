#!/bin/sh
# Linux open for the OpenClaw integration: the bundle's open.sh (stop the
# old gateway, configure, migrate, start the gateway, open the dashboard)
# with an `open` on PATH that calls xdg-open.
set -eu
here=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
PATH="$here/bin:$PATH" exec sh "$here/../open.sh"
