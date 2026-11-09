#!/usr/bin/env python3
"""Install the official TiinyOS app, and Electron, under a data directory.

Usage:
  fetch.py app <dir>       newest TiinyOS app; prints the version directory
  fetch.py electron <dir>  pinned Electron runtime; prints the binary path

app asks the endpoint the TiinyOS updater itself asks
(/update/check_update) for the newest Windows x64 build, downloads it
from Tiiny's CDN when it is newer than the installed one, and extracts
app.asar and app.asar.unpacked into <dir>/<version>. <dir>/current
points at the version in use; the previous one is kept. When the check
or the download fails, the installed version is used. With
TIINY_APP_UPDATE=0 and a version installed, no check is made, so
current can be pointed at the kept older version. Nothing from the
package is redistributed: every user fetches it from Tiiny.

The Windows x64 build is used: the macOS package exists only for
arm64. Neither ships a Linux sharp addon. The native one from npm
segfaults under Electron (its bundled GLib clashes with the GLib
Electron links), so app adds sharp's WebAssembly build instead:
@img/sharp-wasm32 at the asar's sharp version and its @emnapi/runtime,
from the npm registry, checked against the registry's sha512, into
<dir>/<version>/node_modules, which Node searches after the asar's own.
That step is best effort: without it the app's only sharp caller
(OcrUtils) logs a warning and uses the original image. The app's logo
is copied to <dir>/<version>/icon.png for the desktop entry.
"""

import base64
import hashlib
import json
import os
import shutil
import sys
import tarfile
import urllib.error
import urllib.request
import zipfile
from pathlib import Path

import extract

CHECK_URL = "https://service.online.tiiny-svr.com/update/check_update"
PACKAGE_PLATFORM = "windows"
PACKAGE_ARCH = "amd64"
# A version the service always reports as outdated, used before any
# install so the answer carries the download URL.
NO_VERSION = "0.0.0"
HTTP_TIMEOUT_SECONDS = 60
CHUNK_BYTES = 1 << 20
KEEP_VERSIONS = 2

# The app was built with Electron 37 and is checked on this release.
ELECTRON_VERSION = "40.10.6"
ELECTRON_BASE = (
    f"https://github.com/electron/electron/releases/download/v{ELECTRON_VERSION}"
)
ELECTRON_ZIP = f"electron-v{ELECTRON_VERSION}-linux-x64.zip"

NPM_REGISTRY = "https://registry.npmjs.org"
SHARP_WASM = "@img/sharp-wasm32"
EMNAPI = "@emnapi/runtime"
ICON_IN_ASAR = "out/main/tiiny-logo.png"

USER_AGENT = "tiinyos-linux"


def log(text: str) -> None:
    print(f"fetch: {text}", file=sys.stderr)


def version_key(version: str) -> tuple[int, ...]:
    """1.0.3 -> (1, 0, 3); a leading v and any suffix are ignored."""
    core = version.strip().lstrip("vV").split("-", 1)[0].split("+", 1)[0]
    parts = core.split(".")
    if not parts or not all(part.isdigit() for part in parts):
        raise ValueError(f"not a version: {version!r}")
    return tuple(int(part) for part in parts)


def request(url: str, body: bytes | None = None) -> urllib.request.Request:
    headers = {"User-Agent": USER_AGENT, "Accept": "application/json"}
    if body is not None:
        headers["Content-Type"] = "application/json"
    return urllib.request.Request(url, data=body, headers=headers)


def latest(installed: str) -> tuple[str, str, int] | None:
    """(version, url, bytes) of a newer package, or None when current."""
    body = json.dumps(
        {
            "arch": PACKAGE_ARCH,
            "platform": PACKAGE_PLATFORM,
            "now_version": installed,
            "current_os_version": installed,
            "current_service_version": installed,
        }
    ).encode()
    with urllib.request.urlopen(
        request(CHECK_URL, body), timeout=HTTP_TIMEOUT_SECONDS
    ) as resp:
        doc = json.load(resp)
    if not isinstance(doc, dict):
        raise TypeError("update check: reply is not an object")
    if doc.get("need_update") is not True:
        return None
    version, url, size = (
        doc.get("newest_version"),
        doc.get("downloads_url"),
        doc.get("total_bytes"),
    )
    if (
        not isinstance(version, str)
        or not isinstance(url, str)
        or not url.startswith("https://")
    ):
        raise ValueError(f"update check: incomplete reply {doc}")
    if not isinstance(size, int) or size <= 0:
        raise ValueError(f"update check: no size in reply {doc}")
    version_key(version)
    if "/" in version or version.startswith("."):
        raise ValueError(f"update check: bad version {version!r}")
    return version, url, size


def download(url: str, dest: Path, size: int | None = None) -> None:
    part = dest.with_name(dest.name + ".part")
    log(f"downloading {url}")
    with urllib.request.urlopen(
        request(url), timeout=HTTP_TIMEOUT_SECONDS
    ) as resp, part.open("wb") as out:
        shutil.copyfileobj(resp, out, CHUNK_BYTES)
    got = part.stat().st_size
    if size is not None and got != size:
        part.unlink()
        raise ValueError(f"{url}: got {got} bytes, expected {size}")
    part.replace(dest)


def installed_version(root: Path) -> str | None:
    current = root / "current"
    if not current.is_symlink() or not (current / "app.asar").is_file():
        return None
    return os.readlink(current)


def asar_header(asar: Path) -> tuple[dict[str, object], int]:
    """The asar's JSON header and the offset its file data starts at."""
    with asar.open("rb") as src:
        prefix = src.read(16)
        # Pickle: 4-byte size of the header pickle, then that pickle:
        # 4-byte payload size, 4-byte JSON length, JSON, padding.
        pickle_size = int.from_bytes(prefix[4:8], "little")
        json_len = int.from_bytes(prefix[12:16], "little")
        header = json.loads(src.read(json_len))
    return header, 8 + pickle_size


def asar_file(asar: Path, inner: str) -> bytes:
    """One file from an asar archive; KeyError when it is absent."""
    header, base = asar_header(asar)
    node: object = header
    for part in inner.split("/"):
        if not isinstance(node, dict):
            raise KeyError(inner)
        node = node["files"][part]
    if not isinstance(node, dict) or "offset" not in node:
        raise KeyError(inner)
    with asar.open("rb") as src:
        src.seek(base + int(node["offset"]))
        return src.read(int(node["size"]))


def check_app(stage: Path) -> None:
    """Refuse a package without the entry main.js loads."""
    try:
        asar_file(stage / "app.asar", "out/main/index.js")
    except KeyError as exc:
        raise ValueError("app.asar has no out/main/index.js") from exc


def npm_meta(name: str, version: str) -> dict[str, object]:
    url = f"{NPM_REGISTRY}/{name}/{version}"
    with urllib.request.urlopen(request(url), timeout=HTTP_TIMEOUT_SECONDS) as resp:
        meta = json.load(resp)
    if not isinstance(meta, dict):
        raise TypeError(f"{url}: not an object")
    return meta


def npm_install(meta: dict[str, object], modules: Path) -> None:
    """Unpack one registry package into modules/<name> after an integrity check."""
    name, dist = str(meta["name"]), meta["dist"]
    if not isinstance(dist, dict):
        raise TypeError(f"{name}: no dist")
    algo, _, want = str(dist["integrity"]).partition("-")
    if algo != "sha512":
        raise ValueError(f"{name}: unsupported integrity {algo}")
    with urllib.request.urlopen(
        request(str(dist["tarball"])), timeout=HTTP_TIMEOUT_SECONDS
    ) as resp:
        data = resp.read()
    if base64.b64encode(hashlib.sha512(data).digest()).decode() != want:
        raise ValueError(f"{name}: tarball does not match its integrity")
    dest = modules / name
    tgz = modules / f".{name.replace('/', '_')}.tgz"
    tgz.parent.mkdir(parents=True, exist_ok=True)
    tgz.write_bytes(data)
    shutil.rmtree(dest, ignore_errors=True)
    with tarfile.open(tgz) as tar:
        for member in tar.getmembers():
            if member.name.startswith("package/"):
                member.name = member.name.removeprefix("package/")
                tar.extract(member, dest, filter="data")
    tgz.unlink()


def caret_ok(version: str, wanted: str) -> bool:
    """version satisfies a ^X.Y.Z range."""
    if not wanted.startswith("^"):
        return version == wanted
    have, low = version_key(version), version_key(wanted[1:])
    return have[0] == low[0] and have >= low


def add_sharp_wasm(version_dir: Path) -> None:
    sharp = json.loads(
        asar_file(version_dir / "app.asar", "node_modules/sharp/package.json")
    )
    stage = version_dir / ".node_modules.tmp"
    shutil.rmtree(stage, ignore_errors=True)
    wasm = npm_meta(SHARP_WASM, str(sharp["version"]))
    deps = wasm.get("dependencies")
    wanted = str(deps.get(EMNAPI, "")) if isinstance(deps, dict) else ""
    runtime = npm_meta(EMNAPI, "latest")
    if not caret_ok(str(runtime["version"]), wanted):
        raise ValueError(f"{EMNAPI} {runtime['version']} does not satisfy {wanted!r}")
    npm_install(wasm, stage)
    npm_install(runtime, stage)
    final = version_dir / "node_modules"
    shutil.rmtree(final, ignore_errors=True)
    stage.rename(final)
    log(f"added {SHARP_WASM} {sharp['version']} and {EMNAPI} {runtime['version']}")


def add_extras(version_dir: Path) -> None:
    """Icon and WebAssembly sharp, each added once per version."""
    icon = version_dir / "icon.png"
    if not icon.is_file():
        try:
            icon.write_bytes(asar_file(version_dir / "app.asar", ICON_IN_ASAR))
        except KeyError:
            log(f"no {ICON_IN_ASAR} in this version")
    if not (version_dir / "node_modules" / SHARP_WASM).is_dir():
        try:
            add_sharp_wasm(version_dir)
        except (OSError, ValueError, TypeError, KeyError, tarfile.TarError) as exc:
            log(f"image module not added, the app runs without it: {exc}")


def install(root: Path, version: str, url: str, size: int) -> None:
    package = root / "download" / url.rsplit("/", 1)[1]
    package.parent.mkdir(parents=True, exist_ok=True)
    if not package.is_file() or package.stat().st_size != size:
        download(url, package, size)
    stage = root / f".{version}.tmp"
    shutil.rmtree(stage, ignore_errors=True)
    manifest = extract.extract(str(package), str(stage))
    check_app(stage)
    manifest["version"] = version
    manifest["url"] = url
    (stage / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    final = root / version
    shutil.rmtree(final, ignore_errors=True)
    stage.rename(final)
    link = root / ".current.tmp"
    link.unlink(missing_ok=True)
    link.symlink_to(version)
    link.replace(root / "current")
    package.unlink()
    log(f"installed TiinyOS {version}")


def prune(root: Path) -> None:
    versions = []
    for path in root.iterdir():
        try:
            versions.append((version_key(path.name), path))
        except ValueError:
            continue
    current = os.readlink(root / "current")
    keep = {current} | {
        p.name for _k, p in sorted(versions, reverse=True)[:KEEP_VERSIONS]
    }
    for _key, path in versions:
        if path.name not in keep:
            shutil.rmtree(path)


def app(root: Path) -> Path:
    root.mkdir(parents=True, exist_ok=True)
    have = installed_version(root)
    if have is not None and os.environ.get("TIINY_APP_UPDATE") == "0":
        add_extras(root / have)
        return root / have
    try:
        newer = latest(have or NO_VERSION)
        if newer and (have is None or version_key(newer[0]) > version_key(have)):
            install(root, *newer)
            prune(root)
    except (OSError, ValueError, TypeError, SystemExit) as exc:
        if have is None:
            raise SystemExit(
                f"fetch: no TiinyOS installed and the download failed: {exc}"
            ) from exc
        log(f"update failed, staying on {have}: {exc}")
    version = installed_version(root)
    if version is None:
        raise SystemExit("fetch: no TiinyOS installed")
    add_extras(root / version)
    return root / version


def electron(root: Path) -> Path:
    target = root / f"electron-{ELECTRON_VERSION}"
    binary = target / "electron"
    if binary.is_file():
        return binary
    root.mkdir(parents=True, exist_ok=True)
    archive = root / ELECTRON_ZIP
    download(f"{ELECTRON_BASE}/{ELECTRON_ZIP}", archive)
    with urllib.request.urlopen(
        request(f"{ELECTRON_BASE}/SHASUMS256.txt"), timeout=HTTP_TIMEOUT_SECONDS
    ) as resp:
        sums = resp.read().decode()
    want = next(
        (
            line.split()[0]
            for line in sums.splitlines()
            if line.endswith(f"*{ELECTRON_ZIP}")
        ),
        None,
    )
    digest = hashlib.sha256()
    with archive.open("rb") as src:
        for chunk in iter(lambda: src.read(CHUNK_BYTES), b""):
            digest.update(chunk)
    if want is None or digest.hexdigest() != want:
        archive.unlink()
        raise SystemExit(f"fetch: {ELECTRON_ZIP} does not match SHASUMS256.txt")
    stage = root / f".electron-{ELECTRON_VERSION}.tmp"
    shutil.rmtree(stage, ignore_errors=True)
    with zipfile.ZipFile(archive) as zf:
        for info in zf.infolist():
            path = Path(zf.extract(info, stage))
            mode = info.external_attr >> 16
            if mode:
                path.chmod(mode & 0o777)
    archive.unlink()
    stage.rename(target)
    log(f"installed Electron {ELECTRON_VERSION}")
    return binary


def main() -> None:
    if len(sys.argv) != 3 or sys.argv[1] not in ("app", "electron"):
        raise SystemExit("usage: fetch.py app|electron <dir>")
    root = Path(sys.argv[2]).resolve()
    print(app(root) if sys.argv[1] == "app" else electron(root))


if __name__ == "__main__":
    main()
