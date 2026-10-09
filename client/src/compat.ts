// Whether this client and the node it talks to match.
//
// The browser and desktop app load the client from the node, so the two
// always match. The phone app bundles its own copy of the client and can be
// pointed at a node that is older or newer than it is. The node reports what
// it can do in GET /api/v1/info (see internal/server/compat.go):
//
//   - api: the node's API level. Nodes that do not report it are level 1.
//   - features: optional capabilities by name. Missing names turn off the
//     matching controls here.
//   - min_client_api: the oldest client API level the node works with.
//
// The node never refuses older clients; this only decides what to show.

import type { Info } from "./api";

/** This client's API level (matches APILevel in compat.go when built together). */
export const CLIENT_API = 3;

export type Feature = "attachments" | "avatars" | "calls" | "video";

/** Features this client uses; a node without one of them is "too old". */
const WANTED: Feature[] = ["attachments", "avatars"];

export type CompatStatus = "ok" | "node-too-old" | "app-too-old";

export interface Compat {
  api: number;
  minClientApi: number;
  features: ReadonlySet<string>;
}

/**
 * What the node supports. With no info yet (the node could not be reached),
 * nothing is turned off: there is nothing to compare against.
 */
export function readCompat(info: Info | undefined): Compat | null {
  if (!info) return null;
  const api = typeof info.api === "number" ? info.api : 1;
  const minClientApi = typeof info.min_client_api === "number" ? info.min_client_api : 1;
  const features = new Set(Array.isArray(info.features) ? info.features.filter((f) => typeof f === "string") : []);
  return { api, minClientApi, features };
}

/** Whether the node supports feature. True while the node's info is unknown. */
export function has(info: Info | undefined, feature: Feature): boolean {
  const c = readCompat(info);
  return !c || c.features.has(feature);
}

export function compatStatus(info: Info | undefined): CompatStatus {
  const c = readCompat(info);
  if (!c) return "ok";
  if (CLIENT_API < c.minClientApi) return "app-too-old";
  if (c.api < CLIENT_API || WANTED.some((f) => !c.features.has(f))) return "node-too-old";
  return "ok";
}

/** The note shown in the phone app when the two do not match; empty when they do. */
export function compatMessage(status: CompatStatus): string {
  if (status === "node-too-old") return "This node runs an older version. Some features are turned off until it is updated.";
  if (status === "app-too-old") return "This app is older than the node. Update the app to keep everything working.";
  return "";
}

/** Tooltip for a control turned off because the node lacks it. */
export const OLD_NODE_NOTE = "This node runs an older version and does not support this yet.";
