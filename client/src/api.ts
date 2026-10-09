// Typed client for the Domain Node's JSON API (internal/server/*.go).

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
  const res = await fetch("/api/v1" + path, {
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
}
export interface Me {
  user_id: string;
  device_id: string;
  callsign: string;
  is_node_admin: boolean;
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
  members: { user_id: string; callsign: string }[];
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
export interface ClaimedKeyPackage {
  device_id: string;
  key_package: string;
}

// Permission bits (internal/perm/perm.go).
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
  ban: (id: string, uid: string, reason: string) =>
    call<void>("POST", `/domains/${id}/members/${uid}/ban`, { reason }),

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
  send: (gid: string, kind: "application" | "commit", epoch: number, data: string) =>
    call<{ seq: number }>("POST", `/groups/${gid}/messages`, { kind, epoch, data }),
  deleteMessage: (gid: string, seq: number) => call<void>("DELETE", `/groups/${gid}/messages/${seq}`),
  sendWelcome: (gid: string, device_id: string, data: string) =>
    call<void>("POST", `/groups/${gid}/welcomes`, { device_id, data }),
  takeWelcomes: () => call<WireWelcome[]>("POST", "/welcomes/take"),
};
