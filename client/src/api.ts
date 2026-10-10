// Typed client for the Domain Node's JSON API (internal/server/*.go).

import { apiUrl } from "./node";

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

let token: string | null = null;
let onUnauthorized: (() => void) | null = null;

export function setToken(t: string | null) {
  token = t;
}
export function getToken() {
  return token;
}
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn;
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (token) headers["Authorization"] = "Bearer " + token;
  const res = await fetch(apiUrl(path), {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) {
    let msg = res.statusText;
    try {
      msg = (await res.json()).error ?? msg;
    } catch {
      /* not JSON */
    }
    if (res.status === 401 && token && onUnauthorized) onUnauthorized();
    throw new ApiError(res.status, msg);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

// ---- types (mirror the Go JSON) ----

export interface Info {
  name: string;
  version: string;
  platform: string;
  uptime_s: number;
  /** Absent on v1.0.0 nodes (API level 1). See compat.ts. */
  api?: number;
  min_client_api?: number;
  features?: string[];
}
export interface Me {
  user_id: string;
  device_id: string;
  callsign: string;
  is_node_admin: boolean;
  /** Profile picture version; absent when there is none. */
  avatar?: number;
}
export interface Domain {
  id: string;
  name: string;
  owner_id: string;
}
export interface Role {
  id: string;
  name: string;
  rank: number;
  permissions: number;
}
export interface Member {
  user_id: string;
  callsign: string;
  role_id: string;
  muted: boolean;
  avatar?: number;
}
export interface Channel {
  id: string;
  name: string;
  kind: "text" | "voice";
}
export interface DomainDetail {
  roles: Role[];
  members: Member[];
  channels: Channel[];
}
/** An active ban (GET /domains/{id}/bans, node feature "bans"). */
export interface Ban {
  user_id: string;
  callsign: string;
  banned_by: string;
  reason: string;
  banned_at: number;
  /** Unix seconds; 0 means permanent. */
  expires_at: number;
  /** The banned person's rank when banned; only higher ranks may lift it. */
  former_rank: number;
}
export interface Device {
  id: string;
  name: string;
  public_key: string;
  created_at: number;
}
export interface Conclave {
  id: string;
  created_by: string;
  created_at: number;
  members: { user_id: string; callsign: string; avatar?: number }[];
}
export interface WireMessage {
  seq: number;
  group_id: string;
  sender_user: string;
  sender_device: string;
  kind: "application" | "proposal" | "commit";
  epoch: number;
  data?: string;
  deleted?: boolean;
  created_at: number;
}
export interface WireWelcome {
  id: number;
  group_id: string;
  data: string;
}
export interface GroupDevice {
  user_id: string;
  device_id: string;
}
export interface Limits {
  max_upload_bytes: number;
  quota_bytes: number;
  used_bytes: number;
  max_files: number;
  max_avatar_bytes: number;
}
export interface ClaimedKeyPackage {
  device_id: string;
  key_package: string;
}

// Permission bits (internal/perm/perm.go).
/** One person in a call (internal/rtc PartInfo). */
export interface CallPart {
  id: string;
  user_id: string;
  device_id: string;
  muted: boolean;
  camera: boolean;
  screen: boolean;
  can_speak: boolean;
  joined: number;
}

/** A running call: a Voice Relay, or a call in a Confession or Conclave. */
export interface CallRoom {
  id: string;
  relay: boolean;
  started_at: number;
  started_by: string;
  ringing: boolean;
  video: boolean;
  declined: string[];
  participants: CallPart[];
}

export const Perm = {
  ManageDomain: 1 << 0,
  ManageChannels: 1 << 1,
  ManageRoles: 1 << 2,
  CreateInvite: 1 << 3,
  Kick: 1 << 4,
  Ban: 1 << 5,
  Mute: 1 << 6,
  DeleteMessages: 1 << 7,
  PinMessages: 1 << 8,
  SendMessages: 1 << 9,
  JoinVoice: 1 << 10,
} as const;
export const RANK_OWNER = 1000;
export const RANK_MEMBER = 100;

// ---- endpoints ----

export const api = {
  info: () => call<Info>("GET", "/info"),

  register: (callsign: string, device_name: string, public_key: string, summons: string) =>
    call<{ user_id: string; device_id: string; is_node_admin: boolean }>("POST", "/register", {
      callsign,
      device_name,
      public_key,
      summons,
    }),
  challenge: (device_id: string) => call<{ nonce: string }>("POST", "/auth/challenge", { device_id }),
  verify: (device_id: string, nonce: string, signature: string) =>
    call<{ token: string }>("POST", "/auth/verify", { device_id, nonce, signature }),
  signOut: () => call<void>("POST", "/auth/signout"),
  me: () => call<Me>("GET", "/me"),

  domains: () => call<Domain[]>("GET", "/domains"),
  createDomain: (name: string) => call<Domain>("POST", "/domains", { name }),
  domain: (id: string) => call<DomainDetail>("GET", `/domains/${id}`),
  createChannel: (id: string, name: string, kind: "text" | "voice") =>
    call<Channel>("POST", `/domains/${id}/channels`, { name, kind }),
  createSummons: (id: string, max_uses: number, ttl_seconds: number) =>
    call<{ summons: string }>("POST", `/domains/${id}/summons`, { max_uses, ttl_seconds }),
  acceptSummons: (token: string) => call<Domain>("POST", `/summons/${encodeURIComponent(token)}/accept`),
  assignRole: (id: string, uid: string, role_id: string) =>
    call<void>("POST", `/domains/${id}/members/${uid}/role`, { role_id }),
  mute: (id: string, uid: string, on: boolean) =>
    call<void>("POST", `/domains/${id}/members/${uid}/${on ? "mute" : "unmute"}`),
  kick: (id: string, uid: string) => call<void>("POST", `/domains/${id}/members/${uid}/kick`),
  /** seconds: 0 bans permanently; undefined sends no duration (nodes without "bans"). */
  ban: (id: string, uid: string, reason: string, seconds?: number) =>
    call<void>(
      "POST",
      `/domains/${id}/members/${uid}/ban`,
      seconds === undefined ? { reason } : { reason, duration_seconds: seconds },
    ),
  bans: (id: string) => call<Ban[]>("GET", `/domains/${id}/bans`),
  unban: (id: string, uid: string) => call<void>("DELETE", `/domains/${id}/bans/${uid}`),

  devices: () => call<Device[]>("GET", "/devices"),
  linkCode: () => call<{ code: string; expires_in_seconds: number }>("POST", "/devices/link-code"),
  link: (code: string, device_name: string, public_key: string) =>
    call<{ user_id: string; device_id: string; callsign: string }>("POST", "/devices/link", {
      code,
      device_name,
      public_key,
    }),
  revokeDevice: (id: string) => call<void>("DELETE", `/devices/${id}`),
  setRecoveryKey: (public_key: string) => call<void>("PUT", "/account/recovery-key", { public_key }),
  recoveryChallenge: (callsign: string) => call<{ nonce: string }>("POST", "/recover/challenge", { callsign }),
  recover: (callsign: string, nonce: string, signature: string, device_name: string, public_key: string) =>
    call<{ user_id: string; device_id: string; callsign: string }>("POST", "/recover", {
      callsign,
      nonce,
      signature,
      device_name,
      public_key,
    }),

  conclaves: () => call<Conclave[]>("GET", "/conclaves"),
  createConclave: (member_ids: string[]) => call<{ id: string }>("POST", "/conclaves", { member_ids }),
  leaveConclave: (id: string) => call<void>("DELETE", `/conclaves/${id}/members/me`),

  publishKeyPackages: (key_packages: string[]) => call<void>("POST", "/keypackages", { key_packages }),
  claimKeyPackages: (uid: string) => call<ClaimedKeyPackage[]>("POST", `/users/${uid}/keypackages/claim`),
  groupDevices: (gid: string) => call<GroupDevice[]>("GET", `/groups/${gid}/devices`),
  epoch: (gid: string) => call<{ epoch: number }>("GET", `/groups/${gid}/epoch`),
  messages: (gid: string, after: number, limit = 200) =>
    call<WireMessage[]>("GET", `/groups/${gid}/messages?after=${after}&limit=${limit}`),
  send: (gid: string, kind: "application" | "commit", epoch: number, data: string, blobs?: string[]) =>
    call<{ seq: number }>("POST", `/groups/${gid}/messages`, blobs?.length ? { kind, epoch, data, blobs } : { kind, epoch, data }),
  deleteMessage: (gid: string, seq: number) => call<void>("DELETE", `/groups/${gid}/messages/${seq}`),
  sendWelcome: (gid: string, device_id: string, data: string) =>
    call<void>("POST", `/groups/${gid}/welcomes`, { device_id, data }),
  takeWelcomes: () => call<WireWelcome[]>("POST", "/welcomes/take"),

  calls: () => call<CallRoom[]>("GET", "/calls"),
  declineCall: (gid: string) => call<void>("POST", `/calls/${gid}/decline`),

  limits: () => call<Limits>("GET", "/limits"),
  /** Uploads an encrypted attachment (ciphertext only) for a group. */
  uploadBlob: (gid: string, ciphertext: Uint8Array) =>
    callRaw<{ id: string; size: number }>("POST", `/groups/${gid}/blobs`, ciphertext as Uint8Array<ArrayBuffer>, "application/octet-stream"),
  fetchBlob: (id: string) => fetchBytes(`/blobs/${encodeURIComponent(id)}`),
  setAvatar: (image: Blob) => callRaw<{ avatar: number }>("PUT", "/account/avatar", image, image.type),
  deleteAvatar: () => call<void>("DELETE", "/account/avatar"),
  fetchAvatar: (uid: string, version: number) => fetchBlobBody(`/users/${encodeURIComponent(uid)}/avatar?v=${version}`),
};

async function failed(res: Response): Promise<never> {
  let msg = res.statusText;
  try {
    msg = (await res.json()).error ?? msg;
  } catch {
    /* not JSON */
  }
  if (res.status === 401 && token && onUnauthorized) onUnauthorized();
  throw new ApiError(res.status, msg);
}

function authHeaders(): Record<string, string> {
  return token ? { Authorization: "Bearer " + token } : {};
}

async function callRaw<T>(method: string, path: string, body: BodyInit, type: string): Promise<T> {
  const res = await fetch(apiUrl(path), { method, headers: { ...authHeaders(), "Content-Type": type }, body });
  if (!res.ok) return failed(res);
  return (await res.json()) as T;
}

async function fetchBytes(path: string): Promise<Uint8Array> {
  const res = await fetch(apiUrl(path), { headers: authHeaders() });
  if (!res.ok) return failed(res);
  return new Uint8Array(await res.arrayBuffer());
}

async function fetchBlobBody(path: string): Promise<Blob> {
  const res = await fetch(apiUrl(path), { headers: authHeaders() });
  if (!res.ok) return failed(res);
  return await res.blob();
}
