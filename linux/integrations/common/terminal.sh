# Sourced by the CLI integrations' open.sh. run_in_terminal starts the
# command given as arguments in a new terminal window: $TERMINAL when
# set, else the first of the common Linux terminals on PATH. Each takes
# the command as separate arguments, so nothing is re-quoted.
run_in_terminal() {
  unset ELECTRON_RUN_AS_NODE
  for term in ${TERMINAL:-} x-terminal-emulator konsole gnome-terminal kgx xfce4-terminal kitty alacritty wezterm foot ptyxis tilix xterm; do
    command -v "$term" >/dev/null 2>&1 || continue
    case $(basename "$term") in
      gnome-terminal|kgx|ptyxis|tilix) setsid "$term" -- "$@" >/dev/null 2>&1 & ;;
      kitty|foot) setsid "$term" "$@" >/dev/null 2>&1 & ;;
      wezterm) setsid "$term" start -- "$@" >/dev/null 2>&1 & ;;
      *) setsid "$term" -e "$@" >/dev/null 2>&1 & ;;
    esac
    return 0
  done
  echo 'No terminal found; set TERMINAL to your terminal program.' >&2
  return 1
}
