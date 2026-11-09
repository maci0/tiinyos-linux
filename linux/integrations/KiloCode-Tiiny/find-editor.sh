#!/bin/sh
# Prints each VS Code-family editor CLI on PATH, one per line, VSCodium
# included (Linux replacement for the bundle's macOS editor search).
set -eu
for name in code code-insiders cursor codium; do
  command -v "$name" 2>/dev/null || true
done
