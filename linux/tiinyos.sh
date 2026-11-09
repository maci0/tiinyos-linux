#!/bin/sh
# Launch TiinyOS on Linux.
#
# The UI is Tiiny's own Electron app. pack/fetch.py downloads the newest
# official Windows x64 build from Tiiny on every start when a newer one
# exists, and extracts its app.asar under TIINY_APP_DIR. Electron is
# TIINY_ELECTRON when set, otherwise the pinned official release that
# fetch.py downloads. pcsvr, the PC service the UI expects, is the Linux
# build in this directory.

set -eu

ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
FETCH=$ROOT/../pack/fetch.py
# Both are made absolute: the launcher later runs from the profile.
mkdir -p "${TIINY_DATA_DIR:-$ROOT/data}" "${TIINY_APP_DIR:-$ROOT/app}"
TIINY_DATA_DIR=$(CDPATH='' cd -- "${TIINY_DATA_DIR:-$ROOT/data}" && pwd)
TIINY_APP_DIR=$(CDPATH='' cd -- "${TIINY_APP_DIR:-$ROOT/app}" && pwd)
export TIINY_DATA_DIR

for tool in python3 7z; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "tiinyos: $tool is required" >&2
    exit 1
  fi
done
# pcsvr.new is used while an older pcsvr is still mapped. The kernel
# refuses to overwrite a running binary.
PCSVR=$ROOT/pcsvr
if [ -x "$ROOT/pcsvr.new" ]; then
  PCSVR=$ROOT/pcsvr.new
fi
if [ ! -x "$PCSVR" ]; then
  echo "tiinyos: $ROOT/pcsvr is missing; build it from pcsvr/" >&2
  exit 1
fi

APP=$(python3 "$FETCH" app "$TIINY_APP_DIR")
ELECTRON=${TIINY_ELECTRON:-$(python3 "$FETCH" electron "$TIINY_APP_DIR")}
if [ ! -x "$ELECTRON" ]; then
  echo "tiinyos: electron not found at $ELECTRON" >&2
  exit 1
fi

# Desktop entry: app menu, icon, Wayland app id (CHROME_DESKTOP), and
# the tiinyai:// links the app handles (adding a model provider from a
# web page, the tray panel). Written with the real XDG directories,
# before the private ones below replace them. TIINY_LAUNCH is the
# command that starts this launcher (dist/tiinyos sets it to itself).
LAUNCH=${TIINY_LAUNCH:-$ROOT/tiinyos.sh}
export TIINY_LAUNCH="$LAUNCH"
APPLICATIONS=${XDG_DATA_HOME:-$HOME/.local/share}/applications
DESKTOP_ID=tiinyos.desktop
export TIINY_PROTOCOL_REGISTERED=0
case "$LAUNCH$TIINY_APP_DIR" in
  *[\"\`\$\\]*)
    echo "tiinyos: path has a character a desktop entry cannot quote; no desktop entry" >&2
    ;;
  *)
    ENTRY="[Desktop Entry]
Type=Application
Name=TiinyOS
Comment=Tiiny AI desktop app
Exec=\"$LAUNCH\" %u
Icon=$TIINY_APP_DIR/current/icon.png
Terminal=false
Categories=Utility;
MimeType=x-scheme-handler/tiinyai;
StartupWMClass=tiinyos"
    if [ "$(cat "$APPLICATIONS/$DESKTOP_ID" 2>/dev/null)" != "$ENTRY" ]; then
      mkdir -p "$APPLICATIONS"
      printf '%s\n' "$ENTRY" >"$APPLICATIONS/$DESKTOP_ID"
    fi
    if command -v xdg-mime >/dev/null 2>&1; then
      if [ "$(xdg-mime query default x-scheme-handler/tiinyai 2>/dev/null)" != "$DESKTOP_ID" ]; then
        xdg-mime default "$DESKTOP_ID" x-scheme-handler/tiinyai 2>/dev/null || true
      fi
      if [ "$(xdg-mime query default x-scheme-handler/tiinyai 2>/dev/null)" = "$DESKTOP_ID" ]; then
        TIINY_PROTOCOL_REGISTERED=1
      fi
    fi
    export CHROME_DESKTOP=$DESKTOP_ID
    ;;
esac

export ELECTRON_RUN_AS_NODE=
export TIINY_ASAR="$APP/app.asar"
# The bundle loads .env from the working directory before anything
# else, so a .env wherever the launcher was started would reach the
# app. Run from the profile, whose .env is empty.
cd "$TIINY_DATA_DIR"
[ -e .env ] || : >.env
export DOTENV_CONFIG_QUIET=true
mkdir -p "$TIINY_DATA_DIR/config" "$TIINY_DATA_DIR/share" "$TIINY_DATA_DIR/state" "$TIINY_DATA_DIR/cache"
# electron-store and Chromium both follow XDG and ignore app.setPath
# for at least one directory (tiiny-ai under XDG_CONFIG_HOME).
export XDG_CONFIG_HOME="$TIINY_DATA_DIR/config"
export XDG_DATA_HOME="$TIINY_DATA_DIR/share"
export XDG_STATE_HOME="$TIINY_DATA_DIR/state"
export XDG_CACHE_HOME="$TIINY_DATA_DIR/cache"
# A Wayland desktop session sets XDG_SESSION_TYPE or WAYLAND_DISPLAY.
# Electron has to be told on the command line. X11 stays the fallback
# for an X session and for a session that names neither.
OZONE=x11
if [ "${XDG_SESSION_TYPE:-}" = "wayland" ] || [ -n "${WAYLAND_DISPLAY:-}" ]; then
  OZONE=wayland
fi
export ELECTRON_OZONE_PLATFORM_HINT="$OZONE"
# Chromium creates shared-memory files in TMPDIR when
# --disable-dev-shm-usage is set, so sessions where /dev/shm is not
# writable still work. Chromium checks the sticky bit on that directory.
SHM=${TIINY_SHM_DIR:-$TIINY_DATA_DIR/shm}
mkdir -p "$SHM"
chmod 1777 "$SHM" 2>/dev/null || true
export TMPDIR="$SHM"

ELECTRON_FLAGS="--ozone-platform=$OZONE --no-sandbox --disable-dev-shm-usage --disable-gpu-sandbox"
PIDFILE=$TIINY_DATA_DIR/pcsvr.pids
PORTFILE=$TIINY_DATA_DIR/catalog.port
API_PORTFILE=$TIINY_DATA_DIR/pcsvr.port

# One launcher per profile owns the services. A second start leaves
# them alone and only runs Electron, which hands the start to the
# running window through its single-instance lock and exits. The lock
# fd is closed for every child, so only this shell holds it.
exec 9>"$TIINY_DATA_DIR/launcher.lock"
if ! flock -n 9; then
  TIINY_CATALOG_PORT=$(cat "$PORTFILE")
  export TIINY_CATALOG_PORT
  # shellcheck disable=SC2086 # ELECTRON_FLAGS is a fixed word list.
  exec "$ELECTRON" "$ROOT/main.js" $ELECTRON_FLAGS "$@" 9>&-
fi


# The pidfile holds the services of the last owner. A launcher killed
# with SIGKILL leaves them running; the UI will not start against a
# stale service on 60000, so they go. A PID is signalled only while
# /proc shows it running a pcsvr from this directory. cmdline, not exe:
# pcsvr with cap_net_bind_service (API names) is not dumpable, so its
# exe link is unreadable to the user.
stop_pids() {
  for pid in "$@"; do
    case "$(tr '\0' '\n' <"/proc/$pid/cmdline" 2>/dev/null | head -n 1)" in
      "$ROOT"/pcsvr*) kill "$pid" 2>/dev/null || true ;;
    esac
  done
}
if [ -f "$PIDFILE" ]; then
  # shellcheck disable=SC2046 # one PID per line.
  stop_pids $(cat "$PIDFILE")
  sleep 0.3
fi

# setsid: a terminal's Ctrl+C or hang-up goes to its process group.
# Electron may stay open in the tray after one; pcsvr must not die
# under it. The services stop when Electron exits.
rm -f "$API_PORTFILE"
setsid "$PCSVR" -f "$ROOT/pcsvr-api.yaml" -port-file "$API_PORTFILE" >"$TIINY_DATA_DIR/pcsvr.log" 2>&1 9>&- &
API_PID=$!
# Every Tiiny name goes to the catalog listener, which forwards it to
# the box (USB or Wi-Fi, whichever discovery found) and answers locally
# only where the box has no route. Port 80 needs privilege, so the
# catalog binds a free port and writes it to catalog.port; main.js maps
# the names onto it.
rm -f "$PORTFILE"
setsid "$PCSVR" -f "$ROOT/compat.yaml" -port-file "$PORTFILE" >"$TIINY_DATA_DIR/catalog.log" 2>&1 9>&- &
CATALOG_PID=$!
printf '%s\n%s\n' "$API_PID" "$CATALOG_PID" >"$PIDFILE"
stop_services() {
  stop_pids "$API_PID" "$CATALOG_PID"
  rm -f "$PIDFILE"
}
# Electron starts once both are listening, so the app's first PC
# service check finds 60000 answering. 60000 can be held for up to a
# minute (TIME_WAIT of a connection that used it as its local port);
# pcsvr retries for 65 s and exits if it still cannot bind.
wait_port() { # portfile pid log name
  waited=0
  until [ -s "$1" ]; do
    if ! kill -0 "$2" 2>/dev/null; then
      echo "tiinyos: $4 did not start: $(tail -n 1 "$3")" >&2
      stop_services
      exit 1
    fi
    waited=$((waited + 1))
    if [ "$waited" -eq 20 ]; then
      echo "tiinyos: waiting for $4: $(tail -n 1 "$3")" >&2
    fi
    sleep 0.1
  done
}
wait_port "$API_PORTFILE" "$API_PID" "$TIINY_DATA_DIR/pcsvr.log" "the PC service on 60000"
wait_port "$PORTFILE" "$CATALOG_PID" "$TIINY_DATA_DIR/catalog.log" "the catalog"
TIINY_CATALOG_PORT=$(cat "$PORTFILE")
export TIINY_CATALOG_PORT

# Electron's sandbox helper needs setuid root, which a user download
# does not have. --ozone-platform has to be on the command line: set
# from main.js it is too late. The script path has to be the first
# argument: Electron treats argv[1] as the app.
# shellcheck disable=SC2086 # ELECTRON_FLAGS is a fixed word list.
"$ELECTRON" "$ROOT/main.js" $ELECTRON_FLAGS "$@" 9>&- &
ELECTRON_PID=$!
# Ctrl+C, a hang-up, or a TERM to this shell asks Electron to quit;
# the services stop once it has.
trap 'kill -TERM "$ELECTRON_PID" 2>/dev/null || true' INT TERM HUP
status=0
while kill -0 "$ELECTRON_PID" 2>/dev/null; do
  wait "$ELECTRON_PID" && status=0 || status=$?
done

# A start made while this window was quitting can take over Electron's
# single-instance lock (SingletonLock -> host-PID in the profile). The
# services stay up while any Electron holds it, polled every second,
# with a short grace for a successor still starting.
singleton_alive() {
  pid=$(readlink "$TIINY_DATA_DIR/SingletonLock" 2>/dev/null) || return 1
  pid=${pid##*-}
  [ "$(readlink "/proc/$pid/exe" 2>/dev/null)" = "$(readlink -f "$ELECTRON")" ]
}
grace=3
while singleton_alive || [ "$grace" -gt 0 ]; do
  singleton_alive || grace=$((grace - 1))
  sleep 1
done
stop_services
exit "$status"
