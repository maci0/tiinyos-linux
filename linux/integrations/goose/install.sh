#!/bin/sh
# Linux install for the Goose integration: unpacks the official
# goose_<version>_amd64.deb with the launcher's 7-Zip (TIINY_7ZZ) into
# the integration's directory (no root, no package manager) and adds a
# menu entry.
set -eu
test -f "$TIINY_APP_ARCHIVE"
here=$(dirname "$0")
app="$TIINY_INSTALL_DIR/app"
stage="$TIINY_INSTALL_DIR/.app.new"
deb="$TIINY_INSTALL_DIR/.deb"
rm -rf "$stage" "$deb"
trap 'rm -rf "$stage" "$deb"' EXIT
mkdir -p "$deb"
"$TIINY_7ZZ" x -y -o"$deb" "$TIINY_APP_ARCHIVE" >/dev/null
# 7-Zip skips the archive's usr/bin/goose link (it points outside); unused.
"$TIINY_7ZZ" x -y -o"$stage" "$deb"/data.tar* >/dev/null || true
test -x "$stage/usr/lib/goose/Goose"
. "$here/goose.sh"
stop_goose
rm -rf "$app"
mv "$stage" "$app"
mkdir -p "$(dirname "$GOOSE_DESKTOP")"
cat >"$GOOSE_DESKTOP" <<ENTRY
[Desktop Entry]
Name=Goose
Exec="$app/usr/lib/goose/Goose" %U
Icon=$app/usr/share/pixmaps/goose.png
Terminal=false
Type=Application
Categories=Development;
ENTRY
