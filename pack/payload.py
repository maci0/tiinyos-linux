#!/usr/bin/env python3
"""Zip the staged Linux app into repackage/payload.zip.

Usage: payload.py <stage dir> <payload.zip>
Executables keep mode 755 so the unpacked tree runs as is.
"""

import os
import sys
import zipfile

EXECUTABLES = ("pcsvr", "tiinyos.sh", "fetch.py", "extract.py")


def main() -> None:
    stage, dest = sys.argv[1:]
    with zipfile.ZipFile(dest, "w", compression=zipfile.ZIP_DEFLATED) as out:
        for dirpath, _dirs, files in os.walk(stage):
            for name in files:
                path = os.path.join(dirpath, name)
                info = zipfile.ZipInfo.from_file(path, os.path.relpath(path, stage))
                info.compress_type = zipfile.ZIP_DEFLATED
                if name in EXECUTABLES:
                    info.external_attr = 0o755 << 16
                with open(path, "rb") as src, out.open(info, "w") as dst:
                    dst.write(src.read())
    print(f"payload {os.path.getsize(dest)} bytes")


if __name__ == "__main__":
    main()
