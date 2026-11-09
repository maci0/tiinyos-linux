#!/usr/bin/env python3
"""Merge paths from a new package into docs/api-tracker.json.

A path that was already tracked keeps its implementation status and
provenance. A path the new package no longer names is marked removed
and keeps its history. A new path is recorded as not implemented.
"""

import json
import os
import sys


def load(path):
    if not os.path.isfile(path):
        return {"package": "", "paths": {}}
    with open(path, encoding="utf-8") as src:
        return json.load(src)


def main():
    if len(sys.argv) != 4:
        raise SystemExit(
            "usage: tracker.py <tracker.json> <paths.txt> <package-manifest.json>"
        )
    tracker_path, paths_path, manifest_path = sys.argv[1:]
    tracker = load(tracker_path)
    with open(manifest_path, encoding="utf-8") as src:
        manifest = json.load(src)
    with open(paths_path, encoding="utf-8") as src:
        seen = {line.strip() for line in src if line.strip()}
    paths = tracker.setdefault("paths", {})
    package = manifest["package"]
    digest = manifest["package_sha256"]
    for path in sorted(seen):
        row = paths.get(path)
        if row is None:
            paths[path] = {
                "status": "not-implemented",
                "provenance": f"named by {package}",
                "source": "package",
                "first_package": package,
                "first_sha256": digest,
                "last_package": package,
                "removed": False,
            }
            continue
        row["last_package"] = package
        if row.get("removed") and row.get("source") == "package":
            row["removed"] = False
    for path, row in paths.items():
        # A Linux handler that the app bundle does not spell stays.
        # A path that came from an earlier package and is absent now is removed.
        if (
            path not in seen
            and row.get("source") == "package"
            and not row.get("removed")
        ):
            row["removed"] = True
            row["removed_by"] = package
    tracker["package"] = package
    tracker["package_sha256"] = digest
    with open(tracker_path, "w", encoding="utf-8") as out:
        json.dump(tracker, out, indent=2, sort_keys=True)
        out.write("\n")
    added = sum(1 for row in paths.values() if row["first_package"] == package)
    removed = sum(1 for row in paths.values() if row.get("removed_by") == package)
    print(
        f"tracker: {len(paths)} paths, {added} first seen in {package}, {removed} removed"
    )


if __name__ == "__main__":
    main()
