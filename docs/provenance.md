# Provenance

## Publication layout

The gitignore excludes the vendor installers, the downloaded app and
Electron (`linux/app/`), Electron profile data, `dist/`, `.scratch/`,
and `repackage/payload.zip`. `dist/tiinyos` and `payload.zip` hold this
project's files only; each user's launcher downloads the official
TiinyOS installer from Tiiny. The connect stub returns a local user with an
empty email. Tests use the placeholder device id `TNYM000000000000000`.

## 2026-11-09 (KiloCode from the desktop app, 21:12)

On the owner's KDE desktop (TiinyOS restarted on the build with the
port 80 removal, DevTools on loopback for the click, then restarted
without): Agent Store, KiloCode, Open. The bundle's `linux/open.sh` was
the new one; it started `codium --open-url
vscodium:extension/kilocode.kilo-code` (KiloCode is installed in
VSCodium; the earlier version opened Cursor, the first editor found);
configure rewrote `~/.config/kilo/kilo.jsonc` at 21:12:25. With Kilo's
own key file and provider settings, `POST
http://openai.api.tiiny/v1/chat/completions` model `default` answered
"kilo ok" from `Qwen/Qwen3.6-35B-A3B` (`finish_reason` stop; a 20-token
limit had returned empty content, all of it spent on reasoning).

## 2026-11-09 (API names: Not now and Don't ask again, 21:15)

The owner accepted "Update API names" at 21:10:53: `/etc/hosts` holds
`172.29.41.121 openai.api.tiiny anthropic.api.tiiny ollama.api.tiiny`,
all three answered 401 from the box, and nothing listened on port 80.
The quit before it left no pcsvr running. Asked by the owner, the API
names prompt now has "Not now" (that run only; the API Key page offers
it again) and "Don't ask again" (remembered in `api-names-dismissed`,
also honoured by the API Key page). `vnu` clean; screenshot updated.

## 2026-11-09 (API names without port 80, 21:15)

The owner asked not to use port 80 after a restart left "address
already in use": both pcsvr processes (API and catalog) run the same
binary, so with `cap_net_bind_service` on it the catalog instance bound
127.0.0.1:80 first (`/proc/net/tcp` inode owned by uid 1000, held by
the non-dumpable catalog pcsvr) and the API instance's bind failed and
was reported as a missing capability. Removed the port 80 listener,
the capability and the re-exec. The `/etc/hosts` block now points the
names at the box's own address (the box answers them on its port 80:
`curl -H 'Host: openai.api.tiiny' http://172.29.41.121/v1/models` gave
401 earlier); pcsvr compares it with discovery every 2 s and reports
`needs_update` when the box is elsewhere; only the instance with
`APINames.Enabled` (`linux/pcsvr-api.yaml`) does this. The owner also
asked about gvproxy: it gives VMs a user-mode network and does not
change name resolution for host programs, so it was not used.
`TestAPINamesFor`, `TestBlockAddress`, `TestWithHostsBlock` pass; in
`unshare -rm`, `pcsvr -api-names 172.29.41.121` replaced a 127.0.0.1
block and `getent hosts openai.api.tiiny` gave 172.29.41.121, and
`-api-names '1.2.3.4; rm -rf /'` was refused.

## 2026-11-09 (KiloCode in VSCodium, 21:10)

The owner confirmed Tiiny Music works on the desktop and asked for the
KiloCode plugin in their VSCodium. The bundle's scripts search only VS
Code, VS Code Insiders and Cursor. Added Linux `find-editor.sh`,
`detect.sh`, `preflight.sh`, `install.sh`, `open.sh` and `uninstall.sh`
under `linux/integrations/KiloCode-Tiiny/` that also cover `codium` and
`~/.vscode-oss/extensions`; `main.js` copies the folder into the bundle
as `linux/`. With a throwaway HOME, detect exited 1 (empty), 0
(installed), 1 (listed in `.obsolete`). `codium --install-extension
kilocode.kilo-code` installed v7.8.27 in the owner's VSCodium. After
the rebuild and restart the bundle held the new `linux/` scripts and
its `detect.sh` exited 0. The API names stayed `ready` through that
update with `cap_net_bind_service` still on the unchanged pcsvr.

## 2026-11-09 (KiloCode extension installed, 21:00)

Installed the real extension into a throwaway HOME:
`codium --extensions-dir <HOME>/.vscode/extensions --install-extension
kilocode.kilo-code` installed `kilocode.kilo-code-7.8.27-linux-x64`,
and the bundle's `extension/detect.sh` then exited 0. Cursor's AppImage
launcher ignored `--install-extension` and opened its GUI instead, so
Cursor was not used for this. The owner's own editors were not touched.

## 2026-11-09 (KiloCode scripts on Linux, 20:55)

Ran the KiloCode bundle's scripts under `/bin/sh` with a throwaway HOME
(the owner's editors untouched): `extension/detect.sh` exited 1, then 0
once `~/.cursor/extensions/kilocode.kilo-code-4.122.1` existed;
`extension/configure.sh` (TiinyOS's Electron as Node) exited 0 and wrote
`~/.config/kilo/kilo.jsonc` with a `tiiny` provider at
`http://openai.api.tiiny/v1`, the name the API-names setup resolves.
The extension install inside an editor was not done.

## 2026-11-09 (API names kept across restarts, 20:52)

The owner accepted the "Allow API names again" prompt at 20:51:57
(`getcap`: `cap_net_bind_service=ep`; the three names answered 401
from the box). Quitting TiinyOS (TERM to Electron) then left no pcsvr
running, so `stop_pids` now stops a pcsvr that has the capability.
Starting `dist/tiinyos` again reported `ready` at once, with no prompt,
and `openai.api.tiiny` answered 401.

## 2026-11-09 (API names prompt after a pcsvr rebuild, 20:55)

The owner reported being asked again after the previous push. Cause:
that build changed pcsvr, and a rewritten file has no capability; the
`/etc/hosts` block was intact. pcsvr now reports `needs_capability`
for that case and the prompt says the names are still set up and only
the permission is asked again (`setup.html?mode=api-update`, `vnu`
clean, screenshot checked). Accepting runs the same pkexec helper.

## 2026-11-09 (API names: /etc/hosts first, capability kept, 20:50)

Asked by the repository owner: check `/etc/hosts` before asking. pcsvr
now reports `external` and never prompts when `/etc/hosts` lists all
three names outside its block (`TestNamesInHosts`; in `unshare -rnm`
with them mapped to 172.29.41.121, `/pcsvr/api-names` said `external`
and nothing listened on port 80). The self-extractor no longer deletes
the launcher tree on update: it rewrites only changed files (by
temporary file and rename) and removes files the payload dropped, so an
unchanged pcsvr keeps `cap_net_bind_service` (`TestExtractKeepsUnchangedFiles`:
same inode for an unchanged file). Headless Chromium on
`tiinymusic.agent.tiiny.local` showed Qwen3.6-35B-A3B selected once the
owner had loaded it; the "Select an LLM model" toast was gone.

## 2026-11-09 (KiloCode integration, launcher stop fix, 20:40 to 20:47)

The repository owner asked to make the agent integrations work and
chose MIT for the license. The app's integration runner picks
`manifest.scripts[action][process.platform]` (`J0` in
`out/main/index.js`) and runs it with `/bin/sh`; the bundle manifests
(`~/.tiinyai/desktop-agents/<app>/bundle/manifest.json`) name `darwin`
and `win32` only, hence the minutely "Manifest does not provide
detect" warning. `linux/main.js` now adds `linux` entries when the app
reads such a manifest (the darwin `.sh`, or the launcher's
`linux/integrations/<app>/<name>`). Under Xvfb with the box, the Agent
Store listed KiloCode as the only desktop integration; it showed
"Open"; clicking it started `cursor --open-url
cursor:extension/kilocode.kilo-code`; the bundle got `linux/open.sh`;
the last detect warning was 20:41:37, from the instance before. The
extension install inside Cursor was not done.

Found while restarting: quitting left the API pcsvr (with
`cap_net_bind_service`) running, holding 60000. A process with file
capabilities is not dumpable, so `readlink /proc/<pid>/exe` failed and
`stop_pids` skipped it. `stop_pids` now matches `/proc/<pid>/cmdline`.
A quit of the next (capability-less) instance stopped both services.

## 2026-11-09 (API names on the desktop, 20:33)

The repository owner accepted the API-names prompt and the pkexec password
prompt on the KDE desktop. pcsvr logged "configured, restarting" and
"listening on 127.0.0.1:80" at 20:33:29; `getcap` shows
`cap_net_bind_service=ep`; `getent hosts openai.api.tiiny` gives
127.0.0.1. `curl` from a shell: `openai.api.tiiny/v1/models`,
`anthropic.api.tiiny/v1/messages` and `ollama.api.tiiny/api/tags`
answered 401 (the box's missing-token reply), `Host: example.com` on
port 80 answered 404. The box stayed listed (`device_count` 1) and the
app logged no new warning or error through 20:34 apart from the box's
KiloCode manifest warning. The restart path was also run in
`unshare -rnm` with a stand-in pkexec (`.scratch/apirestart.sh`): same
PID after the re-exec, state `ready`.

## 2026-11-09 (API names for other programs, 20:45)

Asked by the repository owner ("do the api names ... just askpass").
The app's API Key page (`n={openai:"http://openai.api.tiiny/v1",...}` in
`app.asar`) and the agent integrations' `release.json` files hand out
`openai`, `anthropic` and `ollama.api.tiiny`. The box answers all three
on port 80 by Host (`curl -H 'Host: openai.api.tiiny'
http://172.29.41.121/v1/models` returned 401, missing token). Added
`pcsvr -api-names` (pkexec): a marked `/etc/hosts` block to 127.0.0.1
and `cap_net_bind_service` on the pcsvr executable itself, then a
re-exec; pcsvr serves only those three names on 127.0.0.1:80 and routes
them to the box. `linux/usb-setup.html` became `linux/setup.html` with a
`usb` and an `api` mode. Verified in `unshare -rnm`
(`.scratch/apitest.sh`): the helper wrote the block, `/pcsvr/api-names`
said `ready`, `openai.api.tiiny` reached the proxy (502, no box in the
namespace), another Host on port 80 got 404. `TestWithHostsBlock`,
`TestNetBindCapability` and a routing case were added; `go test ./...`
passed. The pkexec prompt and re-exec on the desktop are not yet
exercised. Removed `pcsvr/pcsvr-api.yaml` (an unused near-copy of
Tiiny's macOS config) and the unread `ConnectPage` section of
`linux/pcsvr-api.yaml`. The README now says the project is unofficial.

## 2026-11-09 (publication cleanup, 20:30)

Asked by the repository owner before pushing to GitHub. Removed
`pcsvr/swagger_ui.html` and `pcsvr/connect_page.html` with their routes
(`GET /docs/swagger-ui`, `GET /connect-page`): line 12 of each file was
found verbatim in Tiiny's `tiiny_svr` binary (`rg -a -c -F`), so both were
Tiiny content. The app never requests either path (`rg -a` over
`app.asar` finds only the bundled `swagger-ui-dist` package). Removed
`docs/screenshot.png` (it showed Tiiny's welcome photo). Replaced the
box serial in tests and docs with a placeholder and dropped the owner's
account name, home path and LAN address from the docs. pcsvr no longer
logs the expected 404 on aliased routes (`api.tiiny` Wi-Fi status every
15 s); the alias target still logs its own failures. Added websocket
session logging: after the 20:21 restart the three startup sockets
(resource usage, running models, authd heartbeat) each closed once from
the box side within 8 s, then no websocket closed through 20:23.
`go test ./...` in `pcsvr/` passed.

## 2026-11-09 (physical power cycle, 20:04)

The repository owner power-cycled the box with TiinyOS (`dist/tiinyos`)
open on the desktop. Kernel: USB disconnect 20:04:51, reconnect
20:05:06. device.json was unreachable on USB and Wi-Fi from 20:04:59 to
20:05:08; at 20:05:08 `/v1/data/state` was `unlocked`, service `ok`
(box OS 0.1.39, `key_arch: vault`; no password needed). pcsvr logged
"NetworkManager DHCP profile up" for the new USB link at 20:05:13 and
selected the box over USB. Box services answered 502 (update versions,
chat history, NPU status) until about 20:05:27; after that pcsvr
logged no box failures except the Wi-Fi status alias. The box exposes
no uptime; `GET hardware-upgrade /api/v2/device` `server_instance`
changed from `8e897e0c-3137-443b-9ef0-7ef1738418a5` (17:12) to
`692fd2aa-dfd2-4606-96e7-997e36b0e660`, so the update service was
restarted, and the USB gadget re-enumerated. A full reboot is likely
but not proven.

## 2026-11-09 (Model Store import icon, 17:30)

The unlabeled icon at the top right of the Model Store, clicked over
DevTools on the KDE Wayland desktop, opened the app's own "Import
Models" dialog (requirements, supported models, Hugging Face URL, Next
disabled while empty); `busctl --user monitor
org.freedesktop.portal.Desktop` showed no file-chooser call. No import
was started. A 30 s DevTools timeout at that step under Xvfb was not
reproduced on the desktop; its cause is not known.

## 2026-11-09 (box OS update to 0.1.39, 16:16 to 17:15)

The repository owner approved updating the box from TiinyOS's own
update screen (Settings, TiinyOS) under Xvfb.

- The screen's control resumed an OS download already paused on the box
  (`POST /api/upgrade/resume_prepare`, 200). The box stepped through
  `upgrade_path` 0.1.34 to 0.1.39, reached `PREPARED`
  (`MULTI_VERSION_PREPARE_COMPLETED`) at about 16:51; "Click to update"
  opened "Update Detail" ("Upgrading. Do not shut down or cut power."),
  and `/api/upgrade/status` reported `SUCCESS` for 0.1.39 at 17:04:34.
- After the reboot `/v1/data/state` reported `data_state: locked`,
  `key_arch: password-wrapped`, `credential_state:
  password_or_auth_key_required`; `unlock_with_auth_key` with the saved
  key returned `status: ok` and the box stayed locked, so the app's
  password screen was the expected path. The owner logged in on the
  desktop; the box reported `unlocked` at 17:11:27.
- Packager end to end: `rm -rf ~/.local/share/tiinyos/{app,launcher}`
  (the profile kept), `dist/tiinyos` on the KDE Wayland desktop: CDN
  installer download, extraction, sharp-wasm32, Electron 40.10.6
  download, pcsvr and window in 30 s (17:10:14 to 17:10:44).
- On 0.1.39: `GET api.tiiny/api/v1/models/<id>/update` 200 (was 405);
  `agent-services /api/services/apps/metadata` 200 (was 404);
  `hardware-upgrade /api/v2/versions` `code 0`, current and latest
  0.1.39 (one transient 409); legacy `/api/upgrade/check` 404. The only
  box failure the app hit after login was `GET api.tiiny
  /api/v1/sys/wifi/connection_status` 404 (four times); the box serves it
  on `wifi.api.tiiny`. pcsvr now retries that 404 there (`boxAlias`,
  `TestProxyRouting`).

## 2026-11-09 (UI sweep, 15:40)

The repository owner asked for every screen and button to be checked.
`.scratch/crawl.py` clicked every visible control on each main-window
route and in the Settings window over DevTools (skipping delete, remove,
sign out, shutdown, reboot, reset, uninstall, downloads, upgrades and
send), recording renderer errors, failed requests and pcsvr box errors.
The renderer logged no errors or failed requests; every finding came
from pcsvr's new box log (`catalog.log`).

- Agent Store empty: the list is `agent-services.api.tiiny
  /api/services/apps/metadata`, 404 on box OS 0.1.33; pcsvr answered it
  with a one-app stub. pcsvr now answers that 404 with the box's
  `api.tiiny/api/v1/apps` (same `{app, categories, carousel}`); the
  store showed KiloCode, n8n, Open WebUI, Odysseus, OpenHands, Tiiny
  Music, SillyTavern, AutoGPT. The stub was removed.
- Box OS update state: `hardware-upgrade` stubs answered
  `/api/v2/versions` (box 404) with made-up versions and `/api/upgrade/*`
  with made-up DOWNLOADING/PREPARING states. Removed; `hardware-upgrade`
  is box-only. The box's `POST /api/upgrade/check` (current 0.1.33,
  available 0.1.39) took 6.9 to 13.6 s directly and through pcsvr; the
  app aborts after 5 s (25 aborts logged). pcsvr now finishes that fetch
  and serves the box's answer for 60 s (`TestSlowBoxAnswerIsCached`);
  the next run logged one 7.5 s fetch and the sidebar showed the update
  badge. The fake-OK `POST /v1/chat/turns/insert` stub was removed (the
  box serves it).
- Model update check: `GET api.tiiny/api/v1/models/<id>/update` gets 405
  (`allow: DELETE`) from the box directly; box OS 0.1.33 has no such
  check. Not a port issue; box OS 0.1.39 is offered.
- Auto start wrote `~/.config/autostart/tiiny-ai.desktop` with
  `Exec=<electron binary>` (the sweep created one; it was deleted).
  `main.js` answers `app.getPath("exe")` with `TIINY_LAUNCH`; toggling
  then wrote `Exec=~/tiiny/linux/tiinyos.sh`, and off
  removed the file.
- Settings pages with box data: user profile, feedback (empty submit
  refused), My Device (service version 0.1.33), storage, connector,
  Wi-Fi (scan list), networks (usb0, wlan0), API key, privacy policy;
  the floating button and auto start toggles. Tray panel: model list,
  load state, unload. `openai`, `anthropic` and `ollama.api.tiiny`
  appear only as text on the API Key page; they do not resolve for
  other programs on Linux.

## 2026-11-09 (USB without a network daemon, 15:10)

The repository owner asked for USB to work with no network daemon, with
a graphical password prompt as the fallback, announced first by a screen
in the app's style.

- `pcsvr/usblink.go`: pcsvr (PC service process, `USBLink: Enabled`)
  watches for links whose USB device reports manufacturer `Tiiny`. A
  link without IPv4 gets the NetworkManager DHCP profile when
  NetworkManager runs; otherwise it is reported as `needs_admin` on
  `GET /pcsvr/usb-link`. `POST /pcsvr/usb-link/setup?name=` runs
  `pkexec <pcsvr> -usb-dhcp <link>`; the root side refuses non-Tiiny
  links, raises the link with `SIOCSIFFLAGS`, takes a DHCP lease over an
  `AF_PACKET` socket, and sets the address with `SIOCSIFADDR` and
  `SIOCSIFNETMASK`. `linux/usb-link.py` was removed.
- Tests: `TestDHCPMessageAndReply`; `FuzzParseDHCPReply` (20 s, about
  6 million inputs, no failure); `.scratch/dhcptest.sh` in a user and
  network namespace against dnsmasq (link DOWN to UP, lease
  `172.29.41.122/30`, no default route, `lo` refused);
  `.scratch/ndtest.sh` ran systemd-networkd 262 with
  `linux/tiiny-usb.network` against dnsmasq: matched by
  `Property=ID_VENDOR=Tiiny`, lease taken, gateway offered and not
  installed.
- Real box: the owner approved `pkexec linux/pcsvr.new -usb-dhcp
  enp19s0u5u2u2`; output `172.29.41.122/30 from 172.29.41.121`, only the
  /30 route. With the app started on a PATH without `nmcli`, pcsvr
  logged "NetworkManager is not installed; needs admin setup", the
  "Connect Tiiny over USB" window (`linux/usb-setup.html`, `vnu` clean)
  opened by itself, "Set up USB" showed "Setting up USB" while polkit
  asked, the link got `172.29.41.122/30` about 4 s later, the window
  closed, and pcsvr selected the box over USB. With NetworkManager, a
  `nmcli device disconnect` while the app ran was repaired by pcsvr's
  profile within 15 s.
- Unlock cause: `_Ot` in `out/main/index.js` posts
  `/api/v1/account/unlock_with_auth_key` and polls `/v1/data/state`
  only after that request did not fail or report `locked`; the 14:56:05
  `[LocalDataUnlock] main data state polling failed` line is from the
  polling stage, so the app's request unlocked the box. Both endpoints
  answer on the box (`status: ok`, `boot_state: ready`).
- Wayland: `kwin_wayland --virtual` (private D-Bus session) with the
  app on `--ozone-platform=wayland`: no color-manager errors; child
  processes carry `--enable-features=...GlobalShortcutsPortal...` and
  `--disable-features=...WaylandWpColorManagerV1`; chat answered
  "wayland ok" (28 tokens/s).

## 2026-11-09 (box back online, 14:32)

The repository owner powered the box on and asked for USB without
NetworkManager and for every function to be tested under Xvfb.

- DHCP. The earlier entry's "the gadget runs no DHCP server" was wrong.
  A temporary NetworkManager profile with `ipv4.method auto` on
  `enp19s0u5u2u2` received `172.29.41.122/30` as a `dynamic` lease.
  The USB device reports `manufacturer` `Tiiny` and udev sets
  `ID_VENDOR=Tiiny` on the network device. `linux/usb-link.py` now
  finds the link by that, needs no discovery, and adds a NetworkManager
  DHCP profile (`ipv4.never-default`, `ipv4.ignore-auto-dns`) only when
  the link has no address; tested with the link configured, with
  NetworkManager (it converted the earlier static profile; only the
  /30 route was added), and with `nmcli` hidden (prints the
  systemd-networkd instruction). `linux/tiiny-usb.network` matches
  `Property=ID_VENDOR=Tiiny`; it was not installed here.
- No root at all is not possible: the link comes up DOWN without a
  network manager, and raising it or reading frames needs
  `CAP_NET_ADMIN` or `CAP_NET_RAW`.
- Lock state. For 20 minutes after power-up every box name but
  `auth.api.tiiny` returned nginx 502 and port 22 was closed. The box's
  `POST /api/v1/connect` answered `data_state: locked`, while pcsvr's
  catalog answered `auth.api.tiiny` itself with `data_state: unlocked`.
  `auth.api.tiiny` now goes to the box; the fake `/api/v1/connect`,
  `/api/v1/connect/ws` and `/api/v1/account/device_name` handlers were
  removed (the box serves all three: 200, 101, 401). After the app was
  restarted with that change it logged `dataState: locked`, and within
  about a minute the box reported `unlocked` and every name returned
  200. Whether the app's saved key unlocked it or the box finished on
  its own is not shown by the logs.
- Routing. Every Tiiny name now goes to the box; the catalog answers a
  box 404 on a path it implements, and the store and upgrade names when
  no box is present (`TestProxyRouting`). With the box unlocked the
  page sweep (`.scratch/routes.py`: home, app store, model store,
  dashboard, vault, history) logged no renderer errors; the model store
  listed the box's models and the dashboard showed NPU 0 %, SoC 3.3 %,
  RAM 14.1 %.
- Chat, real box, under Xvfb with `xdotool` input: over USB the box's
  Qwen3.6-35B-A3B answered "linux port ok" (27 tokens/s) and the tab
  got a generated title; with the NetworkManager profile taken down,
  pcsvr selected `192.168.1.9` within 2 s and the same app run got
  "wifi ok"; with USB back it selected `172.29.41.121` within 2 s.
  `dist/tiinyos` (rebuilt 15:01) on the owner's profile answered "final
  build ok" (28 tokens/s) over USB.
- `PC timezone update confirmation was not received`: the box's
  `/api/v1/npu/status` socket, opened directly with subprotocols
  `bearer, <auth key>` (`.scratch/tzprobe.py`), sent `snapshot` and
  `update` frames and no reply to `pc_timezone_update`.
- The one `/connect probe failed` after restart (`not_auth`) did not
  repeat. The stored key returns `status: ok` from the box with and
  without a `pc_id`.

## 2026-11-09 (first user run of dist/tiinyos)

The repository owner ran `dist/tiinyos` on KDE Wayland and asked to fix
every problem in its output. Checked under Xvfb `:99`.

- Lost services. In that run both pcsvr processes were gone a minute
  after start (both logs end after their start lines, so they were
  signalled), while Electron kept running; an earlier launch ran from
  13:17:45 to 13:17:58. Reproduced here: a second start while the
  first window was quitting left an Electron holding the profile's
  `SingletonLock` with no launcher and no pcsvr, because the old
  owner stopped the services when its own Electron exited.
  `linux/tiinyos.sh` now takes `flock` on `launcher.lock` (a second
  start only runs Electron), runs pcsvr under `setsid`, forwards
  INT/TERM/HUP to Electron, stops only its own two PIDs, and keeps them
  while any Electron holds `SingletonLock`. Checked with
  `.scratch/launchtest.sh`, `.scratch/sigtest.sh` and
  `.scratch/racetest.sh`: second start keeps the same services; TERM to
  the process group quits in about 10 s and stops both; HUP stops all;
  a second start during quit keeps services for the survivor.
- `authd heartbeat websocket error`. The heartbeat uses the undici copy
  bundled in the app (`qM`), whose WebSocket dials without the patched
  `fetch`, so it reached port 80 ("Received network error or non-101
  status code."). `main.js` now rewrites the port in `net.connect` and
  `net.createConnection` (and `http.Agent`); a probe under Electron
  40.10.6 got 200 from fetch, 502 from `http.get` on `api.tiiny`, and a
  `pong` frame over WebSocket. The `fetch` and `http.request` wrappers
  were removed.
- sharp warning. `@img/sharp-wasm32` 0.34.5 with `@emnapi/runtime`
  1.11.3 placed in `<version>/node_modules` (outside the asar, found by
  Node's upward module search) ran a sharp PNG encode under Electron
  40.10.6 (libvips 8.17.3). `pack/fetch.py` adds them with an npm sha512
  check; failure leaves the app on its existing fallback.
- `tiinyai://` registration returned `registered: false`: Electron ran
  `xdg-mime` with the launcher's private XDG directories and no desktop
  file. The launcher writes `tiinyos.desktop` and sets the
  `x-scheme-handler/tiinyai` default with the real ones; the log then
  shows `registered: true`.
- `.env`: the bundle reads `<cwd>/.env` first (`Itr` in
  `out/main/index.js`). The launcher runs from the profile with an
  empty `.env` and sets `DOTENV_CONFIG_QUIET=true`.
- Wayland: the bundle appends `enable-features` twice, so
  `GlobalShortcutsPortal` was dropped; `main.js` merges repeated
  feature switches. `WaylandWpColorManagerV1` (string present in the
  Electron binary) is disabled to stop the color-manager errors.
- `main.js` set the app version to `1.0.3`; it now reads
  `package.json` from the installed asar.
- A run of `dist/tiinyos` at 13:52:32 found 60000 taken; pcsvr's 30 s
  retry ran out (`bind: address already in use`) while `ss` showed
  `127.0.0.1:60000` in TIME-WAIT, and Electron had started anyway
  ("Bundled Linux PC Server resources not found"). The retry is now
  65 s (TIME_WAIT is 60 s), the PC service writes `pcsvr.port`, and the
  launcher starts Electron only after both services answer, or exits
  with pcsvr's last log line. The next run at 13:56 bound 60000 first
  and logged neither error.
- Not changed: the duplicate `StartTransientUnit` (the first call
  succeeds per the journal), the app's own startup warnings, and the
  no-device 502s; the box was still offline.

## 2026-11-09 (launcher rework)

Requested by the repository owner: get the Linux version fully working,
over USB and Wi-Fi, with a random catalog port, and with the single
executable downloading the official package instead of shipping it.
Checked under Xvfb `:99` (screenshots and DevTools on 9222).

- Download source. The bundle's own update check is
  `POST https://service.online.tiiny-svr.com/update/check_update`
  (`out/main/index.js`, function building `/update/check_update`). An
  anonymous request with `platform` `windows`, `arch` `amd64`,
  `now_version` `0.0.0` returned `newest_version` `1.0.3`,
  `total_bytes` 138851312 and
  `https://files.tiinycdn.com/update/blobs/tiinyos-1.0.3-windows-amd64.exe`;
  that file's size matches the local installer
  (sha256 `c37ccf6216b25b750167370d9e32db48439c0ab4f6fd683c97a5e55f87e0e9c2`).
  The `app-update.yml` feed `https://releases.tiiny-ai.com` does not
  resolve (also not via 1.1.1.1 or 8.8.8.8). `pack/fetch.py` uses the
  update check; `tests/test_fetch.py` covers install, update, pruning,
  a refused package, and offline fallback.
- sharp. The Windows 1.0.3 package has no `@img/sharp-linux-x64`; the
  tree in `.scratch/app` came from npm (0.34.5, libvips 1.2.4). Loaded
  under Electron 40.10.6 it segfaults in `g_object_unref` (gdb: system
  `libgobject-2.0.so.0` called from `sharp::OpenInput`), also with
  `LD_PRELOAD` of libvips. The only `require("sharp")` is in `OcrUtils`,
  which falls back to the original image. sharp is no longer added;
  `pack/asar_linux.py` was removed. The app log shows the sharp load
  failure and the app keeps running.
- Hand-patched `linux/app.asar`. Its `out/main/index.js` differed from
  the pristine 1.0.3 asar in three places: Entry window `show:!0`, an
  extra reveal on `dom-ready`, and `TIINY_PCSVR_DIR`. No script made
  them. The pristine asar shows the Entry window under Xvfb, so the
  copy was moved to `.scratch/app.asar.handpatched`.
- Names and ports. `--host-rules` (Chromium) maps every Tiiny name to
  `127.0.0.1:<catalog port>` and keeps the Host header; a websocket to
  `ws://p8800.api.tiiny/...` failed through a webRequest redirect and
  succeeded through `--host-rules`. The catalog binds port 0 and writes
  the port to `catalog.port` (`-port-file`). pcsvr forwards
  `api`, `kb.api`, `tts.api`, `connector.api` and `backup.managed`
  `.tiiny` to the address discovery reports, read per request. In a
  user and network namespace with a fake box answering discovery as
  Wi-Fi only (`.scratch/nstest.sh`), `api.tiiny` returned 502 before the
  box started and the box's reply 4 s after, in the same app run, and
  three SSE chunks arrived 500 ms apart. `linux/hosts.py`,
  `linux/hosts.js`, `repackage.sh`, `linux/nm-dnsmasq-tiiny.conf` and
  the hardcoded `172.29.41.121` were removed.
- Ports 60000, 60022 and 60053 are inside the ephemeral range
  `32768 60999` (`/proc/sys/net/ipv4/ip_local_port_range`); about 1950
  loopback sockets were in that range. One start lost 60022 and one
  lost 60000. pcsvr now retries those binds for 30 s
  (`TestListenRetryWaitsForPort`).
- USB address. The gadget's address 172.29.41.122/30 had been added
  with `sudo ip addr add` (journal, 09:20:25). `linux/usb-link.py` now
  adds a NetworkManager profile owned by the user and bound to the
  gadget MAC (`settings.modify.own` is `yes` here), else `sudo -n`.
  The box disconnected from USB at 12:29:44 and was not reachable on
  the LAN for the rest of the session, so the profile and a real-device
  chat were not exercised after this rework. The profile in
  `~/.local/share/tiinyos/data` shows a chat answered by the box at
  12:20 with the earlier build.
- Launchers. `linux/tiinyos.sh` no longer kills processes by pattern
  (its `ps | awk /pcsvr/` loop killed the invoking shell); it tracks its
  two pcsvr PIDs in `pcsvr.pids` and stops them when Electron exits.
  `dist/tiinyos` is 9.7 MB and only unpacks `linux/` and `pack/` files
  and execs `linux/tiinyos.sh`. A first run with an empty app directory
  downloaded the installer from the CDN and Electron 40.10.6 from
  GitHub (SHASUMS256 checked) and reached the Entry window.

## 2026-11-09

`pack/build.sh` extracts `app.asar` from a TiinyOS Windows NSIS
installer or a macOS pkg and packs it with the Linux `pcsvr` and the
system Electron runtime into `dist/tiinyos`. The macOS 1.0.3 pkg
(`510004aece8bd87c7a0566d498e3c081accc778b8d548b599e2c23f2bf7b2822`)
was extracted. `app.asar` was at
`TiinyOS.app/Contents/Resources/app.asar`, sha256
`42fbaf708be84aa49713083425c15547ed84af191b3c35b7feb9ecd11bada8de`.
`docs/api-tracker.json` was seeded from the Linux handlers and merged
with the paths named by that asar. A second merge kept implemented
status and did not mark Linux-only routes removed.

The official developer portal is `https://docs.dev.tiiny.ai/`. The
lookup from this network returned `Name or service not known`. Tiiny
describes a TiinySDK and OpenAI-compatible device APIs. `pcsvr` is
the companion PC service and is not part of that public device
reference.

## 2026-11-08

Ported TiinyOS 1.0.3 to Linux. The Windows NSIS installer contains an
Electron 37 app plus `TiinyPCService_Installer.exe`. That installer
contains `pcsvr.exe`, a Go 1.27.1 binary (`path pcsvr`, module
`github.com/zeromicro/go-zero`) whose config listens on 127.0.0.1:60000
and discovers the box with UDP 39217 / `GADGET_DISCOVER_V1`. The macOS
package ships the same service as Mach-O (`tiiny_svr`, `pcsvr`), which
cannot run here. The Electron main process already starts
`resources/pcsvr/linux/pcsvr -f pcsvr-api.yaml` on linux x64
(`checkAndInstallOnFirstLaunchLinux` in `out/main/index.js`) and reads
devices from `ws://127.0.0.1:60000/devices_list/ws`.

Wrote `pcsvr/` (DNS, discovery, device socket, SSH forward) and
`linux/tiinyos.sh`, which loads the repacked asar under Electron 40.
`go test` in `pcsvr/` passed. A live run of `linux/pcsvr` returned
version `1.0.3-linux`, NXDOMAIN for `api.tiiny.local` with no device,
and a `connection` websocket frame. The UI process reached
`StartupVersionReport` version 1.0.3, spawned `pcsvr` on 60000/60022/60053,
and the renderer logged linux/amd64. The Entry window stayed unmapped
(`xwininfo` `IsUnMapped`); a plain Electron `BrowserWindow` in the same
session also reported `visible=false`. No device was plugged in, so
discovery of a real box was not observed.
