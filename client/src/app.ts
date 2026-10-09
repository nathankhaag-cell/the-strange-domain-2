// Application state and the actions the screens call. A small observable
// store: components subscribe with useApp() and re-render on any change.

import { useEffect, useState } from "preact/hooks";
import { ed25519 } from "@noble/curves/ed25519.js";
import {
  api,
  ApiError,
  setToken,
  setUnauthorizedHandler,
  type Conclave,
  type Device,
  type Domain,
  type DomainDetail,
  type Info,
  type Me,
} from "./api";
import { fromB64, toB64 } from "./b64";
import * as idb from "./idb";
import {
  AUTH_CONTEXT,
  RECOVERY_CONTEXT,
  generateDeviceKey,
  loadIdentity,
  recoveryKeyFromCode,
  saveIdentity,
  sign,
  withContext,
  type DeviceIdentity,
  type NewKey,
} from "./keys";
import { MlsCrypto } from "./crypto/mls";
import type { MessageCrypto } from "./crypto/types";
import { Stream, type StreamEvent } from "./stream";

export type Honorific = "Brother" | "Sister";

export interface AppState {
  phase: "loading" | "auth" | "main";
  info?: Info;
  identity?: { callsign: string; deviceName: string };
  me?: Me;
  domains: Domain[];
  details: Record<string, DomainDetail>;
  conclaves: Conclave[];
  devices: Device[];
  selDomain?: string;
  selGroup?: string;
  online: boolean;
  effects: boolean;
  honorific?: Honorific;
  rev: number;
  notice?: string;
  showRecoveryHint: boolean;
}

const TOKEN_KEY = "sd.token";
const EFFECTS_KEY = "sd.effects";
const HONORIFIC_KEY = "sd.honorific";
const RECOVERY_SET_KEY = "sd.recoverySet";

function lsGet(k: string): string | null {
  try {
    return localStorage.getItem(k);
  } catch {
    return null;
  }
}
function lsSet(k: string, v: string | null) {
  try {
    if (v === null) localStorage.removeItem(k);
    else localStorage.setItem(k, v);
  } catch {
    /* storage blocked */
  }
}

const prefersReduced = typeof matchMedia !== "undefined" && matchMedia("(prefers-reduced-motion: reduce)").matches;

let state: AppState = {
  phase: "loading",
  domains: [],
  details: {},
  conclaves: [],
  devices: [],
  online: false,
  effects: lsGet(EFFECTS_KEY) ? lsGet(EFFECTS_KEY) === "on" : !prefersReduced,
  honorific: (lsGet(HONORIFIC_KEY) as Honorific | null) ?? undefined,
  rev: 0,
  showRecoveryHint: false,
};
const subs = new Set<() => void>();

export function getState() {
  return state;
}
function set(patch: Partial<AppState>) {
  state = { ...state, ...patch };
  for (const fn of subs) fn();
}

export function useApp(): AppState {
  const [, force] = useState(0);
  useEffect(() => {
    const fn = () => force((n) => n + 1);
    subs.add(fn);
    return () => {
      subs.delete(fn);
    };
  }, []);
  return state;
}

let engine: MessageCrypto | null = null;
let stream: Stream | null = null;

export function getEngine() {
  return engine;
}

// ---- preferences ----

export function setEffects(on: boolean) {
  lsSet(EFFECTS_KEY, on ? "on" : "off");
  set({ effects: on });
}

export function setHonorific(h: Honorific) {
  // Stored on this device only until the node has a field for it.
  lsSet(HONORIFIC_KEY, h);
  set({ honorific: h });
}

export function dismissRecoveryHint() {
  set({ showRecoveryHint: false });
}

export function notify(msg: string | undefined) {
  set({ notice: msg });
}

// ---- boot and sign-in ----

export async function boot() {
  try {
    set({ info: await api.info() });
  } catch {
    /* node unreachable; the auth screen shows the error on submit */
  }
  setUnauthorizedHandler(() => void endSession("Your session ended. Reconnect to continue."));
  const id = await loadIdentity();
  if (id) set({ identity: { callsign: id.callsign, deviceName: id.deviceName } });
  const token = lsGet(TOKEN_KEY);
  if (id && token) {
    setToken(token);
    try {
      await startMain();
      return;
    } catch {
      setToken(null);
      lsSet(TOKEN_KEY, null);
    }
  }
  set({ phase: "auth" });
}

async function signIn(id: { deviceId: string; privateKey?: CryptoKey; rawPrivateKey?: Uint8Array }) {
  const { nonce } = await api.challenge(id.deviceId);
  const sig = await sign(id, withContext(AUTH_CONTEXT, fromB64(nonce)));
  const { token } = await api.verify(id.deviceId, nonce, toB64(sig));
  setToken(token);
  lsSet(TOKEN_KEY, token);
}

async function adopt(key: NewKey, acct: { user_id: string; device_id: string; callsign: string }, deviceName: string) {
  const id: DeviceIdentity = {
    userId: acct.user_id,
    deviceId: acct.device_id,
    callsign: acct.callsign,
    deviceName,
    publicKey: key.publicKey,
    privateKey: key.privateKey,
    rawPrivateKey: key.rawPrivateKey,
  };
  await saveIdentity(id);
  set({ identity: { callsign: id.callsign, deviceName } });
  await signIn(id);
  await startMain();
}

async function requireFreshDevice() {
  if (await loadIdentity()) {
    throw new Error("This browser already holds a device key. Forget this device first.");
  }
}

export async function enlist(callsign: string, deviceName: string, summons: string) {
  await requireFreshDevice();
  const key = await generateDeviceKey();
  const reg = await api.register(callsign.trim(), deviceName.trim(), toB64(key.publicKey), summons.trim());
  set({ showRecoveryHint: true });
  lsSet(RECOVERY_SET_KEY, null);
  await adopt(key, { ...reg, callsign: callsign.trim() }, deviceName.trim());
}

export async function reconnect() {
  const id = await loadIdentity();
  if (!id) throw new Error("No device key in this browser.");
  await signIn(id);
  await startMain();
}

export async function linkDevice(code: string, deviceName: string) {
  await requireFreshDevice();
  const key = await generateDeviceKey();
  const acct = await api.link(code.trim(), deviceName.trim(), toB64(key.publicKey));
  await adopt(key, acct, deviceName.trim());
}

export async function recoverAccount(callsign: string, code: string, deviceName: string) {
  await requireFreshDevice();
  const rk = await recoveryKeyFromCode(code, callsign);
  const { nonce } = await api.recoveryChallenge(callsign.trim());
  const sig = ed25519.sign(withContext(RECOVERY_CONTEXT, fromB64(nonce)), rk.rawPrivateKey);
  const key = await generateDeviceKey();
  const acct = await api.recover(callsign.trim(), nonce, toB64(sig), deviceName.trim(), toB64(key.publicKey));
  lsSet(RECOVERY_SET_KEY, "1");
  await adopt(key, acct, deviceName.trim());
}

/** Wipes every key, message and setting this browser holds for the node. */
export async function forgetDevice() {
  stream?.close();
  stream = null;
  engine = null;
  await idb.clearAll();
  for (const k of [TOKEN_KEY, RECOVERY_SET_KEY]) lsSet(k, null);
  setToken(null);
  set({ identity: undefined, phase: "auth", me: undefined, domains: [], details: {}, conclaves: [], devices: [] });
}

export async function signOut() {
  try {
    await api.signOut();
  } catch {
    /* already gone */
  }
  await endSession();
}

async function endSession(notice?: string) {
  stream?.close();
  stream = null;
  engine = null;
  setToken(null);
  lsSet(TOKEN_KEY, null);
  set({ phase: "auth", me: undefined, notice });
}

// ---- main session ----

async function startMain() {
  const me = await api.me();
  set({ me, notice: undefined });
  const c = new MlsCrypto({ userId: me.user_id, deviceId: me.device_id });
  c.onChange(() => set({ rev: state.rev + 1 }));
  await c.start();
  engine = c;
  await Promise.all([refreshDomains(), refreshConclaves(), refreshDevices()]);
  if (lsGet(RECOVERY_SET_KEY) !== "1") set({ showRecoveryHint: true });
  set({ phase: "main" });
  if (!state.selDomain && state.domains[0]) await selectDomain(state.domains[0].id);
  stream = new Stream(onEvent, (online) => {
    set({ online });
    if (online) void resync();
  });
  stream.connect();
}

async function resync() {
  const c = engine;
  if (!c) return;
  try {
    await c.takeWelcomes();
    await Promise.all([refreshDomains(), refreshConclaves()]);
    for (const gid of allGroupIds()) await c.sync(gid);
  } catch (e) {
    console.warn("resync", e);
  }
}

function allGroupIds(): string[] {
  const ids: string[] = [];
  for (const d of Object.values(state.details)) for (const ch of d.channels) if (ch.kind === "text") ids.push(ch.id);
  for (const c of state.conclaves) ids.push(c.id);
  return ids;
}

export async function refreshDomains() {
  const domains = await api.domains();
  const details: Record<string, DomainDetail> = {};
  await Promise.all(
    domains.map(async (d) => {
      try {
        details[d.id] = await api.domain(d.id);
      } catch {
        /* lost access between calls */
      }
    }),
  );
  let { selDomain, selGroup } = state;
  if (selDomain && !details[selDomain]) {
    selDomain = domains[0]?.id;
    selGroup = undefined;
  }
  set({ domains, details, selDomain, selGroup });
}

async function refreshDetail(domainId: string): Promise<DomainDetail | undefined> {
  try {
    const d = await api.domain(domainId);
    set({ details: { ...state.details, [domainId]: d } });
    return d;
  } catch (e) {
    if (e instanceof ApiError && (e.status === 403 || e.status === 404)) {
      await refreshDomains();
      return undefined;
    }
    throw e;
  }
}

export async function refreshConclaves() {
  set({ conclaves: await api.conclaves() });
}

export async function refreshDevices() {
  set({ devices: await api.devices() });
}

// ---- live events ----

const reconcileTimers = new Map<string, number>();

/** Reconciles a group after a short random delay so members don't all race. */
function scheduleReconcile(gid: string) {
  if (reconcileTimers.has(gid)) return;
  const t = window.setTimeout(() => {
    reconcileTimers.delete(gid);
    engine?.reconcile(gid).catch((e) => console.warn("reconcile", gid, e));
  }, 200 + Math.random() * 1200);
  reconcileTimers.set(gid, t);
}

async function onEvent(ev: StreamEvent) {
  const c = engine;
  if (!c) return;
  try {
    switch (ev.type) {
      case "message":
        if (ev.group_id) await c.sync(ev.group_id);
        break;
      case "deleted":
        if (ev.group_id && ev.seq) await c.markDeleted(ev.group_id, ev.seq);
        break;
      case "welcome":
        await c.takeWelcomes();
        break;
      case "domain": {
        const known = state.domains.some((d) => d.id === ev.group_id);
        if (!known || !(await refreshDetail(ev.group_id!))) await refreshDomains();
        const d = state.details[ev.group_id!];
        if (d) for (const ch of d.channels) if (ch.kind === "text") scheduleReconcile(ch.id);
        break;
      }
      case "conclave":
        await refreshConclaves();
        if (state.conclaves.some((x) => x.id === ev.group_id)) scheduleReconcile(ev.group_id!);
        break;
      case "keys":
        if (ev.group_id === state.me?.user_id) await refreshDevices();
        for (const gid of allGroupIds()) {
          if (c.status(gid) === "ready") scheduleReconcile(gid);
        }
        break;
      case "devices":
      case "device_linked":
      case "device_revoked":
        if (ev.type !== "devices" || ev.group_id === state.me?.user_id) await refreshDevices();
        for (const gid of allGroupIds()) if (c.status(gid) === "ready") scheduleReconcile(gid);
        break;
    }
  } catch (e) {
    console.warn("event", ev, e);
  }
}

// ---- navigation ----

export async function selectDomain(id: string) {
  const d = state.details[id];
  const first = d?.channels.find((c) => c.kind === "text");
  set({ selDomain: id, selGroup: first?.id });
  if (first) await openGroup(first.id);
}

export async function openGroup(gid: string) {
  set({ selGroup: gid });
  const c = engine;
  if (!c) return;
  try {
    await c.sync(gid);
    if (c.status(gid) === "ready") await c.reconcile(gid);
  } catch (e) {
    console.warn("open group", e);
  }
}

// ---- actions ----

export async function createDomain(name: string) {
  const d = await api.createDomain(name.trim());
  await refreshDomains();
  await selectDomain(d.id);
}

export async function acceptSummons(code: string) {
  const d = await api.acceptSummons(code.trim());
  await refreshDomains();
  await selectDomain(d.id);
}

export async function createChannel(domainId: string, name: string, kind: "text" | "voice") {
  const ch = await api.createChannel(domainId, name.trim(), kind);
  await refreshDetail(domainId);
  if (kind === "text") await openGroup(ch.id);
}

export async function createConclave(memberIds: string[]) {
  const { id } = await api.createConclave(memberIds);
  await refreshConclaves();
  await openGroup(id);
}

export async function sendMessage(gid: string, text: string) {
  if (!engine) throw new Error("not signed in");
  await engine.send(gid, text);
}

export async function deleteMessage(gid: string, seq: number) {
  await api.deleteMessage(gid, seq);
  await engine?.markDeleted(gid, seq);
}

export async function moderate(domainId: string, action: () => Promise<void>) {
  await action();
  await refreshDetail(domainId);
}

export async function setRecoveryCode(code: string) {
  const callsign = state.me?.callsign;
  if (!callsign) throw new Error("not signed in");
  const rk = await recoveryKeyFromCode(code, callsign);
  await api.setRecoveryKey(toB64(rk.publicKey));
  lsSet(RECOVERY_SET_KEY, "1");
  set({ showRecoveryHint: false });
}

export async function revokeDevice(id: string) {
  await api.revokeDevice(id);
  if (id === state.me?.device_id) {
    await forgetDevice();
    return;
  }
  await refreshDevices();
  for (const gid of allGroupIds()) if (engine?.status(gid) === "ready") scheduleReconcile(gid);
}
