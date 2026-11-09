#!/bin/sh
# Build dist/tiinyos, one Linux executable.
#
# The executable holds this project's files and a pinned 7-Zip: the
# launcher, the Linux pcsvr, fetch (downloads and unpacks the official
# app) and 7zz (reads Tiiny's NSIS installer). On first run it downloads
# the official TiinyOS app from Tiiny and Electron from its GitHub
# release. It needs nothing installed beyond a Linux desktop.
#
# Usage: pack/build.sh
# Output: dist/tiinyos

set -eu

ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
STAGE=$ROOT/.scratch/pack-stage
CACHE=$ROOT/.scratch/pack-cache
# 7-Zip for Linux, static build (7zzs); source at the same release.
SEVENZIP_VERSION=26.05
SEVENZIP_URL=https://github.com/ip7z/7zip/releases/download/$SEVENZIP_VERSION/7z2605-linux-x64.tar.xz
SEVENZIP_SHA256=22cf82fcc6eb7474f1d5bb4f02c0f8ebd573a718089d6833dc30f8350a3b232e
export GOCACHE="${GOCACHE:-$ROOT/.scratch/gocache}"
export GOTMPDIR="${GOTMPDIR:-$ROOT/.scratch/gotmp}"
mkdir -p "$GOTMPDIR" "$CACHE"

SEVENZIP_TAR=$CACHE/7z-$SEVENZIP_VERSION.tar.xz
if ! echo "$SEVENZIP_SHA256  $SEVENZIP_TAR" | sha256sum -c --status 2>/dev/null; then
  curl -fsSL -o "$SEVENZIP_TAR" "$SEVENZIP_URL"
  echo "$SEVENZIP_SHA256  $SEVENZIP_TAR" | sha256sum -c --quiet
fi

rm -rf "$STAGE"
mkdir -p "$STAGE/linux" "$ROOT/dist"
(cd "$ROOT/pcsvr" && go build -trimpath -buildvcs=false -o "$STAGE/linux/pcsvr" .)
(cd "$ROOT/fetch" && go build -trimpath -buildvcs=false -o "$STAGE/linux/fetch" .)
tar -xJf "$SEVENZIP_TAR" -C "$STAGE/linux" 7zzs License.txt
mv "$STAGE/linux/7zzs" "$STAGE/linux/7zz"
mv "$STAGE/linux/License.txt" "$STAGE/linux/7zz-License.txt"
cp "$ROOT/linux/tiinyos.sh" "$ROOT/linux/main.js" "$ROOT/linux/preload.js" "$ROOT/linux/setup.html" \
  "$ROOT/linux/pcsvr-api.yaml" "$ROOT/linux/compat.yaml" "$ROOT/linux/tiiny-usb.network" \
  "$STAGE/linux/"
cp -r "$ROOT/linux/integrations" "$STAGE/linux/"
python3 "$ROOT/pack/payload.py" "$STAGE" "$ROOT/repackage/payload.zip"
(cd "$ROOT/repackage" && go build -trimpath -buildvcs=false -o "$ROOT/dist/tiinyos" .)
ls -lh "$ROOT/dist/tiinyos"
