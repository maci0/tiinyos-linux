#!/bin/sh
# KiloCode needs a host editor; fail before any download without one.
set -eu
if [ -z "$(sh "$(dirname "$0")/find-editor.sh")" ]; then
  echo 'KiloCode requires VS Code, VS Code Insiders, Cursor, or VSCodium. Install one of them and try again.' >&2
  exit 2
fi
