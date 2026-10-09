// Where the node is.
//
// In a browser, and in the desktop app (which loads the client from the
// node), the client talks to the page's own origin.
//
// The phone app bundles the client (built with `npm run build:app`) and runs
// it at http://localhost, which counts as a secure context, so it keeps the
// node's address and talks to it cross-origin; the node allows that origin
// with CORS. Keys,
// messages and the session are kept per node, because one app can be pointed
// at different nodes over time.

export const BUNDLED = import.meta.env.MODE === "app";

export const DEFAULT_PORT = "8743";
const NODE_KEY = "sd.node";
const LAST_KEY = "sd.node.last";

function readSaved(): string {
  try {
    return localStorage.getItem(NODE_KEY) ?? "";
  } catch {
    return "";
  }
}

// Fixed for the life of the page: changing node reloads it.
const base = BUNDLED ? readSaved() : "";

/** False only in the phone app before a node has been chosen. */
export function hasNode(): boolean {
  return !BUNDLED || base !== "";
}

/** The node's host for display, such as 192.168.1.20:8743. */
export function nodeHost(): string {
  return base ? new URL(base).host : location.host;
}

export function apiUrl(path: string): string {
  return base + "/api/v1" + path;
}

export function streamUrl(): string {
  const u = new URL(base || location.origin);
  return `${u.protocol === "https:" ? "wss:" : "ws:"}//${u.host}/api/v1/stream`;
}

/**
 * Appended to storage names so each node's keys, messages and session are
 * kept apart in the phone app. Empty in a browser, where the origin already
 * separates nodes.
 */
export const storageSuffix = base ? ":" + base : "";

/**
 * Turns what the person typed into an origin such as http://192.168.1.20:8743.
 * With no scheme it assumes http:// and, with no port, the node's default.
 */
export function normalizeNodeAddress(input: string): string {
  let s = input.trim();
  if (!s) throw new Error("Enter the node's address.");
  const hasScheme = /^[a-z][a-z0-9+.-]*:\/\//i.test(s);
  if (!hasScheme) s = "http://" + s;
  let u: URL;
  try {
    u = new URL(s);
  } catch {
    throw new Error("That is not a valid address.");
  }
  if (u.protocol !== "http:" && u.protocol !== "https:") throw new Error("The address must start with http:// or https://.");
  if (u.username || u.password) throw new Error("The address must not contain a user name or password.");
  if (!u.hostname) throw new Error("That is not a valid address.");
  if (!hasScheme && !u.port) u.port = DEFAULT_PORT;
  return u.origin;
}

/** Checks a node answers at origin, then saves it and reloads the page. */
export async function chooseNode(origin: string): Promise<void> {
  let ok = false;
  try {
    const res = await fetch(origin + "/api/v1/info", { cache: "no-store" });
    const info = res.ok ? await res.json() : null;
    ok = !!info && typeof info.version === "string";
  } catch {
    throw new Error(`Could not reach ${origin}. Check the address and that the node is running.`);
  }
  if (!ok) throw new Error("Something answered at that address, but it is not a Domain Node.");
  try {
    localStorage.setItem(NODE_KEY, origin);
  } catch {
    throw new Error("This app cannot save settings on this device.");
  }
  location.reload();
}

/** The node chosen before "Change node", to fill in the address field. */
export function lastNode(): string {
  try {
    return localStorage.getItem(LAST_KEY) ?? "";
  } catch {
    return "";
  }
}

/** Forgets the chosen node (keys and messages for it stay) and reloads. */
export function changeNode(): void {
  try {
    if (base) localStorage.setItem(LAST_KEY, base);
    localStorage.removeItem(NODE_KEY);
  } catch {
    /* storage blocked */
  }
  location.reload();
}
