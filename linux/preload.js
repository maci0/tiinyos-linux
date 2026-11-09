// Runs in the main process before the packaged TiinyOS bundle.
//
// The bundle was built for Windows and macOS. On Linux a handful of
// calls throw before the UI is up: the Windows auto-launch path, the
// registry-backed PC id, and AppImage-only updater install. Each one
// already has a failure return in the bundle (false, "absent", or a
// thrown error the caller catches). This preload makes those calls
// take that path instead of spawning powershell, reg, or cscript.
"use strict";

const childProcess = require("node:child_process");
const realExecFile = childProcess.execFile;
const realExecFileSync = childProcess.execFileSync;
const realSpawn = childProcess.spawn;

const blocked = new Set([
  "powershell.exe",
  "powershell",
  "pwsh.exe",
  "pwsh",
  "cscript.exe",
  "cscript",
  "wscript.exe",
  "wscript",
  "reg.exe",
  "reg",
  "schtasks.exe",
  "schtasks",
  "wmic.exe",
  "wmic",
  "netsh.exe",
  "netsh",
]);

function baseName(file) {
  const text = String(file || "");
  const parts = text.split(/[\\/]/);
  return parts[parts.length - 1].toLowerCase();
}

function blockedError(file) {
  const error = new Error("linux port: " + baseName(file) + " is not available");
  error.code = "ENOENT";
  return error;
}

function execFile(file, args, options, callback) {
  if (typeof args === "function") {
    callback = args;
    args = [];
    options = {};
  } else if (typeof options === "function") {
    callback = options;
    options = {};
  }
  if (blocked.has(baseName(file))) {
    const error = blockedError(file);
    if (typeof callback === "function") {
      process.nextTick(callback, error, "", "");
      return undefined;
    }
    return realExecFile(file, args, options, callback);
  }
  return realExecFile(file, args, options, callback);
}

function execFileSync(file, args, options) {
  if (blocked.has(baseName(file))) {
    throw blockedError(file);
  }
  return realExecFileSync(file, args, options);
}

function spawn(file, args, options) {
  if (blocked.has(baseName(file))) {
    const child = realSpawn("/bin/false", [], { stdio: "ignore" });
    const error = blockedError(file);
    process.nextTick(() => child.emit("error", error));
    return child;
  }
  return realSpawn(file, args, options);
}

childProcess.execFile = execFile;
childProcess.execFileSync = execFileSync;
childProcess.spawn = spawn;
