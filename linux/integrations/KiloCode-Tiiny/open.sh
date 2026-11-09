#!/bin/sh
# Shows the KiloCode extension page in the first VS Code-family editor
# that find-editor.sh reports (Linux replacement for the bundle's macOS
# script, which uses /Applications, `open -a` and osascript).
set -eu
extension_id=kilocode.kilo-code
cli=$(sh "$(dirname "$0")/find-editor.sh" | head -n 1)
if [ -z "$cli" ]; then
  echo 'KiloCode requires VS Code, VS Code Insiders, Cursor, or VSCodium. Install one of them and try again.' >&2
  exit 2
fi
case $(basename "$cli") in
  code-insiders) scheme=vscode-insiders ;;
  cursor) scheme=cursor ;;
  codium) scheme=vscodium ;;
  *) scheme=vscode ;;
esac
# The editor CLI starts or reuses the editor and hands it the URL.
"$cli" --open-url "$scheme:extension/$extension_id" >/dev/null 2>&1 &
