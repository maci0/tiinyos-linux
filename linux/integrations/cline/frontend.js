// Cline desktop on Linux keeps its UI state in WebKitGTK localStorage
// (SQLite, ItemTable key TEXT / value BLOB of UTF-16 JSON). configure
// makes the Tiiny provider the selected model, the local machine the
// execution target, and marks onboarding done, keeping the previous
// values in TIINY_INSTALL_DIR/frontend-baseline.json; restore puts them
// back. Run only while Cline is stopped. Exit 1 from status: no storage.
"use strict";

const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { DatabaseSync } = require("node:sqlite");

const dataHome = process.env.XDG_DATA_HOME || path.join(os.homedir(), ".local", "share");
const DB = path.join(dataHome, "bot.cline.app", "localstorage", "tauri_localhost_0.localstorage");
const KEYS = ["cline.code.model-selection.v1", "cline.code.execution-target.v1", "cline.code.onboarding.v1"];
const root = process.env.TIINY_INSTALL_DIR;
if (!root) throw new Error("TIINY_INSTALL_DIR is required");
const baseline = path.join(root, "frontend-baseline.json");

const decode = (blob) => (blob === undefined ? undefined : JSON.parse(Buffer.from(blob).toString("utf16le")));
const encode = (value) => Buffer.from(JSON.stringify(value), "utf16le");
const object = (v) => (v && typeof v === "object" && !Array.isArray(v) ? v : {});

function read(db) {
  const rows = {};
  for (const key of KEYS) {
    const row = db.prepare("SELECT value FROM ItemTable WHERE key = ?").get(key);
    rows[key] = row ? Buffer.from(row.value).toString("hex") : undefined;
  }
  return rows;
}

function write(db, rows) {
  db.exec("BEGIN IMMEDIATE");
  try {
    for (const key of KEYS) {
      if (rows[key] === undefined) {
        db.prepare("DELETE FROM ItemTable WHERE key = ?").run(key);
      } else {
        db.prepare("INSERT OR REPLACE INTO ItemTable (key, value) VALUES (?, ?)").run(key, Buffer.from(rows[key], "hex"));
      }
    }
    db.exec("COMMIT");
  } catch (error) {
    db.exec("ROLLBACK");
    throw error;
  }
}

function configure(db) {
  const before = read(db);
  if (!fs.existsSync(baseline)) {
    fs.writeFileSync(baseline, JSON.stringify({ path: DB, before }) + "\n", { mode: 0o600 });
  }
  const model = JSON.parse(process.env.TIINY_INTEGRATION_CONFIG || "{}").provider?.model || "default";
  const hex = (key) => (before[key] === undefined ? undefined : Buffer.from(before[key], "hex"));
  const selection = object(decode(hex(KEYS[0])));
  const next = { ...before };
  next[KEYS[0]] = encode({
    ...selection,
    lastProvider: "tiinyos",
    lastModelByProvider: { ...object(selection.lastModelByProvider), tiinyos: model },
  }).toString("hex");
  next[KEYS[1]] = encode({ ...object(decode(hex(KEYS[1]))), target: "local" }).toString("hex");
  if (!object(decode(hex(KEYS[2]))).completedAt) {
    next[KEYS[2]] = encode({ completedAt: new Date().toISOString() }).toString("hex");
  }
  write(db, next);
}

function restore(db) {
  if (!fs.existsSync(baseline)) return;
  const saved = JSON.parse(fs.readFileSync(baseline, "utf8"));
  if (saved.path === DB) write(db, saved.before);
  fs.rmSync(baseline);
}

const action = process.argv[2];
if (!fs.existsSync(DB)) {
  process.exit(action === "status" ? 1 : 0);
}
if (action !== "status") {
  const db = new DatabaseSync(DB);
  try {
    if (action === "configure") configure(db);
    else if (action === "restore") restore(db);
    else throw new Error(`unknown action ${action}`);
  } finally {
    db.close();
  }
}
