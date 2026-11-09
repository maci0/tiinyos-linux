# Shared by the Cline integration's Linux scripts. The bundle's
# runtime.cjs (Tiiny's) keeps the provider settings in
# ~/.cline/data/settings; the app itself is this integration's own
# unpacked .deb.
HERE=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
CLINE_APP="$TIINY_INSTALL_DIR/app/usr/bin/cline-app"
CLINE_CLI="$TIINY_INSTALL_DIR/app/usr/bin/cline-cli"
CLINE_DESKTOP="${XDG_DATA_HOME:-$HOME/.local/share}/applications/tiiny-cline.desktop"

node_action() { ELECTRON_RUN_AS_NODE=1 "$TIINY_NODE_EXECUTABLE" "$HERE/../runtime.cjs" "$@"; }
frontend_action() { ELECTRON_RUN_AS_NODE=1 "$TIINY_NODE_EXECUTABLE" "$HERE/frontend.js" "$@"; }

# cline_env runs a command without TiinyOS's Node mode or the Tiiny key.
cline_env() { env -u ELECTRON_RUN_AS_NODE -u TIINY_AUTH_KEY "$@"; }

# stop_cline ends this install's app (matched by exact executable path,
# never by name) and its hub daemon.
stop_cline() {
  for proc in /proc/[0-9]*; do
    [ "$(readlink "$proc/exe" 2>/dev/null)" = "$CLINE_APP" ] && kill "${proc#/proc/}" 2>/dev/null
  done
  if [ -x "$CLINE_CLI" ]; then cline_env "$CLINE_CLI" hub stop >/dev/null 2>&1 || true; fi
  return 0
}

start_cline() {
  cd "$HOME"
  cline_env setsid "$CLINE_APP" >/dev/null 2>&1 &
}
