#!/bin/sh
# Applies the Tiiny settings, then starts Cline detached from TiinyOS.
set -eu
. "$(dirname "$0")/cline.sh"
sh "$HERE/configure.sh"
start_cline
