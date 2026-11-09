#!/bin/sh
# Exits 0 when the KiloCode extension is installed in a VS Code-family
# editor, VSCodium (~/.vscode-oss) included. Reads the extension folders
# directly; an extension listed in .obsolete awaits removal and does not
# count.
set -eu
for dir in "$HOME/.vscode/extensions" "$HOME/.vscode-insiders/extensions" \
  "$HOME/.cursor/extensions" "$HOME/.vscode-oss/extensions"; do
  for found in "$dir"/kilocode.kilo-code-*; do
    [ -d "$found" ] || continue
    if [ -f "$dir/.obsolete" ] && grep -Fq "\"$(basename "$found")\"" "$dir/.obsolete"; then
      continue
    fi
    exit 0
  done
done
exit 1
