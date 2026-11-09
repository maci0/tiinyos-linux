#!/bin/sh
# Removes the KiloCode extension from every editor found, then the
# integration's install folder.
set -eu
sh "$(dirname "$0")/find-editor.sh" | while IFS= read -r cli; do
  "$cli" --uninstall-extension kilocode.kilo-code >/dev/null 2>&1 || true
done
rm -rf "$TIINY_INSTALL_DIR"
