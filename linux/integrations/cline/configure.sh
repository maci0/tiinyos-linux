#!/bin/sh
# Writes the Tiiny provider (runtime.cjs), then the app's UI choice
# (frontend.js). The app creates its storage on first start, so a first
# configure starts it once, waits for the storage, and stops it.
set -eu
. "$(dirname "$0")/cline.sh"
stop_cline
node_action configure
if ! frontend_action status; then
  start_cline
  for _ in $(seq 1 20); do
    frontend_action status && break
    sleep 1
  done
  stop_cline
  sleep 1
fi
frontend_action configure
cline_env "$CLINE_CLI" hub ensure --json --cwd "$HOME" >/dev/null
