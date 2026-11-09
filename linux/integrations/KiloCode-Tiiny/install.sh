#!/bin/sh
# Installs the KiloCode extension archive into the first editor found.
set -eu
[ -f "$TIINY_APP_ARCHIVE" ]
mkdir -p "$TIINY_INSTALL_DIR"
cp "$TIINY_APP_ARCHIVE" "$TIINY_INSTALL_DIR/KiloCode.vsix"
cli=$(sh "$(dirname "$0")/find-editor.sh" | head -n 1)
if [ -z "$cli" ]; then
  echo 'KiloCode requires VS Code, VS Code Insiders, Cursor, or VSCodium to be installed.' >&2
  exit 2
fi
if ! "$cli" --install-extension "$TIINY_APP_ARCHIVE" --force >/dev/null 2>&1; then
  echo "KiloCode failed to install the extension through $cli." >&2
  exit 1
fi
