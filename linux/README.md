# Launcher

`tiinyos.sh` installs or updates the official TiinyOS app with
`../pack/fetch.py`, starts the Linux `pcsvr` twice (the PC service on
60000 and the catalog on a free port), and runs Electron with
`main.js`. It also writes `~/.local/share/applications/tiinyos.desktop`
(app menu, icon, and the `tiinyai://` link handler) and runs from the
profile directory, whose `.env` is empty: the app loads `.env` from the
working directory.

One launcher per profile owns the services (`flock` on
`launcher.lock`). A second start only runs Electron, which hands the
start to the open window. pcsvr runs in its own session, so Ctrl+C or
closing the terminal cannot stop it under a window that stays open in
the tray; the launcher forwards those to Electron instead. The services
stop when no Electron holds the profile's `SingletonLock` any more,
which covers a start made while the previous window was quitting.

```sh
linux/tiinyos.sh
```

| Variable | Default | Meaning |
|---|---|---|
| `TIINY_DATA_DIR` | `linux/data` | Electron profile, `pcsvr.log`, `catalog.log`, `catalog.port`, `pcsvr.pids` |
| `TIINY_APP_DIR` | `linux/app` | Downloaded TiinyOS versions, `current` link, Electron |
| `TIINY_ELECTRON` | downloaded 40.10.6 | Electron binary to use instead |
| `TIINY_APP_UPDATE` | unset | `0` skips the update check when a version is installed |
| `TIINY_SHM_DIR` | `$TIINY_DATA_DIR/shm` | `TMPDIR` for Chromium shared memory |
| `TIINY_LAUNCH` | `linux/tiinyos.sh` | Command the desktop entry runs; `dist/tiinyos` sets itself |

`pcsvr.new` is used when it is executable, otherwise `pcsvr`. Build
with `go build -o ../linux/pcsvr.new .` in `pcsvr/`; the kernel refuses
to overwrite a running binary.

The window uses Wayland when `XDG_SESSION_TYPE` is `wayland` or
`WAYLAND_DISPLAY` is set, otherwise X11.

## Files

| File | What it does |
|---|---|
| `main.js` | Electron entry. Maps every Tiiny name to the catalog port: `--host-rules` for Chromium, `dns.lookup` plus a `net.connect` port rewrite for every main-process client (fetch, WebSocket, the bundled undici, `ws`, `http`). Merges repeated `enable-features`/`disable-features` (the bundle's second call dropped `GlobalShortcutsPortal` on Wayland), turns off `WaylandWpColorManagerV1`, takes the app version from the asar, answers `app.getPath("exe")` with `TIINY_LAUNCH` (the Linux "Auto start" entry then starts the launcher), marks the app packaged, and loads `out/main/index.js`. |
| `setup.html` | The setup prompt in two modes. `usb`: `main.js` polls `GET 127.0.0.1:60000/pcsvr/usb-link` and opens it once per attempt for a link in `needs_admin` (a new attempt starts when a link appears or loses its address); "Set up USB" calls `POST /pcsvr/usb-link/setup?name=<link>`. `api`: with a box connected and `GET /pcsvr/api-names` in `needs_admin` (mode `api`) or `needs_update` (mode `api-update`: the hosts block points at an address the box no longer has), it opens once per run; "Not now" skips that run (the API Key page offers it again), "Don't ask again" is remembered in `api-names-dismissed` in the profile (delete the file to be asked again). "Set up API names" calls `POST /pcsvr/api-names/setup`. Both raise the polkit password prompt (pkexec). |
| `integrations/` | Linux scripts for Agent Store integrations, added to their manifests by `main.js` (KiloCode: editor search, detect, preflight, install, open, uninstall, with VSCodium added). |
| `preload.js` | Makes Windows-only calls (powershell, reg, schtasks, and others) fail the way the bundle already handles. |
| `pcsvr-api.yaml` | PC service: port 60000, DNS 60053, discovery, SSH forward 60022, and `USBLink` (gives the box's USB link its DHCP address: NetworkManager profile, else `pkexec pcsvr -usb-dhcp <link>`). |
| `compat.yaml` | Catalog: port 0 (free port), discovery on, no DNS or SSH. |
| `tiiny-usb.network` | systemd-networkd DHCP config for that link (`Property=ID_VENDOR=Tiiny`), for hosts without NetworkManager. |

## Testing without the box

Run the app under Xvfb with DevTools on, then probe from the renderer:

```sh
Xvfb :99 -screen 0 1600x1000x24 -nolisten tcp &
env -u WAYLAND_DISPLAY XDG_SESSION_TYPE=x11 DISPLAY=:99 linux/tiinyos.sh --remote-debugging-port=9222
```

With no box, `fetch("http://api.tiiny/v1/models")` in the renderer
returns 502 `no device` from pcsvr, which shows the name reached pcsvr.

## Log lines that remain

- `StartTransientUnit ... UnitExists`: Chromium asks systemd for the
  same scope twice; the first call succeeds (the journal shows
  `Started app-org.chromium.Chromium-<pid>.scope`).
- `LoggerService not yet initialized`, `Activated startup remained
  hidden`, `Temp window is still loading`: the app's own startup
  messages. The last appears while a hidden window parses the renderer
  bundle and stops once it has loaded.
- `/connect probe failed`, `502`, `no device`: no box is reachable;
  pcsvr answers device names with 502 until discovery finds one. With
  the box present, one `/connect probe failed` (`not_auth`) at start is
  followed by a normal connection.
- `PC timezone update confirmation was not received`: the box's NPU
  socket (`p8800.api.tiiny/api/v1/npu/status`) streams status but does
  not acknowledge `pc_timezone_update`, also when spoken to directly.
