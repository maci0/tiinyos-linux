# TiinyOS on Linux

Run Tiiny's own TiinyOS desktop app on Linux, with a Tiiny box on USB
or Wi-Fi. For Tiiny owners whose PC runs Linux.

Unofficial. Not affiliated with or endorsed by Tiiny AI; "Tiiny" and
"TiinyOS" are their names.

TiinyOS ships for Windows and macOS only: an Electron app plus a Go
service named `pcsvr` that the app needs. Wine cannot run that service.
This project runs the official app under Electron and replaces `pcsvr`
with a Linux service written from scratch to answer the same HTTP API
(`docs/pcsvr-api.md`, `docs/api-tracker.json`).

No Tiiny code is in this repository or in the built executable. On
first start, and whenever Tiiny publishes a newer release, the launcher
asks Tiiny's update service (the same one the app uses) for the newest
Windows x64 build, downloads it from Tiiny's CDN, and extracts the app.

## Install

You need `python3`, `7z` (p7zip), and Go 1.25 to build.

```sh
pack/build.sh
dist/tiinyos
```

`dist/tiinyos` is a 10 MB executable. It adds TiinyOS to the app menu
and handles `tiinyai://` links. The first start downloads about
140 MB from Tiiny and 110 MB of Electron from its GitHub release, under
`~/.local/share/tiinyos/app`. Later starts check for a newer TiinyOS
and keep the previous version if the update fails or the network is
down. The profile is in `~/.local/share/tiinyos/data`.

From the source tree instead:

```sh
(cd pcsvr && go build -o ../linux/pcsvr.new .)
linux/tiinyos.sh
```

That downloads into `linux/app` and keeps the profile in `linux/data`.
Set `TIINY_ELECTRON` to use an installed Electron.

## How it reaches the box

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

Other programs reach the box's model APIs at the names the app's API Key
page and its agent integrations give out: `http://openai.api.tiiny/v1`,
`http://anthropic.api.tiiny` and `http://ollama.api.tiiny`. Linux does
not resolve them by itself, so once a box is connected TiinyOS offers to
set them up ("Not now" is remembered; the API Key page offers it again).
One `pkexec` prompt adds them to `/etc/hosts` as `127.0.0.1` (in a
marked block) and gives pcsvr `cap_net_bind_service`; pcsvr then
restarts itself and answers those three names, and only those, on
127.0.0.1:80, forwarding to the box over USB or Wi-Fi. The capability
is lost when an update replaces pcsvr, and the prompt comes back. To
undo: delete the `tiinyos api names` block from `/etc/hosts`.

Without that setup, the box's own mDNS names work on hosts that resolve
`.local` (avahi with nss-mdns): `http://openai.api.tiiny.local/v1`,
`http://anthropic.api.tiiny.local`, `http://ollama.api.tiiny.local`.
The app's agent integrations (OpenCode, Goose, KiloCode and others)
write the bare `openai.api.tiiny` into those tools' settings, so they
need the setup above.

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

Not ported: OCR itself (`@napi-rs/system-ocr` has no Linux build; the
app logs it and continues), text selection helpers, and the Windows
auto-update installer (the launcher updates the app instead). Image
resizing uses sharp's WebAssembly build, fetched from npm, because the
native Linux addon crashes under Electron. x64 only.
A future TiinyOS release may call pcsvr routes this port does not have
yet. The launcher keeps the previous version for that case: point
`~/.local/share/tiinyos/app/current` at the older directory and start
with `TIINY_APP_UPDATE=0`, which skips the update check.

## Tests

```sh
(cd pcsvr && go test ./...)
python3 -m unittest discover -s tests
```

## Layout

| Path | What it is |
|---|---|
| `pcsvr/` | Linux reimplementation of the PC service |
| `linux/` | Launcher, Electron entry, USB link helper, service configs |
| `pack/fetch.py` | Downloads and updates the official app and Electron |
| `pack/build.sh` | Builds `dist/tiinyos` |
| `repackage/` | Go self-extractor for the launcher files |
| `docs/pcsvr-api.md` | Recovered request and response reference |
| `docs/api-tracker.json` | Route, status, and provenance |
| `docs/provenance.md` | What was examined and verified, by date |

Tiiny's developer portal is <https://docs.dev.tiiny.ai/>. A public
write-up of the device API is
<https://github.com/thezolon/tiiny-studio/blob/main/docs/DEVICE-API.md>.

TiinyOS is Tiiny AI's software and Electron is the Electron project's.
This repository holds only the Linux service, launcher, and tooling.

## License

MIT for this repository's code (`LICENSE`). TiinyOS itself, which the
launcher downloads from Tiiny, stays under Tiiny's own terms.
