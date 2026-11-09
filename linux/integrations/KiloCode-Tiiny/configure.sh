#!/bin/sh
# Writes the Tiiny provider into Kilo's config (the bundle's configure.js),
# then makes it the default model in each editor's Kilo Code settings
# (editor-settings.js): Kilo Code 7 starts new chats on its own gateway
# unless kilo-code.new.model.* say otherwise.
set -eu
here=$(dirname "$0")
ELECTRON_RUN_AS_NODE=1 "$TIINY_NODE_EXECUTABLE" "$here/../extension/configure.js"
ELECTRON_RUN_AS_NODE=1 "$TIINY_NODE_EXECUTABLE" "$here/editor-settings.js"
