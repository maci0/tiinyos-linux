# TiinyOS on Linux

Run Tiiny's official TiinyOS desktop app on Linux, with a Tiiny box on
USB or Wi-Fi. Unofficial; not affiliated with or endorsed by Tiiny AI.

TiinyOS ships for Windows and macOS only: an Electron app plus a Go
service, `pcsvr`, that Wine cannot run. This project runs the official
app under Electron and replaces `pcsvr` with a Linux service written
from scratch for the same HTTP API. No Tiiny code is in this repository
or in the release: on first start, and whenever Tiiny releases a new
version, the launcher downloads the official build from Tiiny.

## Install

Download `tiinyos` from the
[latest release](https://github.com/maci0/tiinyos-linux/releases/latest),
then:

```sh
chmod +x tiinyos && ./tiinyos
```

One 18 MB x86-64 executable, nothing else to install. It adds TiinyOS
to the app menu, then downloads TiinyOS (about 140 MB, from Tiiny) and
Electron (110 MB, from its GitHub release) to `~/.local/share/tiinyos`.

To build it yourself (Go 1.27, python3, curl, tar with xz):
`pack/build.sh` writes `dist/tiinyos`.

## What it adds

<p>
<img src="docs/img/setup-usb.png" width="360" alt="Connect Tiiny over USB prompt">
<img src="docs/img/setup-api.png" width="360" alt="Use Tiiny's API in other apps prompt">
</p>

- **USB without a network daemon.** If no NetworkManager takes the box's
  USB link, TiinyOS asks once (polkit password prompt) and sets it up
  itself. Wi-Fi needs nothing.
- **API names for other programs.** One password prompt adds
  `openai.api.tiiny`, `anthropic.api.tiiny` and `ollama.api.tiiny` to
  `/etc/hosts`, pointing at the box; skipped if they are already there.
  The box's mDNS names (`openai.api.tiiny.local`) work without it.
- **KiloCode** from the Agent Store in VS Code, Cursor or VSCodium, set
  up to use the box's models.
- **Switching** between USB and Wi-Fi while the app runs, with no restart.

In the app, the prompts show the box photo from the downloaded TiinyOS.

## Status

Tested with TiinyOS 1.0.3 and a real box under Xvfb and on KDE Wayland:
chat, model store, dashboard, vault, history, agent store, Tiiny Music,
settings, box OS update, power cycles, USB and Wi-Fi. x86-64 only.
Details: [docs/how-it-works.md](docs/how-it-works.md).

## Missing on Linux

Tiiny's Agent Store offers these desktop integrations for macOS and
Windows only (their downloads are macOS and Windows builds), so the
store does not list them on Linux:

| Integration | Kind |
|---|---|
| OpenCode | CLI |
| Goose | desktop app |
| Pi | CLI |
| Hermes Agent | desktop app |
| OpenClaw | CLI |
| Tiiny Meeting | desktop app |

Apps that run on the box itself (Tiiny Music, n8n, Open WebUI,
OpenHands and the rest) do not depend on this port; Tiiny Music was
checked. OCR inside TiinyOS is also missing: its addon uses the macOS
and Windows OCR engines, and the app skips it.

## Tests

```sh
for m in pcsvr fetch repackage; do (cd $m && go test ./...); done
```

## Layout

| Path | What it is |
|---|---|
| `pcsvr/` | Linux PC service |
| `fetch/` | Downloads and unpacks the official app and Electron |
| `linux/` | Launcher, Electron entry, setup prompt, integration scripts, configs |
| `pack/` | `build.sh` builds `dist/tiinyos`; API path tracker |
| `repackage/` | Self-extractor that is `dist/tiinyos` |
| `docs/` | How it works, API reference, provenance ledger |

## License

MIT (`LICENSE`) for this repository. TiinyOS stays under Tiiny's terms.
The executable includes 7-Zip 26.05 (`7zz`, LGPL-2.1 with BSD parts;
license in `7zz-License.txt` next to it, source at
<https://github.com/ip7z/7zip/releases/tag/26.05>).
