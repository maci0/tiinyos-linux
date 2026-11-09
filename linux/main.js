// Electron entry for the packaged TiinyOS bundle on Linux.
//
// The bundle's main process reads process.resourcesPath for app.asar
// and for resources/pcsvr/linux/pcsvr. A stock Electron binary has its
// own resourcesPath, so this entry points it at a directory that holds
// the extracted asar plus the Linux pcsvr before the bundle loads.
"use strict";

const dns = require("node:dns");
const fs = require("node:fs");
const http = require("node:http");
const net = require("node:net");
const path = require("node:path");
const { app } = require("electron");

// The UI calls http://api.tiiny and the other names below from the main
// process through Node and from the renderer through Chromium. The
// system resolver knows none of them. Every name resolves to this
// machine and is sent to the pcsvr catalog port (a free port picked at
// launch, TIINY_CATALOG_PORT) with its hostname kept.
// pcsvr forwards them to the box at the address discovery currently
// reports (USB first, then Wi-Fi), so a box plugged in or moved after
// launch needs no restart, and answers locally only where the box has
// no route (proxyOrLocal in pcsvr/proxy.go).
const TIINY_NAMES = new Set([
  "api.tiiny",
  "kb.api.tiiny",
  "tts.api.tiiny",
  "connector.api.tiiny",
  "backup.managed.tiiny",
  "p8800.api.tiiny",
  "agent-services.api.tiiny",
  "hardware-upgrade.api.tiiny",
  "auth.api.tiiny",
  "chat-history.api.tiiny",
  "wifi.api.tiiny",
]);
const LOOPBACK = "127.0.0.1";
const catalogPort = process.env.TIINY_CATALOG_PORT;
if (!catalogPort) {
  process.stderr.write("tiinyos: TIINY_CATALOG_PORT is not set\n");
  process.exit(1);
}

// --host-rules rewrites host and port where Chromium opens the socket,
// websockets included, and keeps the Host header. --host-resolver-rules
// would drop the port, and webRequest redirects do not apply to a
// websocket handshake.
app.commandLine.appendSwitch(
  "host-rules",
  [...TIINY_NAMES].map((name) => `MAP ${name} ${LOOPBACK}:${catalogPort}`).join(", "),
);
const lookup = dns.lookup;
dns.lookup = function (hostname, options, callback) {
  if (typeof options === "function") {
    callback = options;
    options = {};
  }
  if (!TIINY_NAMES.has(hostname)) {
    return lookup.call(dns, hostname, options, callback);
  }
  if (options && options.all) {
    process.nextTick(callback, null, [{ address: LOOPBACK, family: 4 }]);
    return undefined;
  }
  process.nextTick(callback, null, LOOPBACK, 4);
  return undefined;
};

// Every main-process client ends in net.connect or net.createConnection:
// Node's fetch and WebSocket, the undici copy bundled in the app, the
// ws package, and http.Agent (which keeps its own reference, so it is
// pointed at the patched function). dns.lookup above supplies the
// address; the port is set here. The request keeps its Host header.
function toCatalog(options) {
  if (options && typeof options === "object" && TIINY_NAMES.has(options.host) && String(options.port) !== catalogPort) {
    return { ...options, port: Number(catalogPort) };
  }
  return options;
}
for (const name of ["connect", "createConnection"]) {
  const real = net[name];
  net[name] = function (...args) {
    args[0] = toCatalog(args[0]);
    return real.apply(this, args);
  };
}
http.Agent.prototype.createConnection = net.createConnection;

// Must run before app ready. Chromium otherwise creates shared-memory
// files in /dev/shm, which is not writable in every session, and the
// GPU and network processes abort.
app.commandLine.appendSwitch("disable-dev-shm-usage");
app.commandLine.appendSwitch("no-sandbox");
app.commandLine.appendSwitch(
  "ozone-platform",
  process.env.ELECTRON_OZONE_PLATFORM_HINT === "wayland" ? "wayland" : "x11",
);

// Chromium keeps only the last value of a repeated switch. The bundle
// appends enable-features twice on Wayland, and the second call drops
// GlobalShortcutsPortal, so its global shortcuts never register. Merge
// repeated feature lists instead.
const FEATURE_SWITCHES = new Set(["enable-features", "disable-features"]);
const features = new Map();
const appendSwitch = app.commandLine.appendSwitch.bind(app.commandLine);
app.commandLine.appendSwitch = (name, value) => {
  if (!FEATURE_SWITCHES.has(name) || !value) {
    return appendSwitch(name, value);
  }
  const merged = features.get(name) || new Set();
  for (const feature of String(value).split(",")) {
    if (feature) {
      merged.add(feature);
    }
  }
  features.set(name, merged);
  return appendSwitch(name, [...merged].join(","));
};
// KWin advertises wp_color_manager_v1 but rejects the sRGB image
// description Chromium asks for, and Chromium logs that for every
// window. The app does no HDR or wide-gamut work.
app.commandLine.appendSwitch("disable-features", "WaylandWpColorManagerV1");

// The launcher registers tiinyai:// with the desktop (a desktop entry
// and the x-scheme-handler default). Electron's own registration runs
// xdg-mime against the private XDG directories the launcher sets and
// always fails, so report the launcher's result instead.
if (process.env.TIINY_PROTOCOL_REGISTERED === "1") {
  app.setAsDefaultProtocolClient = (protocol) => protocol === "tiinyai";
  app.isDefaultProtocolClient = (protocol) => protocol === "tiinyai";
}

// "Auto start" on Linux writes ~/.config/autostart/tiiny-ai.desktop with
// Exec=app.getPath("exe"), which here is the bare Electron binary: at
// login that starts Electron with no app. The launcher command starts
// the whole stack. (APPIMAGE, the bundle's other source for Exec, would
// also arm its AppImage updater, which deletes that file on install.)
const realGetPath = app.getPath.bind(app);
app.getPath = (name) => (name === "exe" && process.env.TIINY_LAUNCH ? process.env.TIINY_LAUNCH : realGetPath(name));

// Deprecation warnings come from the bundle's dependencies (punycode),
// which this port cannot change.
process.noDeprecation = true;

require("./preload.js");

// The packaged bundle sets the name itself once it loads. userData is
// set here so the first mkdir (it runs at load, before the bundle's
// own setName) lands somewhere writable. XDG config is not writable
// from every session that launches this.
app.setName("TiinyOS");
const dataHome =
  process.env.TIINY_DATA_DIR ||
  path.join(process.env.XDG_DATA_HOME || path.join(app.getPath("home"), ".local", "share"), "TiinyOS");
app.setPath("userData", dataHome);

const asar = process.env.TIINY_ASAR;
if (!asar) {
  process.stderr.write("tiinyos: TIINY_ASAR is not set\n");
  process.exit(1);
}

// Running under a stock Electron looks unpackaged, so the bundle loads
// renderer files from a directory that does not exist. Packaged mode
// uses <app path>/out/renderer inside the asar. A stock Electron reports
// its own version, which the bundle rejects, so the version is the one
// in the installed app's package.json.
const appVersion = require(path.join(asar, "package.json")).version;
Object.defineProperty(app, "isPackaged", { get: () => true });
app.setVersion(appVersion);
app.getVersion = () => appVersion;
app.getAppPath = () => asar;

// Agent integrations. The store's script bundles (under
// ~/.tiinyai/desktop-agents/<app>/bundle) name a script per platform,
// darwin and win32 only, and the app runs the one for process.platform
// with /bin/sh. When the app reads a bundle manifest, this launcher's
// integrations/<app>/ folder is copied into the bundle as linux/ (its
// scripts call each other there), and each script gains a linux entry:
// linux/<name> when that folder has it, else the darwin shell script.
const AGENT_MANIFEST = /\/\.tiinyai\/desktop-agents\/([^/]+)\/bundle\/(?:[^/]+\/)?manifest\.json$/;

function withLinuxScripts(file, text) {
  const own = path.join(__dirname, "integrations", file.match(AGENT_MANIFEST)[1]);
  const ownNames = fs.existsSync(own) ? fs.readdirSync(own) : [];
  if (ownNames.length > 0) {
    fs.cpSync(own, path.join(path.dirname(file), "linux"), { recursive: true });
  }
  const manifest = JSON.parse(text);
  for (const entry of Object.values(manifest.scripts || {})) {
    if (!entry || typeof entry !== "object" || entry.linux || typeof entry.darwin !== "string") {
      continue;
    }
    const name = path.basename(entry.darwin);
    if (ownNames.includes(name)) {
      entry.linux = `linux/${name}`;
    } else if (entry.darwin.endsWith(".sh")) {
      entry.linux = entry.darwin;
    }
  }
  return JSON.stringify(manifest);
}

const readFile = fs.promises.readFile;
fs.promises.readFile = async function (file, options) {
  const data = await readFile.call(this, file, options);
  return typeof file === "string" && typeof data === "string" && AGENT_MANIFEST.test(file)
    ? withLinuxScripts(file, data)
    : data;
};

require(path.join(asar, "out", "main", "index.js"));

// Setup prompts. pcsvr reports what it could not set up without admin
// rights: a Tiiny USB link (/pcsvr/usb-link) and the API names other
// programs use (/pcsvr/api-names). Each prompt explains that first, and
// only its primary button runs pcsvr's setup, which raises the desktop's
// polkit password prompt; the password never passes through this app.
const { BrowserWindow } = require("electron");
const PC_SERVICE = "http://127.0.0.1:60000";
const SETUP_POLL_MS = 3000;
const SETUP_DONE_CLOSE_MS = 1500;
const API_NAMES_READY_MS = 10000;
const API_KEY_ROUTE = "api-key";
const apiNamesDismissed = path.join(app.getPath("userData"), "api-names-dismissed");
const offeredLinks = new Set();
let setupWindow = null;
let apiNamesOffered = false;

async function getJSON(route) {
  return (await fetch(`${PC_SERVICE}${route}`)).json();
}

async function postSetup(route) {
  try {
    return await (await fetch(`${PC_SERVICE}${route}`, { method: "POST" })).json();
  } catch (error) {
    return { state: "failed", error: `PC service did not answer: ${error}` };
  }
}

// waitForAPINames polls until pcsvr sees the updated /etc/hosts.
async function waitForAPINames() {
  const until = Date.now() + API_NAMES_READY_MS;
  let last = { state: "failed", error: "PC service did not come back" };
  while (Date.now() < until) {
    await new Promise((resolve) => setTimeout(resolve, SETUP_POLL_MS / 3));
    try {
      last = await getJSON("/pcsvr/api-names");
      if (last.state === "ready") {
        return last;
      }
    } catch {
      // pcsvr did not answer; poll again.
    }
  }
  return { state: "failed", error: last.error || "API names not ready" };
}

async function setUpAPINames() {
  const result = await postSetup("/pcsvr/api-names/setup");
  return result.state === "configured" ? waitForAPINames() : result;
}

// offerAPINames opens the prompt once per run. Without force it respects
// an earlier "Not now" and waits for a connected box.
async function offerAPINames(force) {
  if (setupWindow || apiNamesOffered || (!force && fs.existsSync(apiNamesDismissed))) {
    return;
  }
  let status;
  try {
    status = await getJSON("/pcsvr/api-names");
    if (!force && ((await getJSON("/devices_list")).device_count || 0) === 0) {
      return;
    }
  } catch {
    return;
  }
  // needs_update: the /etc/hosts block points at an address the box
  // no longer has (it moved between USB and Wi-Fi).
  const mode = { needs_admin: "api", needs_update: "api-update" }[status.state];
  if (mode) {
    apiNamesOffered = true;
    openSetup(mode, setUpAPINames, () => fs.writeFileSync(apiNamesDismissed, ""));
  }
}

async function pollSetup() {
  if (setupWindow) {
    return;
  }
  let links = [];
  try {
    links = (await getJSON("/pcsvr/usb-link")).links || [];
  } catch {
    return;
  }
  const link = links.find((l) => l.state === "needs_admin" && !offeredLinks.has(`${l.index}:${l.attempt}`));
  if (link) {
    offeredLinks.add(`${link.index}:${link.attempt}`);
    openSetup("usb", () => postSetup(`/pcsvr/usb-link/setup?name=${encodeURIComponent(link.name)}`));
    return;
  }
  await offerAPINames(false);
}

// openSetup shows setup.html in the given mode. run performs the setup
// and resolves to pcsvr's answer; onDismiss runs when the user declines
// before trying.
function openSetup(mode, run, onDismiss = () => {}) {
  setupWindow = new BrowserWindow({
    width: 480,
    height: 440,
    resizable: false,
    maximizable: false,
    minimizable: false,
    fullscreenable: false,
    autoHideMenuBar: true,
    title: "TiinyOS",
    backgroundColor: "#ffffff",
    show: false,
    webPreferences: { sandbox: true, contextIsolation: true },
  });
  const win = setupWindow;
  const image = "file://" + path.join(asar, "out", "renderer", "assets", "welcome-device.png");
  win.loadFile(path.join(__dirname, "setup.html"), { query: { mode, image } });
  win.once("ready-to-show", () => win.show());
  win.on("closed", () => {
    setupWindow = null;
  });
  const show = (state, detail = "") => {
    if (!win.isDestroyed()) {
      win.webContents.executeJavaScript(`show(${JSON.stringify(state)}, ${JSON.stringify(detail)})`);
    }
  };
  let tried = false;
  win.webContents.on("did-navigate-in-page", async (_event, url) => {
    const action = new URL(url).hash;
    if (action === "#close") {
      if (!tried) {
        onDismiss();
      }
      win.close();
      return;
    }
    if (action !== "#setup") {
      return;
    }
    tried = true;
    show("working");
    const result = await run();
    if (result.state === "configured" || result.state === "ready") {
      show("done");
      setTimeout(() => !win.isDestroyed() && win.close(), SETUP_DONE_CLOSE_MS);
    } else {
      show("failed", result.error || "");
    }
  });
}

// The app's API Key page lists the API names; visiting it offers the
// setup again even after "Not now".
app.on("web-contents-created", (_event, contents) => {
  contents.on("did-navigate-in-page", (_e, url) => {
    if (url.includes(API_KEY_ROUTE)) {
      apiNamesOffered = false;
      offerAPINames(true);
    }
  });
});

app.whenReady().then(() => {
  setInterval(pollSetup, SETUP_POLL_MS);
});
