#!/usr/bin/env python3
"""Zip the staged Linux app into repackage/payload.zip.

Usage: payload.py <stage dir> <payload.zip>
ZipInfo.from_file keeps each file's mode, so executables unpack as such.
"""

import os
import sys
import zipfile


def main() -> None:
    stage, dest = sys.argv[1:]
    with zipfile.ZipFile(dest, "w", compression=zipfile.ZIP_DEFLATED) as out:
        for dirpath, _dirs, files in os.walk(stage):
            for name in sorted(files):
                path = os.path.join(dirpath, name)
                out.write(path, os.path.relpath(path, stage))
    print(f"payload {os.path.getsize(dest)} bytes")


if __name__ == "__main__":
    main()
