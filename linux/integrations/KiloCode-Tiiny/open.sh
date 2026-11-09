#!/bin/sh
# Linux open script for the KiloCode integration: shows the extension in
# the first VS Code-family editor that find-editor.sh reports. The bundle's
# macOS script looks in /Applications and uses `open -a` and osascript.
set -eu
extension_id=kilocode.kilo-code
cli=$(sh "$(dirname "$0")/../extension/find-editor.sh" | head -n 1)
if test -z "$cli"; then
  echo 'KiloCode requires VS Code, VS Code Insiders, or Cursor. Install one of them and try again.' >&2
  exit 2
fi
case $(basename "$cli") in
  code-insiders) scheme=vscode-insiders ;;
  cursor) scheme=cursor ;;
  *) scheme=vscode ;;
esac
# The editor CLI starts or reuses the editor and hands it the URL.
"$cli" --open-url "$scheme:extension/$extension_id" >/dev/null 2>&1 &
