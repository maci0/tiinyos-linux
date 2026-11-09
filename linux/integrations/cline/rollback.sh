#!/bin/sh
# Undoes a failed install or update's settings changes.
set -eu
. "$(dirname "$0")/cline.sh"
stop_cline
node_action rollback
frontend_action restore
