// The Strange Domain desktop app: a window onto a Domain Node.
//
// The app does not bundle the client. It loads it from the node the person
// picks, so the client always matches the node's version, and the client's
// own rules (CSP, same-origin API calls, the WebSocket) work unchanged.
//
// The client needs a secure context (WebCrypto Ed25519, IndexedDB-held keys).
// A node on the LAN over plain http:// is not one, so at start-up the app
// tells Chromium to treat exactly that one origin as secure. Chromium reads
// that switch only at start-up, so choosing a different http:// node
// restarts the app. A node with a self-signed certificate is trusted by the
// SHA-256 fingerprint the person confirmed, and only for that node.
"use strict";

const { app, BrowserWindow, Menu, ipcMain, session, shell } = require("electron");
const crypto = require("node:crypto");
const fs = require("node:fs");
const http = require("node:http");
const https = require("node:https");
const path = require("node:path");
const { pathToFileURL } = require("node:url");
const { normalize, needsSecureSwitch } = require("./address");
const { scheduleUpdateCheck } = require("./updates");

const CONNECT_PAGE = path.join(__dirname, "connect.html");
const CONNECT_URL = pathToFileURL(CONNECT_PAGE).href;

// ---- saved node ----

function configPath() {
  return path.join(app.getPath("userData"), "node.json");
}

/** @returns {{url: string, fingerprint?: string} | null} */
function readConfig() {
  try {
    const c = JSON.parse(fs.readFileSync(configPath(), "utf8"));
    const url = normalize(c.url);
    const fingerprint = typeof c.fingerprint === "string" ? c.fingerprint : undefined;
    return { url, fingerprint };
  } catch {
    return null;
  }
}

function writeConfig(c) {
  fs.mkdirSync(path.dirname(configPath()), { recursive: true });
  fs.writeFileSync(configPath(), JSON.stringify(c, null, 2) + "\n", { mode: 0o600 });
}

let current = readConfig();

// Must happen before the app is ready.
const secureOrigin = current && needsSecureSwitch(current.url) ? current.url : null;
if (secureOrigin) app.commandLine.appendSwitch("unsafely-treat-insecure-origin-as-secure", secureOrigin);

// ---- probing a node ----

/**
 * Asks the node for /api/v1/info. For https, also reports the certificate's
 * fingerprint and whether this computer trusts it.
 */
function probe(origin) {
  return new Promise((resolve) => {
    const u = new URL("/api/v1/info", origin);
    const mod = u.protocol === "https:" ? https : http;
    const req = mod.get(
      u,
      // The certificate is checked below: either the system trusts it or
      // the person compares its fingerprint before anything is saved.
      { timeout: 8000, rejectUnauthorized: false, headers: { Accept: "application/json" } },
      (res) => {
        let trusted = true;
        let fingerprint;
        if (u.protocol === "https:") {
          trusted = res.socket.authorized === true;
          fingerprint = res.socket.getPeerCertificate()?.fingerprint256;
        }
        let body = "";
        res.setEncoding("utf8");
        res.on("data", (chunk) => {
          body += chunk;
          if (body.length > 64 * 1024) req.destroy(new Error("response too large"));
        });
        res.on("end", () => {
          let info;
          try {
            info = JSON.parse(body);
          } catch {
            /* not JSON */
          }
          if (res.statusCode !== 200 || !info || typeof info.version !== "string") {
            resolve({ ok: false, error: "Something answered at that address, but it is not a Domain Node." });
            return;
          }
          resolve({ ok: true, url: origin, version: info.version, trusted, fingerprint });
        });
      },
    );
    req.on("timeout", () => req.destroy(new Error("no answer within 8 seconds")));
    req.on("error", (e) => resolve({ ok: false, error: `Could not reach ${origin}: ${e.message}.` }));
  });
}

// What the connect page last probed; save() accepts only that.
let probed = null;

// ---- window ----

let win = null;

function isConnectPage(url) {
  return typeof url === "string" && url.split(/[?#]/)[0] === CONNECT_URL;
}

function onNode(url) {
  try {
    return !!current && new URL(url).origin === current.url;
  } catch {
    return false;
  }
}

function openExternal(url) {
  try {
    const p = new URL(url).protocol;
    if (p === "https:" || p === "http:" || p === "mailto:") void shell.openExternal(url);
  } catch {
    /* ignore */
  }
}

function showConnect(error) {
  const query = {};
  if (error) query.error = error;
  void win.loadFile(CONNECT_PAGE, { query });
}

function showNode() {
  void win.loadURL(current.url + "/");
}

function createWindow() {
  win = new BrowserWindow({
    width: 1280,
    height: 820,
    minWidth: 360,
    minHeight: 480,
    backgroundColor: "#050505",
    title: "The Strange Domain",
    icon: path.join(__dirname, "..", "build", "icon.png"),
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
      webviewTag: false,
      preload: path.join(__dirname, "preload.js"),
    },
  });

  const wc = win.webContents;
  // Only the connect page and the chosen node may load in the window.
  // Anything else (a link in a message) opens in the system browser.
  wc.on("will-navigate", (e, url) => {
    if (isConnectPage(url) || onNode(url)) return;
    e.preventDefault();
    openExternal(url);
  });
  wc.on("will-redirect", (e, url) => {
    if (!isConnectPage(url) && !onNode(url)) e.preventDefault();
  });
  wc.setWindowOpenHandler(({ url }) => {
    openExternal(url);
    return { action: "deny" };
  });
  wc.on("did-fail-load", (_e, code, desc, url, isMainFrame) => {
    if (!isMainFrame || code === -3 /* aborted */ || !current || !onNode(url)) return;
    showConnect(`Could not load ${current.url} (${desc}). Check that the node is running, then connect again.`);
  });

  if (current) showNode();
  else showConnect();
}

// ---- connect page IPC ----

function fromConnectPage(event) {
  return isConnectPage(event.senderFrame?.url);
}

ipcMain.handle("node:current", (event) => {
  if (!fromConnectPage(event)) return null;
  return current ? current.url : null;
});

ipcMain.handle("node:probe", async (event, address) => {
  if (!fromConnectPage(event)) return { ok: false, error: "Not allowed." };
  let origin;
  try {
    origin = normalize(address);
  } catch (e) {
    return { ok: false, error: e.message };
  }
  const r = await probe(origin);
  probed = r.ok ? r : null;
  if (!r.ok) return r;
  return { ok: true, url: r.url, version: r.version, untrustedFingerprint: r.trusted ? undefined : r.fingerprint };
});

ipcMain.handle("node:connect", (event, url) => {
  if (!fromConnectPage(event)) return { ok: false, error: "Not allowed." };
  if (!probed || probed.url !== url) return { ok: false, error: "Check the address again." };
  current = { url, fingerprint: probed.trusted ? undefined : probed.fingerprint };
  writeConfig(current);
  probed = null;
  if (needsSecureSwitch(url) && url !== secureOrigin) {
    // Chromium only reads the secure-origin switch at start-up.
    const opts = process.env.APPIMAGE ? { execPath: process.env.APPIMAGE, args: process.argv.slice(1) } : undefined;
    app.relaunch(opts);
    app.exit(0);
    return { ok: true, restarting: true };
  }
  showNode();
  return { ok: true };
});

// ---- app ----

function fingerprintOf(pem) {
  try {
    return new crypto.X509Certificate(pem).fingerprint256;
  } catch {
    return null;
  }
}

function buildMenu() {
  const isMac = process.platform === "darwin";
  const template = [
    ...(isMac ? [{ role: "appMenu" }] : []),
    {
      label: "File",
      submenu: [
        { label: "Change node…", click: () => win && showConnect() },
        { type: "separator" },
        isMac ? { role: "close" } : { role: "quit" },
      ],
    },
    { role: "editMenu" },
    {
      label: "View",
      submenu: [
        { role: "reload" },
        { role: "toggleDevTools" },
        { type: "separator" },
        { role: "resetZoom" },
        { role: "zoomIn" },
        { role: "zoomOut" },
        { type: "separator" },
        { role: "togglefullscreen" },
      ],
    },
    { role: "windowMenu" },
  ];
  Menu.setApplicationMenu(Menu.buildFromTemplate(template));
}

if (!app.requestSingleInstanceLock()) {
  app.quit();
} else {
  app.on("second-instance", () => {
    if (!win) return;
    if (win.isMinimized()) win.restore();
    win.focus();
  });

  app.on("web-contents-created", (_e, contents) => {
    contents.on("will-attach-webview", (e) => e.preventDefault());
  });

  app.whenReady().then(() => {
    const ses = session.defaultSession;

    // A self-signed node is trusted only if its certificate is the one whose
    // fingerprint the person confirmed. Everything else gets Chromium's
    // normal verification (-3).
    ses.setCertificateVerifyProc((req, callback) => {
      if (current?.fingerprint && new URL(current.url).hostname.replace(/^\[|\]$/g, "") === req.hostname) {
        if (fingerprintOf(req.certificate.data) === current.fingerprint) return callback(0);
      }
      callback(-3);
    });

    // The client needs no camera, microphone or location yet. It asks to copy
    // (link codes, recovery codes) and to show message notifications.
    const allowed = new Set(["clipboard-sanitized-write", "notifications"]);
    ses.setPermissionRequestHandler((_wc, permission, callback, details) => {
      callback(allowed.has(permission) && onNode(details.requestingUrl));
    });
    ses.setPermissionCheckHandler((_wc, permission, requestingOrigin) => allowed.has(permission) && onNode(requestingOrigin));

    buildMenu();
    createWindow();
    scheduleUpdateCheck(() => win);
    app.on("activate", () => {
      if (BrowserWindow.getAllWindows().length === 0) createWindow();
    });
  });

  app.on("window-all-closed", () => app.quit());
}
