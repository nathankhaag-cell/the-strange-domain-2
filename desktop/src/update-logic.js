// Pure helpers for the update check (src/updates.js), kept free of Electron
// so they can be tested with plain Node.
"use strict";

const REPO = "nathankhaag-cell/the-strange-domain-2";
const LATEST_API = `https://api.github.com/repos/${REPO}/releases/latest`;
const RELEASES_PAGE = `https://github.com/${REPO}/releases/latest`;

/** Parses "v1.2.3", "1.2.3-beta.1" (build metadata ignored); null if not semver. */
function parseVersion(s) {
  const m = /^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z.-]+))?(?:\+.*)?$/.exec(String(s).trim());
  return m ? { nums: [Number(m[1]), Number(m[2]), Number(m[3])], pre: m[4] || "" } : null;
}

/** Semver precedence of two parsed versions: -1, 0 or 1. */
function compareVersions(a, b) {
  for (let i = 0; i < 3; i++) if (a.nums[i] !== b.nums[i]) return a.nums[i] < b.nums[i] ? -1 : 1;
  if (a.pre === b.pre) return 0;
  if (!a.pre) return 1;
  if (!b.pre) return -1;
  const ap = a.pre.split(".");
  const bp = b.pre.split(".");
  for (let i = 0; i < Math.min(ap.length, bp.length); i++) {
    if (ap[i] === bp[i]) continue;
    const an = /^\d+$/.test(ap[i]);
    const bn = /^\d+$/.test(bp[i]);
    if (an && bn) return Number(ap[i]) < Number(bp[i]) ? -1 : 1;
    if (an) return -1;
    if (bn) return 1;
    return ap[i] < bp[i] ? -1 : 1;
  }
  return ap.length === bp.length ? 0 : ap.length < bp.length ? -1 : 1;
}

/** True if latest (a tag such as v1.2.0) is newer than current. */
function isNewer(latest, current) {
  const l = parseVersion(latest);
  const c = parseVersion(current);
  return !!l && !!c && compareVersions(l, c) > 0;
}

/**
 * How this copy of the app can be updated:
 *   "install" - electron-updater can download and install it (the Windows
 *               installer, or an AppImage on Linux);
 *   "page"    - the person downloads it (the .deb, and macOS, where an
 *               unsigned app cannot update itself).
 */
function updateMode(platform, env) {
  if (platform === "win32") return "install";
  if (platform === "linux" && env.APPIMAGE) return "install";
  return "page";
}

/** Picks the release page from GitHub's answer, falling back to /latest. */
function releasePage(release) {
  const u = release && release.html_url;
  return typeof u === "string" && u.startsWith(`https://github.com/${REPO}/`) ? u : RELEASES_PAGE;
}

/** Strings for the update dialogs. Plain wording for Nathan to approve. */
function dialogText(mode, latest, current) {
  const v = (s) => "v" + String(s).replace(/^v/, "");
  const message = `A new version of The Strange Domain is available (${v(latest)}). You have ${v(current)}.`;
  if (mode === "install") {
    return {
      title: "Update available",
      message,
      detail: "The app will download the update, then close and restart.",
      buttons: ["Update and restart", "Later"],
    };
  }
  return {
    title: "Update available",
    message,
    detail: "Download it from the release page and install it over this version.",
    buttons: ["Open download page", "Later"],
  };
}

module.exports = { LATEST_API, RELEASES_PAGE, parseVersion, compareVersions, isNewer, updateMode, releasePage, dialogText };
