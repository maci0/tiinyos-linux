# How it works

## Reaching the box

pcsvr discovers the box by its gadget protocol (UDP 39217, then HTTP
39218 `/device.json`) on every interface, every second, and prefers the
USB link over Wi-Fi. Electron sends every Tiiny name (`api.tiiny`,
`kb.api.tiiny`, and the rest) to a pcsvr listener on a free loopback
port. pcsvr forwards each request, under its own host name, to
whatever address discovery reports at that moment, so a box plugged in,
or moved to Wi-Fi, after the app started is used without a restart.
pcsvr answers locally only where this firmware has no route (a 404 for
a path it implements) or, for the store and upgrade names, when no box
is present. Sign-in and the box's lock state always come from the box,
so after a power cycle the app sees a locked box as locked. Streamed
chat replies pass through unbuffered.

Over USB the box is a network gadget with its own DHCP server. pcsvr
notices its link (USB manufacturer `Tiiny`), also when it is plugged in
while the app runs, and if the link has no address it takes the box's
lease itself: through a NetworkManager profile when NetworkManager runs
(no password). Otherwise TiinyOS shows a "Connect Tiiny over USB" screen
in the app's own style that says the link needs setting up and will ask
for the administrator password; "Set up USB" runs pcsvr once through
`pkexec`, whose system prompt takes the password, to bring the link up,
do the DHCP exchange and set the address. No DHCP client or network
daemon is needed. "Use Wi-Fi instead" or a cancelled prompt leaves USB
unused until the next plug-in; Wi-Fi needs none of this. Hosts that run
systemd-networkd can instead install `linux/tiiny-usb.network` once and never see the prompt.

## API names for other programs

Other programs reach the box's model APIs at the names the app's API Key
page and its agent integrations give out: `http://openai.api.tiiny/v1`,
`http://anthropic.api.tiiny` and `http://ollama.api.tiiny`. The box
answers those names itself on its port 80; Linux just does not resolve
them. pcsvr reads `/etc/hosts` first: if it already lists all three
names (your own entries, any address), TiinyOS leaves them alone and
never asks. Otherwise, once a box is connected, it offers to set them
up ("Not now" waits for the next start or a visit to the API Key page;
"Don't ask again" is final). One `pkexec` prompt writes a marked block
pointing the names at the box's current address. pcsvr checks the block
against discovery every 2 s; when the box turns up at another address
(USB is always 172.29.41.121, Wi-Fi is whatever the router gives it),
TiinyOS offers to update the block. Nothing listens on port 80 on
this computer. To undo: delete the `tiinyos api names` block from
`/etc/hosts`.

Without that setup, the box's own mDNS names work on hosts that resolve
`.local` (avahi with nss-mdns): `http://openai.api.tiiny.local/v1`,
`http://anthropic.api.tiiny.local`, `http://ollama.api.tiiny.local`.
The app's agent integrations (OpenCode, Goose, KiloCode and others)
write the bare `openai.api.tiiny` into those tools' settings, so they
need the setup above.


## Ports

The app hardcodes ports 60000, 60022 and 60053, which lie inside the
default Linux ephemeral port range. A busy machine can briefly hold one
for an outgoing connection, and a closed connection can keep it for the
60 s of TIME_WAIT. pcsvr retries for 65 s and the launcher starts the
app only once 60000 answers. To reserve them:

```sh
echo 'net.ipv4.ip_local_reserved_ports = 60000,60022,60053' | sudo tee /etc/sysctl.d/60-tiiny.conf
sudo sysctl --system
```

## Status

Checked with TiinyOS 1.0.3 and a real box, under Xvfb with mouse and
keyboard input: chat with streamed replies over USB and over Wi-Fi,
switching between them in the same session within 2 s; model store,
dashboard (live NPU, SoC and RAM), vault, history and agent store with
no renderer errors; the box's locked state after power-up reaching the
app; first-start download and update check; USB set up by the
NetworkManager profile, by the in-app "Connect Tiiny over USB" screen
and polkit prompt with no network daemon, and by the systemd-networkd
file (real networkd in a namespace). Wayland was checked under a
headless KWin, with chat working; not on a physical Wayland desktop.

Box software 0.1.33 lacks two routes app 1.0.3 calls (the model update
check and the agent store list; pcsvr answers the second with the box's
own app list) and its OS update check takes 7 to 14 s against the app's
5 s limit (pcsvr finishes it and serves the answer to the next poll).
Updating the box from Settings, TiinyOS fixes the first two: checked on
0.1.39. After a box OS update the box asks for the device password once.

OCR uses the app's built-in Tesseract (tesseract.js), its default on
Linux; the macOS and Windows system engine (`@napi-rs/system-ocr`) is
not registered there, and its load warning is harmless. Not ported: text
selection helpers and the Windows auto-update installer (the launcher
updates the app instead). Image resizing uses sharp's WebAssembly
build, fetched from npm, because the native Linux addon crashes
under Electron. x64 only.  A future TiinyOS release may call pcsvr
routes this port does not have yet. The launcher keeps the previous
version for that case: point `~/.local/share/tiinyos/app/current`
at the older directory and start with `TIINY_APP_UPDATE=0`, which
skips the update check.


## References

Tiiny's developer portal: <https://docs.dev.tiiny.ai/>. A public write-up
of the device API:
<https://github.com/thezolon/tiiny-studio/blob/main/docs/DEVICE-API.md>.

## Agent integrations

Desktop integrations from the Agent Store arrive as script bundles in
`~/.tiinyai/desktop-agents/<app>/bundle`; the app runs each script with
`/bin/sh`, picking it by `process.platform` when the manifest names one
per platform. The store metadata comes from the box
(`agent-services /api/services/apps/metadata`) and lists macOS and
Windows platforms only.

- pcsvr (`pcsvr/linuxstore.go`) adds a `linux-x86-64` platform for each
  tool with an official Linux build: Tiiny's macOS script bundle, with
  the tool download pointed at the pinned upstream release (OpenCode
  `opencode-linux-x64.tar.gz`, Pi `pi-linux-x64.tar.gz`, Goose
  `goose_<v>_amd64.deb`, all with size and sha256) or, for OpenClaw,
  its platform-neutral installer as is. An entry is added only while
  Tiiny's bundle is for the version the Linux build is pinned to.
- `linux/main.js` copies `linux/integrations/common/` and
  `linux/integrations/<app>/` into the bundle as `linux/` and points the
  manifest at those scripts where the macOS ones cannot run (terminal
  and browser launch, `.app` bundles); the rest of Tiiny's scripts and
  their `runtime.js` and `configure.js` run unchanged from the bundle,
  which every user downloads from Tiiny. Goose's `.deb` is unpacked
  with the bundled 7-Zip (`TIINY_7ZZ`) into the integration directory.
- Scripts and the tools they start get the user's own `XDG_*`
  directories (`TIINY_USER_XDG_*` from `tiinyos.sh`), not TiinyOS's
  private ones.

Cline (`desktop-v0.0.48` `.deb`, needs the system's WebKitGTK 4.1) uses
Tiiny's `runtime.cjs` for the provider settings
(`~/.cline/data/settings/providers.json`, provider `tiinyos`); the
macOS script that sets the app's chosen model writes WebKit storage under
`~/Library`, so `linux/integrations/cline/frontend.js` does the same in
WebKitGTK's `~/.local/share/bot.cline.app/localstorage/tauri_localhost_0.localstorage`
(`node:sqlite`; previous values kept and restored on uninstall). Hermes
Agent and Tiiny Meeting have no Linux build.

## Uninstalling integrations

Removing `~/.tiinyai` (README, Uninstall) removes each integration's
bundle and app. Left elsewhere:

| Integration | Files |
|---|---|
| KiloCode | the extension in your editor; `~/.config/kilo`, `~/.local/share/kilo`; `kilo-code.new.model.providerID`/`modelID` in the editor's `settings.json` |
| OpenCode | `~/.opencode`; the `hardware-box` provider in `~/.config/opencode/opencode.json` |
| Pi | the `hardware-box` provider in `~/.pi/agent` |
| OpenClaw | `~/.openclaw` |
| Goose | `~/.local/share/applications/tiiny-goose.desktop`; settings in `~/.config/goose` |
| Cline | `~/.local/share/applications/tiiny-cline.desktop`; `~/.cline`; `~/.local/share/bot.cline.app` |

The `tiinyai://` link handler is registered with `xdg-mime`; if
`xdg-mime query default x-scheme-handler/tiinyai` prints
`tiinyos.desktop`, remove that line from `~/.config/mimeapps.list`.
