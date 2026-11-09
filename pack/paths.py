#!/usr/bin/env python3
"""List HTTP paths named by a TiinyOS app.asar.

The scan reads the asar header and the file bytes. It does not execute
the package. A later package that adds or drops a path shows up as a
diff against docs/api-tracker.json.
"""

import json
import re
import struct
import sys

PATH_RE = re.compile(
    rb"(?<![A-Za-z0-9_$.])/(?:api|account|dns|devices_list|debug|pcsvr|select_device|status|v1|conversations|docs|log|version|healthz|requests|usb-storage|connect-page|webui)(?:/[A-Za-z0-9_./{}-]{0,80})?"
)


def read_asar(path):
    with open(path, "rb") as src:
        src.read(8)
        header_len, json_len = struct.unpack("<II", src.read(8))
        header_text = src.read(json_len)
        data_off = 16 + header_len
        header = json.loads(header_text)
        files = []

        def walk(node, prefix):
            if "files" in node:
                for name, child in node["files"].items():
                    child_name = prefix + "/" + name if prefix else name
                    walk(child, child_name)
                return
            if "offset" in node and prefix.endswith(
                (".js", ".mjs", ".cjs", ".html", ".json")
            ):
                src.seek(data_off + int(node["offset"]))
                files.append((prefix, src.read(int(node["size"]))))

        walk(header, "")
        return files


def paths_in(blob):
    found = set()
    for match in PATH_RE.finditer(blob):
        path = match.group(0).decode("ascii", "replace")
        if "." in path.split("/")[-1]:
            continue
        found.add(path.rstrip("/") or path)
    return found


def main():
    if len(sys.argv) != 2:
        raise SystemExit("usage: paths.py <app.asar>")
    found = set()
    for name, blob in read_asar(sys.argv[1]):
        found.update(paths_in(blob))
    for path in sorted(found):
        print(path)


if __name__ == "__main__":
    main()
