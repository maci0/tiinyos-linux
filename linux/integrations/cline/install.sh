#!/bin/sh
# Linux install for the Cline integration: unpacks the official
# Cline_<version>_amd64.deb with the launcher's 7-Zip (TIINY_7ZZ) into
# the integration's directory and adds a menu entry. Needs the system's
# WebKitGTK 4.1, as Cline's own package does.
set -eu
. "$(dirname "$0")/cline.sh"
test -f "$TIINY_APP_ARCHIVE"
stage="$TIINY_INSTALL_DIR/.app.new"
deb="$TIINY_INSTALL_DIR/.deb"
rm -rf "$stage" "$deb"
trap 'rm -rf "$stage" "$deb"' EXIT
mkdir -p "$deb"
"$TIINY_7ZZ" x -y -o"$deb" "$TIINY_APP_ARCHIVE" >/dev/null
"$TIINY_7ZZ" x -y -o"$stage" "$deb"/data.tar* >/dev/null
test -x "$stage/usr/bin/cline-app" && test -x "$stage/usr/bin/cline-cli"
stop_cline
node_action begin
rm -rf "$TIINY_INSTALL_DIR/app"
mv "$stage" "$TIINY_INSTALL_DIR/app"
mkdir -p "$(dirname "$CLINE_DESKTOP")"
cat >"$CLINE_DESKTOP" <<ENTRY
[Desktop Entry]
Name=Cline
Comment=Cline AI coding agent
Exec="$CLINE_APP"
Icon=$TIINY_INSTALL_DIR/app/usr/share/icons/hicolor/128x128/apps/cline-app.png
StartupWMClass=cline-app
Terminal=false
Type=Application
Categories=Development;
ENTRY
