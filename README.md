# TiinyOS on Linux

Run Tiiny's official TiinyOS desktop app on Linux, with a Tiiny box on
USB or Wi-Fi. Unofficial; not affiliated with or endorsed by Tiiny AI.

TiinyOS ships for Windows and macOS only: an Electron app plus a Go
service, `pcsvr`, that Wine cannot run. This project runs the official
app under Electron and replaces `pcsvr` with a Linux service written
from scratch. No Tiiny code is in this repository or the release: the
launcher downloads the official build from Tiiny on first start and on
every new version.

## Install

```sh
curl -fLo tiinyos https://github.com/maci0/tiinyos-linux/releases/latest/download/tiinyos
chmod +x tiinyos && ./tiinyos
```

One 18 MB x86-64 executable, nothing else to install. It adds TiinyOS
to the app menu and downloads TiinyOS (140 MB, from Tiiny) and Electron
(110 MB) to `~/.local/share/tiinyos`. Build it yourself with
`pack/build.sh` (Go 1.27, python3, curl, tar).

## What works

| Feature | Linux | Notes |
|---|:-:|---|
| Chat, Model Store, Dashboard, Vault, History, Settings | ✅ | |
| Box over USB | ✅ | without NetworkManager: one password prompt |
| Box over Wi-Fi | ✅ | |
| Switching USB ↔ Wi-Fi while running | ✅ | |
| Box OS update from Settings | ✅ | |
| Box apps (Tiiny Music, n8n, Open WebUI, …) | ✅ | Tiiny Music checked |
| OCR | ✅ | the app's built-in Tesseract |
| API names for other programs | ✅ | `openai.api.tiiny` etc. via `/etc/hosts`, one password prompt |
| Agent Store: KiloCode | ✅ | VS Code, Cursor or VSCodium |
| Agent Store: OpenCode, Pi, OpenClaw | ✅ | open in your terminal or browser |
| Agent Store: Goose, Cline | ✅ | desktop apps; Cline needs WebKitGTK 4.1 |
| Agent Store: Hermes Agent, Tiiny Meeting | ❌ | Tiiny ships no Linux build |
| Uninstall Goose from the store | ❌ | TiinyOS shows "Manual uninstall required"; see Uninstall below |
| Windows-only helpers (text selection, auto-update installer) | ❌ | the launcher updates the app instead |
| ARM64 | ❌ | x86-64 only |

Tested with TiinyOS 1.0.3 and box OS 0.1.39 on KDE Wayland and Xvfb.
Every integration was checked answering through the box.

<p>
<img src="docs/img/setup-usb.png" width="360" alt="Connect Tiiny over USB prompt">
<img src="docs/img/setup-api.png" width="360" alt="Use Tiiny's API in other apps prompt">
</p>

How it works, and what each part does:
[docs/how-it-works.md](docs/how-it-works.md).

## Uninstall

Quit TiinyOS from its tray icon, then:

```sh
rm -rf ~/.local/share/tiinyos ~/.tiinyai
rm -f ~/.local/share/applications/tiinyos.desktop ~/.config/autostart/tiiny-ai.desktop
```

Also remove the `tiinyos api names` block from `/etc/hosts` if you set
it up, and the `tiiny-usb-<mac>` NetworkManager profile if there is
one. What each Agent Store integration leaves behind is listed in
[docs/how-it-works.md](docs/how-it-works.md#uninstalling-integrations).

## Development

```sh
pack/build.sh                                                  # dist/tiinyos
for m in pcsvr fetch repackage; do (cd $m && go test ./...); done
```

| Path | What it is |
|---|---|
| `pcsvr/` | Linux PC service |
| `fetch/` | Downloads and unpacks the official app and Electron |
| `linux/` | Launcher, Electron entry, setup prompt, integration scripts |
| `pack/` | Build script; API path tracker |
| `repackage/` | Self-extractor that is `dist/tiinyos` |
| `docs/` | How it works, API reference, provenance ledger |

## License

MIT (`LICENSE`) for this repository; TiinyOS stays under Tiiny's terms.
The executable includes 7-Zip 26.05 (`7zz`, LGPL-2.1 with BSD parts;
license in `7zz-License.txt`, source at
<https://github.com/ip7z/7zip/releases/tag/26.05>).
