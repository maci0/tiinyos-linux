#!/bin/sh
# Shows the KiloCode extension in a VS Code-family editor: the first one
# that already has it installed, else the first one find-editor.sh
# reports (Linux replacement for the bundle's macOS script, which uses
# /Applications, `open -a` and osascript).
set -eu
extension_id=kilocode.kilo-code
editors=$(sh "$(dirname "$0")/find-editor.sh")
if [ -z "$editors" ]; then
  echo 'KiloCode requires VS Code, VS Code Insiders, Cursor, or VSCodium. Install one of them and try again.' >&2
  exit 2
fi
cli=''
for candidate in $editors; do
  case $(basename "$candidate") in
    code-insiders) dir=.vscode-insiders ;;
    cursor) dir=.cursor ;;
    codium) dir=.vscode-oss ;;
    *) dir=.vscode ;;
  esac
  for found in "$HOME/$dir/extensions/$extension_id"-*; do
    if [ -d "$found" ]; then
      cli=$candidate
      break 2
    fi
  done
done
[ -n "$cli" ] || cli=$(printf '%s\n' "$editors" | head -n 1)
case $(basename "$cli") in
  code-insiders) scheme=vscode-insiders ;;
  cursor) scheme=cursor ;;
  codium) scheme=vscodium ;;
  *) scheme=vscode ;;
esac
# The editor CLI starts or reuses the editor and hands it the URL.
"$cli" --open-url "$scheme:extension/$extension_id" >/dev/null 2>&1 &
