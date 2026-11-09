#!/bin/sh
# Build dist/tiinyos, one Linux executable.
#
# The executable holds this project's files only: the launcher, the
# Linux pcsvr, and the fetch scripts. On first run it downloads the
# official TiinyOS app from Tiiny and Electron from its GitHub release.
#
# Usage: pack/build.sh
# Output: dist/tiinyos

set -eu

ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
STAGE=$ROOT/.scratch/pack-stage
export GOCACHE="${GOCACHE:-$ROOT/.scratch/gocache}"
export GOTMPDIR="${GOTMPDIR:-$ROOT/.scratch/gotmp}"
mkdir -p "$GOTMPDIR"

rm -rf "$STAGE"
mkdir -p "$STAGE/linux" "$STAGE/pack" "$ROOT/dist"
(cd "$ROOT/pcsvr" && go build -buildvcs=false -o "$STAGE/linux/pcsvr" .)
cp "$ROOT/linux/tiinyos.sh" "$ROOT/linux/main.js" "$ROOT/linux/preload.js" "$ROOT/linux/setup.html" \
  "$ROOT/linux/pcsvr-api.yaml" "$ROOT/linux/compat.yaml" "$ROOT/linux/tiiny-usb.network" \
  "$STAGE/linux/"
cp -r "$ROOT/linux/integrations" "$STAGE/linux/"
cp "$ROOT/pack/fetch.py" "$ROOT/pack/extract.py" "$STAGE/pack/"
python3 "$ROOT/pack/payload.py" "$STAGE" "$ROOT/repackage/payload.zip"
(cd "$ROOT/repackage" && go build -buildvcs=false -o "$ROOT/dist/tiinyos" .)
ls -lh "$ROOT/dist/tiinyos"
