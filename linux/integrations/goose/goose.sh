# Shared by the Goose integration's Linux scripts.
GOOSE_BINARY="$TIINY_INSTALL_DIR/app/usr/lib/goose/Goose"
GOOSE_DESKTOP="${XDG_DATA_HOME:-$HOME/.local/share}/applications/tiiny-goose.desktop"

# stop_goose ends this install's Goose processes, matched by exact
# executable path, never by name.
stop_goose() {
  for proc in /proc/[0-9]*; do
    [ "$(readlink "$proc/exe" 2>/dev/null)" = "$GOOSE_BINARY" ] && kill "${proc#/proc/}" 2>/dev/null
  done
  return 0
}
