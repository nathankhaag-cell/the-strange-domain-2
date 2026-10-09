// The phone app's update check. Apps installed outside an app store cannot
// replace themselves, so on start the app asks GitHub for the latest release
// and, if it is newer, shows a bar with a link to download the new APK. The
// person installs it themselves. Any failure (offline, a mesh network,
// GitHub unreachable) is silent.
//
// Only the bundled phone app does this. In a browser and in the desktop app
// the client is served by the node, and the desktop app checks on its own.

import { useEffect, useState } from "preact/hooks";
import { BUNDLED } from "../node";
import "./admin.css";

declare const __APP_VERSION__: string;

const LATEST = "https://api.github.com/repos/nathankhaag-cell/the-strange-domain-2/releases/latest";

type Ver = [number, number, number, string];

export function parseVersion(s: string): Ver | null {
  const m = /^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z.-]+))?(?:\+.*)?$/.exec(s.trim());
  return m ? [Number(m[1]), Number(m[2]), Number(m[3]), m[4] ?? ""] : null;
}

/** Semver precedence: -1, 0 or 1. */
export function compareVersions(a: Ver, b: Ver): number {
  for (let i = 0; i < 3; i++) if (a[i] !== b[i]) return (a[i] as number) < (b[i] as number) ? -1 : 1;
  if (a[3] === b[3]) return 0;
  if (!a[3]) return 1;
  if (!b[3]) return -1;
  const ap = a[3].split("."),
    bp = b[3].split(".");
  for (let i = 0; i < Math.min(ap.length, bp.length); i++) {
    if (ap[i] === bp[i]) continue;
    const an = /^\d+$/.test(ap[i]),
      bn = /^\d+$/.test(bp[i]);
    if (an && bn) return Number(ap[i]) < Number(bp[i]) ? -1 : 1;
    if (an) return -1;
    if (bn) return 1;
    return ap[i] < bp[i] ? -1 : 1;
  }
  return ap.length === bp.length ? 0 : ap.length < bp.length ? -1 : 1;
}

interface Found {
  version: string;
  url: string;
}

async function check(): Promise<Found | null> {
  const current = parseVersion(__APP_VERSION__);
  if (!current) return null; // a development build
  const ctl = new AbortController();
  const timer = setTimeout(() => ctl.abort(), 10000);
  try {
    const res = await fetch(LATEST, { headers: { Accept: "application/vnd.github+json" }, signal: ctl.signal });
    if (!res.ok) return null;
    const rel = await res.json();
    const latest = typeof rel.tag_name === "string" ? parseVersion(rel.tag_name) : null;
    if (!latest || rel.draft || rel.prerelease || compareVersions(latest, current) <= 0) return null;
    const ok = (u: unknown): u is string => typeof u === "string" && u.startsWith("https://github.com/");
    const apk = Array.isArray(rel.assets)
      ? rel.assets.find((a: { name?: unknown }) => typeof a.name === "string" && a.name.endsWith(".apk"))
      : undefined;
    const url = ok(apk?.browser_download_url) ? apk.browser_download_url : ok(rel.html_url) ? rel.html_url : null;
    return url ? { version: rel.tag_name, url } : null;
  } catch {
    return null;
  } finally {
    clearTimeout(timer);
  }
}

export function AppUpdate() {
  const [found, setFound] = useState<Found | null>(null);
  useEffect(() => {
    if (!BUNDLED) return;
    void check().then(setFound);
  }, []);
  if (!found) return null;
  return (
    <div class="banner app-update" role="status">
      <span>A new version of the app is available ({found.version}). This app is v{__APP_VERSION__.replace(/^v/, "")}.</span>
      <a class="btn-small accent" href={found.url}>
        Download
      </a>
      <button type="button" class="btn-small" onClick={() => setFound(null)}>
        Later
      </button>
    </div>
  );
}
