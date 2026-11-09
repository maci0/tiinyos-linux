// Sets Kilo Code's default model for new chats to the Tiiny provider
// (tiiny/default, written to ~/.config/kilo/kilo.jsonc by configure.js)
// in every VS Code-family editor that has the extension installed. A
// value the user changed from the extension default is kept, and a
// settings file that is not plain JSON (comments) is left alone.
"use strict";

const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const EXTENSION = "kilocode.kilo-code-";
const WANT = { "kilo-code.new.model.providerID": "tiiny", "kilo-code.new.model.modelID": "default" };
const EXTENSION_DEFAULTS = { "kilo-code.new.model.providerID": "kilo", "kilo-code.new.model.modelID": "kilo-auto/free" };
const home = os.homedir();
const config = process.env.XDG_CONFIG_HOME || path.join(home, ".config");
// Extensions directory and user settings directory of each editor.
const EDITORS = [
  [".vscode", "Code"],
  [".vscode-insiders", "Code - Insiders"],
  [".cursor", "Cursor"],
  [".vscode-oss", "VSCodium"],
];

for (const [extensions, settingsDir] of EDITORS) {
  const extDir = path.join(home, extensions, "extensions");
  if (!fs.existsSync(extDir) || !fs.readdirSync(extDir).some((name) => name.startsWith(EXTENSION))) {
    continue;
  }
  const file = path.join(config, settingsDir, "User", "settings.json");
  let settings = {};
  if (fs.existsSync(file)) {
    const text = fs.readFileSync(file, "utf8");
    try {
      settings = text.trim() === "" ? {} : JSON.parse(text);
    } catch {
      console.error(`${file} is not plain JSON; set kilo-code.new.model.* there by hand`);
      continue;
    }
  }
  let changed = false;
  for (const [key, value] of Object.entries(WANT)) {
    if (settings[key] === undefined || settings[key] === EXTENSION_DEFAULTS[key]) {
      changed ||= settings[key] !== value;
      settings[key] = value;
    }
  }
  if (changed) {
    fs.mkdirSync(path.dirname(file), { recursive: true });
    fs.writeFileSync(file, JSON.stringify(settings, null, 2) + "\n");
  }
}
