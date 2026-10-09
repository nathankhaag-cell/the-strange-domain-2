// Checks GitHub for a newer version of the desktop app, once, shortly after
// start-up. Nothing is downloaded or installed unless the person agrees.
//
// Where the app can replace itself (the Windows installer, an AppImage on
// Linux), electron-updater downloads and installs the update after the
// person clicks "Update and restart". It reads latest.yml /
// latest-linux.yml from the GitHub release, which the release workflow
// uploads. Elsewhere (the .deb, macOS without a signing certificate) the
// dialog offers to open the release page instead.
//
// Offline, on a mesh network, or when GitHub cannot be reached, the check
// fails silently. Set STRANGE_DOMAIN_NO_UPDATE_CHECK=1 to turn it off.
// STRANGE_DOMAIN_UPDATE_FEED (a generic electron-updater feed URL) and
// STRANGE_DOMAIN_UPDATE_API (a URL answering like GitHub's latest-release
// API) point the check elsewhere for testing.
"use strict";

const { app, dialog, net, shell } = require("electron");
const { LATEST_API, RELEASES_PAGE, isNewer, updateMode, releasePage, dialogText } = require("./update-logic");

const DELAY_MS = 5000;

function log(...args) {
  if (process.env.STRANGE_DOMAIN_UPDATE_DEBUG) console.log("[update]", ...args);
}

async function ask(getWindow, text) {
  const win = getWindow();
  const opts = { type: "info", ...text, defaultId: 0, cancelId: 1, noLink: true };
  const { response } = win && !win.isDestroyed() ? await dialog.showMessageBox(win, opts) : await dialog.showMessageBox(opts);
  return response === 0;
}

// ---- "page": tell the person and open the release page ----

async function latestRelease() {
  const url = process.env.STRANGE_DOMAIN_UPDATE_API || LATEST_API;
  const ctl = new AbortController();
  const timer = setTimeout(() => ctl.abort(), 10000);
  try {
    const res = await net.fetch(url, {
      headers: { Accept: "application/vnd.github+json", "User-Agent": "the-strange-domain-desktop/" + app.getVersion() },
      signal: ctl.signal,
    });
    if (!res.ok) throw new Error("HTTP " + res.status);
    return await res.json();
  } finally {
    clearTimeout(timer);
  }
}

async function checkPage(getWindow) {
  const rel = await latestRelease();
  const current = app.getVersion();
  log("latest", rel && rel.tag_name, "current", current);
  if (!rel || rel.draft || rel.prerelease || !isNewer(rel.tag_name, current)) return;
  if (await ask(getWindow, dialogText("page", rel.tag_name, current))) {
    void shell.openExternal(releasePage(rel));
  }
}

// ---- "install": electron-updater downloads and installs ----

function checkInstall(getWindow) {
  const { autoUpdater } = require("electron-updater");
  autoUpdater.autoDownload = false;
  autoUpdater.autoInstallOnAppQuit = false;
  autoUpdater.allowPrerelease = false;
  autoUpdater.logger = process.env.STRANGE_DOMAIN_UPDATE_DEBUG ? console : null;
  if (process.env.STRANGE_DOMAIN_UPDATE_FEED) {
    autoUpdater.setFeedURL({ provider: "generic", url: process.env.STRANGE_DOMAIN_UPDATE_FEED });
    if (!app.isPackaged) autoUpdater.forceDevUpdateConfig = true;
  }

  let accepted = false;
  const progress = (f) => {
    const win = getWindow();
    if (win && !win.isDestroyed()) win.setProgressBar(f);
  };

  autoUpdater.on("update-available", async (info) => {
    const current = app.getVersion();
    log("available", info.version, "current", current);
    if (!isNewer(info.version, current)) return;
    if (!(await ask(getWindow, dialogText("install", info.version, current)))) return;
    accepted = true;
    progress(2); // indeterminate until the first progress event
    autoUpdater.downloadUpdate().catch(() => {}); // failures arrive as "error"
  });
  autoUpdater.on("download-progress", (p) => progress(Math.max(0, Math.min(1, p.percent / 100))));
  autoUpdater.on("update-downloaded", () => {
    log("downloaded; installing");
    progress(-1);
    // Not silent (the Windows installer shows its progress), then restart.
    autoUpdater.quitAndInstall(false, true);
  });
  autoUpdater.on("error", async (err) => {
    log("error", err && err.message);
    if (!accepted) return; // the check itself failed: stay quiet
    accepted = false;
    progress(-1);
    const open = await ask(getWindow, {
      title: "Update failed",
      message: "The update could not be downloaded or installed.",
      detail: "You can download the new version from the release page instead.",
      buttons: ["Open download page", "Close"],
    });
    if (open) void shell.openExternal(RELEASES_PAGE);
  });

  return autoUpdater.checkForUpdates();
}

/** Starts the one update check for this run of the app. */
function scheduleUpdateCheck(getWindow) {
  if (process.env.STRANGE_DOMAIN_NO_UPDATE_CHECK) return;
  const testing = !!(process.env.STRANGE_DOMAIN_UPDATE_FEED || process.env.STRANGE_DOMAIN_UPDATE_API);
  if (!app.isPackaged && !testing) return; // `npm start` during development
  setTimeout(() => {
    const mode = updateMode(process.platform, process.env);
    log("mode", mode);
    const run = mode === "install" ? checkInstall : checkPage;
    Promise.resolve()
      .then(() => run(getWindow))
      .catch((e) => log("check failed", e && e.message));
  }, DELAY_MS);
}

module.exports = { scheduleUpdateCheck };
