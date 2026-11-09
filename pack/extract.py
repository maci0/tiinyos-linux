#!/usr/bin/env python3
"""Extract the Electron assets from a TiinyOS Windows or macOS package.

The layout of a future installer can move. This looks for app.asar and
the unpacked tree by name, then records where they were found.
"""

import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile


def sha256(path):
    digest = hashlib.sha256()
    with open(path, "rb") as src:
        for chunk in iter(lambda: src.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def run(cmd):
    result = subprocess.run(cmd, check=False, capture_output=True)
    if result.returncode != 0:
        text = result.stderr.decode("utf-8", "replace").strip()
        raise SystemExit(f"extract: {' '.join(cmd)} failed: {text}")


def kind_of(path):
    with open(path, "rb") as src:
        head = src.read(8)
    if head.startswith(b"MZ"):
        return "windows-nsis"
    if head.startswith(b"xar!"):
        return "macos-pkg"
    raise SystemExit(f"extract: {path} is not a Windows NSIS installer or a macOS pkg")


def find_named(root, name):
    found = []
    for dirpath, dirs, files in os.walk(root):
        if name in files or name in dirs:
            found.append(os.path.join(dirpath, name))
    return found


def extract_windows(package, dest):
    run(["7z", "x", "-y", f"-o{dest}", package, "$PLUGINSDIR/app-64.7z"])
    archive = os.path.join(dest, "$PLUGINSDIR", "app-64.7z")
    if not os.path.isfile(archive):
        archives = []
        for dirpath, dirs, files in os.walk(dest):
            for name in files:
                if name.endswith(".7z"):
                    archives.append(os.path.join(dirpath, name))
        if len(archives) != 1:
            raise SystemExit(f"extract: expected one payload archive, found {archives}")
        archive = archives[0]
    app = os.path.join(dest, "app")
    run(["7z", "x", "-y", f"-o{app}", archive])
    return app


def extract_macos(package, dest):
    run(["7z", "x", "-y", f"-o{dest}", package])
    payloads = []
    for dirpath, dirs, files in os.walk(dest):
        if "Payload" in files and "PackageInfo" in files:
            payloads.append(os.path.join(dirpath, "Payload"))
    if not payloads:
        raise SystemExit("extract: macOS pkg has no Payload")
    payload = max(payloads, key=os.path.getsize)
    out = os.path.join(dest, "payload")
    os.makedirs(out, exist_ok=True)
    # The payload is a cpio archive, sometimes gzip-compressed.
    with open(payload, "rb") as src:
        magic = src.read(2)
    if magic == b"\x1f\x8b":
        run(
            [
                "bash",
                "-c",
                f"gzip -dc {json.dumps(payload)} | cpio -idD {json.dumps(out)}",
            ]
        )
    else:
        run(["cpio", "-idD", out, "-I", payload])
    return out


def copy_assets(app_root, stage):
    asars = find_named(app_root, "app.asar")
    if len(asars) != 1:
        raise SystemExit(f"extract: expected one app.asar, found {asars}")
    unpacked = find_named(app_root, "app.asar.unpacked")
    os.makedirs(stage, exist_ok=True)
    shutil.copy2(asars[0], os.path.join(stage, "app.asar"))
    if unpacked:
        dest = os.path.join(stage, "app.asar.unpacked")
        if os.path.exists(dest):
            shutil.rmtree(dest)
        shutil.copytree(unpacked[0], dest)
    return {
        "app.asar": os.path.relpath(asars[0], app_root),
        "app.asar.unpacked": os.path.relpath(unpacked[0], app_root) if unpacked else "",
        "asar_sha256": sha256(asars[0]),
        "asar_bytes": os.path.getsize(asars[0]),
    }


def extract(package, stage):
    """Copy app.asar and its unpacked tree from package into stage."""
    kind = kind_of(package)
    # Next to stage, not in /tmp: the installer unpacks to hundreds of MB.
    parent = os.path.dirname(os.path.abspath(stage))
    os.makedirs(parent, exist_ok=True)
    work = tempfile.mkdtemp(prefix=".tiiny-extract-", dir=parent)
    try:
        app_root = (
            extract_windows(package, work)
            if kind == "windows-nsis"
            else extract_macos(package, work)
        )
        found = copy_assets(app_root, stage)
    finally:
        shutil.rmtree(work, ignore_errors=True)
    manifest = {
        "package": os.path.basename(package),
        "package_sha256": sha256(package),
        "package_bytes": os.path.getsize(package),
        "kind": kind,
        **found,
    }
    return manifest


def main():
    if len(sys.argv) != 4:
        raise SystemExit("usage: extract.py <package> <stage> <manifest.json>")
    package, stage, manifest_path = sys.argv[1:]
    manifest = extract(package, stage)
    kind = manifest["kind"]
    with open(manifest_path, "w", encoding="utf-8") as out:
        json.dump(manifest, out, indent=2)
        out.write("\n")
    print(f"extract: {kind} {manifest['app.asar']} -> {stage}")


if __name__ == "__main__":
    main()
