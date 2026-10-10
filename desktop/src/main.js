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

const { app, BrowserWindow, Menu, desktopCapturer, dialog, globalShortcut, ipcMain, screen, session, shell } = require("electron");
const crypto = require("node:crypto");
const fs = require("node:fs");
const http = require("node:http");
const https = require("node:https");
const path = require("node:path");
const { pathToFileURL } = require("node:url");
const { normalize, needsSecureSwitch } = require("./address");
const { scheduleUpdateCheck } = require("./updates");
const windowState = require("./window-state");
const { isSafeAccelerator } = require("./accelerator");

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

function windowStatePath() {
  return path.join(app.getPath("userData"), "window.json");
}

/** Tells the page whether the window is maximized, full screen, focused. */
function windowStateOf(w) {
  return { maximized: w.isMaximized(), fullscreen: w.isFullScreen(), focused: w.isFocused() };
}

function createWindow() {
  const isMac = process.platform === "darwin";
  const saved = windowState.restore(
    windowState.load(windowStatePath()),
    screen.getAllDisplays().map((d) => d.workArea),
  );
  win = new BrowserWindow({
    x: saved.x,
    y: saved.y,
    width: saved.width,
    height: saved.height,
    minWidth: windowState.MIN.width,
    minHeight: windowState.MIN.height,
    // No system title bar: the page draws its own (see preload.js). macOS
    // keeps its traffic lights, placed inside that bar.
    frame: isMac,
    titleBarStyle: isMac ? "hidden" : undefined,
    trafficLightPosition: isMac ? { x: 12, y: 10 } : undefined,
    show: false,
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

  // Remember size, position, maximized and full screen. The normal bounds
  // and the maximized flag are tracked here: while full screen (or
  // maximized, on some Linux window managers) getNormalBounds() reports
  // the full-screen size instead.
  let normal = win.getNormalBounds();
  let maximized = saved.maximized;
  let saveTimer = null;
  const track = () => {
    if (!win || win.isDestroyed() || win.isFullScreen() || win.isMinimized()) return;
    maximized = win.isMaximized();
    if (!maximized) normal = win.getBounds();
  };
  const remember = () => {
    if (!win || win.isDestroyed()) return;
    windowState.save(windowStatePath(), windowState.snapshot(normal, maximized, win.isFullScreen()));
  };
  const rememberSoon = () => {
    clearTimeout(saveTimer);
    saveTimer = setTimeout(remember, 400);
  };
  const sendState = () => {
    if (win && !win.isDestroyed()) win.webContents.send("window:state", windowStateOf(win));
  };
  for (const ev of ["resize", "move"]) {
    win.on(ev, () => {
      track();
      rememberSoon();
    });
  }
  for (const ev of ["maximize", "unmaximize", "enter-full-screen", "leave-full-screen"]) {
    win.on(ev, () => {
      track();
      sendState();
      rememberSoon();
    });
  }
  if (saved.maximized) win.maximize();
  if (saved.fullscreen) win.setFullScreen(true);
  win.once("ready-to-show", () => win && win.show());
  // Shown anyway if the page is slow, so the window never stays invisible.
  setTimeout(() => win && !win.isDestroyed() && !win.isVisible() && win.show(), 3000);
  win.on("focus", sendState);
  win.on("blur", sendState);
  win.on("close", () => {
    clearTimeout(saveTimer);
    remember();
  });
  win.on("closed", () => {
    win = null;
  });

  const wc = win.webContents;
  // F11 toggles full screen everywhere (the menu bar is hidden, so its own
  // shortcut cannot be relied on). On macOS the system's Ctrl+Cmd+F and the
  // green button also work.
  wc.on("before-input-event", (e, input) => {
    if (input.type === "keyDown" && input.key === "F11" && !input.control && !input.alt && !input.meta && !input.shift) {
      e.preventDefault();
      if (win) win.setFullScreen(!win.isFullScreen());
    }
  });
  // A new page asks for its background shortcut again.
  wc.on("did-start-navigation", (details) => {
    if (details.isMainFrame && !details.isSameDocument) clearGlobalMute();
  });
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

// ---- window controls and the background mute shortcut ----
//
// The connect page and the chosen node's pages draw the title bar, so both
// may minimize, maximize, close and full-screen the window and open the app
// menu, and may set the one background shortcut (Toggle mute, checked in
// accelerator.js). Nothing else is reachable from a page.

function fromWindowPage(event) {
  if (!win || event.sender !== win.webContents || event.senderFrame !== win.webContents.mainFrame) return false;
  const url = event.senderFrame?.url;
  return isConnectPage(url) || onNode(url);
}

ipcMain.on("window:minimize", (event) => {
  if (fromWindowPage(event)) win.minimize();
});
ipcMain.on("window:maximize", (event) => {
  if (!fromWindowPage(event)) return;
  if (win.isMaximized()) win.unmaximize();
  else win.maximize();
});
ipcMain.on("window:close", (event) => {
  if (fromWindowPage(event)) win.close();
});
ipcMain.on("window:fullscreen", (event, on) => {
  if (!fromWindowPage(event)) return;
  win.setFullScreen(typeof on === "boolean" ? on : !win.isFullScreen());
});
ipcMain.handle("window:state", (event) => (fromWindowPage(event) ? windowStateOf(win) : null));
ipcMain.on("window:menu", (event, x, y) => {
  if (!fromWindowPage(event)) return;
  const menu = Menu.getApplicationMenu();
  if (!menu) return;
  const opts = { window: win };
  if (Number.isFinite(x) && Number.isFinite(y)) Object.assign(opts, { x: Math.round(x), y: Math.round(y) });
  menu.popup(opts);
});

let globalMute = null;

function clearGlobalMute() {
  if (globalMute) {
    globalShortcut.unregister(globalMute);
    globalMute = null;
  }
}

ipcMain.handle("keys:global-mute", (event, acc) => {
  if (!fromWindowPage(event)) return false;
  clearGlobalMute();
  if (acc === null || acc === undefined || acc === "") return true;
  if (!isSafeAccelerator(acc)) return false;
  let ok = false;
  try {
    ok = globalShortcut.register(acc, () => {
      if (win && !win.isDestroyed()) win.webContents.send("keys:global-mute");
    });
  } catch {
    ok = false;
  }
  if (ok) globalMute = acc;
  return ok;
});

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

/**
 * Asks which screen or window to share. Resolves to a desktopCapturer
 * source, or null when the person cancels.
 */
async function chooseScreen(parent) {
  const sources = await desktopCapturer.getSources({ types: ["screen", "window"], thumbnailSize: { width: 0, height: 0 } });
  if (sources.length === 0) return null;
  // Screens first, then windows; a dialog has room for a handful of buttons.
  const shown = sources.slice(0, 8);
  const buttons = [...shown.map((s, i) => (s.id.startsWith("screen:") ? `Screen ${i + 1}` : s.name.slice(0, 40) || "Window")), "Cancel"];
  const { response } = await dialog.showMessageBox(parent, {
    type: "question",
    title: "Share screen",
    message: "Share screen",
    detail: "Choose what to share in this call.",
    buttons,
    cancelId: buttons.length - 1,
    defaultId: 0,
    noLink: true,
  });
  return response < shown.length ? shown[response] : null;
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

    // The client asks to copy (link codes, recovery codes), to show
    // notifications, and for the microphone, camera and screen in calls.
    // Only the chosen node's pages get any of these; never location or
    // anything else.
    const allowed = new Set(["clipboard-sanitized-write", "notifications", "media", "display-capture"]);
    ses.setPermissionRequestHandler((_wc, permission, callback, details) => {
      if (permission === "media") {
        const types = details.mediaTypes ?? [];
        callback(onNode(details.requestingUrl) && types.every((t) => t === "audio" || t === "video"));
        return;
      }
      callback(allowed.has(permission) && onNode(details.requestingUrl));
    });
    ses.setPermissionCheckHandler((_wc, permission, requestingOrigin) => allowed.has(permission) && onNode(requestingOrigin));

    // Screen sharing (getDisplayMedia): Electron has no picker of its own, so
    // ask which screen or window to share in a plain dialog.
    ses.setDisplayMediaRequestHandler((request, callback) => {
      const origin = request.securityOrigin || request.frame?.url;
      if (!win || !onNode(origin)) return callback({});
      chooseScreen(win)
        .then((source) => callback(source ? { video: source } : {}))
        .catch(() => callback({}));
    });

    buildMenu();
    createWindow();
    scheduleUpdateCheck(() => win);
    app.on("activate", () => {
      if (BrowserWindow.getAllWindows().length === 0) createWindow();
    });
  });

  app.on("window-all-closed", () => app.quit());
  app.on("will-quit", () => globalShortcut.unregisterAll());
}
