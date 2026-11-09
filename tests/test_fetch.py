"""fetch.app against a local update service and CDN.

Run: python3 -m unittest discover -s tests
extract.extract (7z on a real installer) is replaced by a stub that
writes a minimal asar; everything else is the shipped code path.
"""

import base64
import hashlib
import io
import json
import os
import shutil
import struct
import sys
import tarfile
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import ClassVar

ROOT = next(
    p for p in Path(__file__).resolve().parents if (p / "pack" / "fetch.py").is_file()
)
sys.path.insert(0, str(ROOT / "pack"))

import extract
import fetch

PACKAGE = b"MZ fake installer"


def write_asar(path: Path, files: dict[str, object]) -> None:
    raw = json.dumps({"files": files}).encode()
    pad = (4 - len(raw) % 4) % 4
    path.write_bytes(
        struct.pack("<IIII", 4, 8 + len(raw) + pad, 4 + len(raw) + pad, len(raw))
        + raw
        + b"\0" * pad
    )


def fake_extract(package: str, stage: str) -> dict[str, object]:
    assert Path(package).read_bytes() == PACKAGE
    Path(stage).mkdir(parents=True)
    entry = {
        "out": {"files": {"main": {"files": {"index.js": {"size": 0, "offset": "0"}}}}}
    }
    write_asar(Path(stage) / "app.asar", entry)
    return {"kind": "windows-nsis"}


class Service(BaseHTTPRequestHandler):
    newest = "1.0.3"
    asked: ClassVar[list[str]] = []

    def do_POST(self) -> None:
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        Service.asked.append(body["now_version"])
        port = self.server.server_address[1]
        doc: dict[str, object] = {
            "need_update": fetch.version_key(body["now_version"])
            < fetch.version_key(self.newest)
        }
        if doc["need_update"]:
            doc |= {
                "newest_version": self.newest,
                "total_bytes": len(PACKAGE),
                # fetch requires https; the test opener below maps it back.
                "downloads_url": f"https://127.0.0.1:{port}/blobs/tiinyos-{self.newest}.exe",
            }
        self.reply(json.dumps(doc).encode())

    def do_GET(self) -> None:
        self.reply(PACKAGE)

    def reply(self, data: bytes) -> None:
        self.send_response(200)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, *_args: object) -> None:
        pass


class FetchApp(unittest.TestCase):
    def setUp(self) -> None:
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Service)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        base = f"http://127.0.0.1:{self.server.server_address[1]}"
        self.saved = (fetch.CHECK_URL, extract.extract, fetch.request)
        fetch.CHECK_URL = base + "/update/check_update"
        extract.extract = fake_extract
        real_request = fetch.request
        fetch.request = lambda url, body=None: real_request(
            url.replace("https://", "http://"), body
        )
        self.root = Path(
            tempfile.mkdtemp(
                dir=ROOT / ".scratch" if (ROOT / ".scratch").is_dir() else None
            )
        )
        Service.asked = []

    def tearDown(self) -> None:
        fetch.CHECK_URL, extract.extract, fetch.request = self.saved
        shutil.rmtree(self.root)
        self.server.shutdown()
        self.server.server_close()

    def test_install_update_prune_and_offline(self) -> None:
        Service.newest = "1.0.3"
        self.assertEqual(fetch.app(self.root), self.root / "1.0.3")
        manifest = json.loads((self.root / "current" / "manifest.json").read_text())
        self.assertEqual(manifest["version"], "1.0.3")
        self.assertEqual(Service.asked, ["0.0.0"])

        # Current: no download, same directory.
        self.assertEqual(fetch.app(self.root), self.root / "1.0.3")
        self.assertEqual(Service.asked, ["0.0.0", "1.0.3"])

        # Newer releases replace current; only two versions stay.
        for version in ("1.0.10", "1.1.0"):
            Service.newest = version
            self.assertEqual(fetch.app(self.root), self.root / version)
        self.assertEqual(
            sorted(p.name for p in self.root.iterdir() if p.name[0].isdigit()),
            ["1.0.10", "1.1.0"],
        )
        self.assertFalse(any((self.root / "download").iterdir()))

        # Pinned: current is pointed back at the kept version and stays.
        (self.root / "current").unlink()
        (self.root / "current").symlink_to("1.0.10")
        os.environ["TIINY_APP_UPDATE"] = "0"
        try:
            self.assertEqual(fetch.app(self.root), self.root / "1.0.10")
        finally:
            del os.environ["TIINY_APP_UPDATE"]
        self.assertEqual(fetch.app(self.root), self.root / "1.1.0")

        # Service unreachable: the installed version is used.
        self.server.shutdown()
        self.server.server_close()
        self.assertEqual(fetch.app(self.root), self.root / "1.1.0")

    def test_bad_package_keeps_current(self) -> None:
        Service.newest = "1.0.3"
        fetch.app(self.root)
        Service.newest = "2.0.0"
        extract.extract = lambda _p, stage: Path(stage).mkdir() or write_asar(
            Path(stage) / "app.asar", {}
        )
        self.assertEqual(fetch.app(self.root), self.root / "1.0.3")


def asar_with(path: Path, contents: dict[str, bytes]) -> None:
    """An asar holding contents (path -> bytes)."""
    files: dict[str, object] = {}
    body = b""
    for name, data in contents.items():
        node = files
        *dirs, leaf = name.split("/")
        for part in dirs:
            node = node.setdefault(part, {"files": {}})["files"]  # type: ignore[assignment]
        node[leaf] = {"size": len(data), "offset": str(len(body))}
        body += data
    raw = json.dumps({"files": files}).encode()
    pad = (4 - len(raw) % 4) % 4
    path.write_bytes(
        struct.pack("<IIII", 4, 8 + len(raw) + pad, 4 + len(raw) + pad, len(raw))
        + raw
        + b"\0" * pad
        + body
    )


def tgz(files: dict[str, bytes]) -> bytes:
    out = io.BytesIO()
    with tarfile.open(fileobj=out, mode="w:gz") as tar:
        for name, data in files.items():
            info = tarfile.TarInfo(f"package/{name}")
            info.size = len(data)
            tar.addfile(info, io.BytesIO(data))
    return out.getvalue()


class Registry(BaseHTTPRequestHandler):
    tarballs: ClassVar[dict[str, bytes]] = {
        "/wasm.tgz": tgz({"package.json": b'{"name":"@img/sharp-wasm32"}'}),
        "/runtime.tgz": tgz({"index.js": b"module.exports = {}"}),
    }
    tamper = False

    def do_GET(self) -> None:
        base = f"http://127.0.0.1:{self.server.server_address[1]}"
        metas = {
            "/@img/sharp-wasm32/0.34.5": {
                "name": "@img/sharp-wasm32",
                "version": "0.34.5",
                "dependencies": {"@emnapi/runtime": "^1.7.0"},
                "dist": self.dist(base, "/wasm.tgz"),
            },
            "/@emnapi/runtime/latest": {
                "name": "@emnapi/runtime",
                "version": "1.11.3",
                "dist": self.dist(base, "/runtime.tgz"),
            },
        }
        if self.path in metas:
            data = json.dumps(metas[self.path]).encode()
        else:
            data = self.tarballs[self.path]
        self.send_response(200)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def dist(self, base: str, path: str) -> dict[str, str]:
        data = self.tarballs[path] + (b"x" if self.tamper else b"")
        digest = base64.b64encode(hashlib.sha512(data).digest()).decode()
        return {"tarball": base + path, "integrity": f"sha512-{digest}"}

    def log_message(self, *_args: object) -> None:
        pass


class Extras(unittest.TestCase):
    def setUp(self) -> None:
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Registry)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        self.saved = fetch.NPM_REGISTRY
        fetch.NPM_REGISTRY = f"http://127.0.0.1:{self.server.server_address[1]}"
        self.dir = Path(
            tempfile.mkdtemp(
                dir=ROOT / ".scratch" if (ROOT / ".scratch").is_dir() else None
            )
        )
        asar_with(
            self.dir / "app.asar",
            {
                "out/main/index.js": b"",
                "out/main/tiiny-logo.png": b"PNG",
                "node_modules/sharp/package.json": b'{"version":"0.34.5"}',
            },
        )

    def tearDown(self) -> None:
        fetch.NPM_REGISTRY = self.saved
        Registry.tamper = False
        shutil.rmtree(self.dir)
        self.server.shutdown()
        self.server.server_close()

    def test_icon_and_sharp_wasm(self) -> None:
        fetch.add_extras(self.dir)
        self.assertEqual((self.dir / "icon.png").read_bytes(), b"PNG")
        modules = self.dir / "node_modules"
        self.assertTrue((modules / "@img/sharp-wasm32/package.json").is_file())
        self.assertTrue((modules / "@emnapi/runtime/index.js").is_file())

    def test_bad_integrity_leaves_app_runnable(self) -> None:
        Registry.tamper = True
        fetch.add_extras(self.dir)
        self.assertTrue((self.dir / "icon.png").is_file())
        self.assertFalse((self.dir / "node_modules").exists())

    def test_caret(self) -> None:
        self.assertTrue(fetch.caret_ok("1.11.3", "^1.7.0"))
        self.assertFalse(fetch.caret_ok("2.0.0", "^1.7.0"))
        self.assertFalse(fetch.caret_ok("1.6.9", "^1.7.0"))


class VersionKey(unittest.TestCase):
    def test_order_and_rejects(self) -> None:
        self.assertLess(fetch.version_key("1.0.9"), fetch.version_key("1.0.10"))
        self.assertEqual(fetch.version_key("v1.2.3-beta"), (1, 2, 3))
        for bad in ("", "1.x", "../1"):
            with self.assertRaises(ValueError):
                fetch.version_key(bad)


if __name__ == "__main__":
    unittest.main()
