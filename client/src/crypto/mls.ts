// End-to-end encryption with MLS (RFC 9420) via ts-mls.
//
// One MLS group per Chapel or Conclave; the MLS group id is the node's group
// id. Each device is one leaf, with a basic credential "<user_id>:<device_id>".
//
// The node orders messages and accepts one commit per epoch, and because every
// commit goes through it, the node's epoch and the MLS epoch stay equal. The
// first device to commit at epoch 0 creates the group. A commit that loses a
// race comes back 409: we fetch what won, apply it and try again.
//
// MLS keys are single use, so a device can decrypt each message exactly once
// and cannot decrypt its own. Decrypted text is kept in IndexedDB on this
// device only, along with the group state after every message.

import {
  acceptAll,
  createApplicationMessage,
  createCommit,
  createGroup,
  decodeGroupState,
  decodeMlsMessage,
  defaultCapabilities,
  defaultLifetime,
  emptyPskIndex,
  encodeGroupState,
  encodeMlsMessage,
  generateKeyPackage,
  getCiphersuiteFromName,
  getCiphersuiteImpl,
  joinGroup,
  processMessage,
  zeroOutUint8Array,
  type CiphersuiteImpl,
  type ClientConfig,
  type ClientState,
  type Credential,
  type KeyPackage,
  type PrivateKeyPackage,
  type Proposal,
} from "ts-mls";
import { defaultClientConfig } from "ts-mls/clientConfig.js";
import { makeKeyPackageRef } from "ts-mls/keyPackage.js";
import { api, ApiError, type GroupDevice, type WireMessage } from "../api";
import { fromB64, fromUtf8, toB64, toHex, utf8 } from "../b64";
import * as idb from "../idb";
import { parseFileRef, type FileRef } from "../files";
import type { GroupStatus, MessageCrypto, ShownMessage } from "./types";

const SUITE = "MLS_128_DHKEMX25519_AES128GCM_SHA256_Ed25519" as const;
const FETCH_LIMIT = 200;
const KEYPACKAGE_TTL_MS = 60 * 24 * 3600 * 1000;

interface GroupRec {
  loaded: boolean;
  state?: ClientState;
  lastSeq: number;
  joinedEpoch: number;
  /** Seq to go back to once this device joins: messages after it were seen without group state. */
  rewindTo?: number;
  status: GroupStatus;
  messages: Map<number, ShownMessage>;
}

interface StoredGroup {
  state?: Uint8Array;
  lastSeq: number;
  joinedEpoch: number;
  rewindTo?: number;
}

interface StoredKeyPackage {
  publicPackage: Uint8Array; // encoded MLSMessage(KeyPackage)
  privatePackage: PrivateKeyPackage;
  createdAt: number;
}

export class NotReadyError extends Error {}

const plainKey = (gid: string, seq: number) => `${gid}:${String(seq).padStart(15, "0")}`;

function parseIdentity(c: Credential): { userId: string; deviceId: string } | undefined {
  if (c.credentialType !== "basic") return undefined;
  const parts = fromUtf8.decode(c.identity).split(":");
  if (parts.length !== 2 || !parts[0] || !parts[1]) return undefined;
  return { userId: parts[0], deviceId: parts[1] };
}

interface Leaf {
  leafIndex: number;
  userId: string;
  deviceId: string;
}

function leaves(state: ClientState): Leaf[] {
  const out: Leaf[] = [];
  state.ratchetTree.forEach((n, i) => {
    if (n && n.nodeType === "leaf") {
      const id = parseIdentity(n.leaf.credential);
      if (id) out.push({ leafIndex: i / 2, ...id });
    }
  });
  return out;
}

export class MlsCrypto implements MessageCrypto {
  private cs!: CiphersuiteImpl;
  private config: ClientConfig;
  private groups = new Map<string, GroupRec>();
  private locks = new Map<string, Promise<unknown>>();
  private listeners: ((gid: string) => void)[] = [];
  private incoming: ((m: ShownMessage) => void)[] = [];

  constructor(private me: { userId: string; deviceId: string }) {
    this.config = {
      ...defaultClientConfig,
      authService: {
        // Credentials must name a user and device. Binding them to the
        // device keys the node knows about is a follow-up (see README).
        async validateCredential(c: Credential) {
          return parseIdentity(c) !== undefined;
        },
      },
    };
  }

  onChange(fn: (gid: string) => void) {
    this.listeners.push(fn);
  }

  onIncoming(fn: (m: ShownMessage) => void) {
    this.incoming.push(fn);
  }

  private emit(gid: string) {
    for (const fn of this.listeners) fn(gid);
  }

  /** Runs fn after any earlier work on the same key has finished. */
  private lock<T>(key: string, fn: () => Promise<T>): Promise<T> {
    const prev = this.locks.get(key) ?? Promise.resolve();
    const next = prev.then(fn, fn);
    this.locks.set(
      key,
      next.catch(() => undefined),
    );
    return next;
  }

  async start() {
    this.cs = await getCiphersuiteImpl(getCiphersuiteFromName(SUITE));
    await this.replenishKeyPackages();
  }

  private credential(): Credential {
    return { credentialType: "basic", identity: utf8.encode(`${this.me.userId}:${this.me.deviceId}`) };
  }

  private newKeyPackage() {
    return generateKeyPackage(this.credential(), defaultCapabilities(), defaultLifetime, [], this.cs);
  }

  /**
   * Publishes fresh KeyPackages. KeyPackages claimed by a device that then
   * lost a commit race are never used, so we can't count the node's supply
   * from here; publish a few every start and more when the local pool is low.
   */
  private async replenishKeyPackages() {
    const now = Date.now();
    const existing = await idb.entries<StoredKeyPackage>("keypackages");
    for (const [k, v] of existing) {
      if (now - v.createdAt > KEYPACKAGE_TTL_MS) await idb.del("keypackages", k);
    }
    const n = existing.length < 20 ? 20 : 5;
    const made: { ref: string; bytes: Uint8Array }[] = [];
    for (let i = 0; i < n; i++) {
      const kp = await this.newKeyPackage();
      const bytes = encodeMlsMessage({ version: "mls10", wireformat: "mls_key_package", keyPackage: kp.publicPackage });
      const ref = toHex(await makeKeyPackageRef(kp.publicPackage, this.cs.hash));
      made.push({ ref, bytes });
      await idb.put("keypackages", ref, { publicPackage: bytes, privatePackage: kp.privatePackage, createdAt: now });
    }
    try {
      await api.publishKeyPackages(made.map((m) => toB64(m.bytes)));
    } catch (e) {
      // The node holds at most 100 per device; it already has plenty.
      for (const m of made) await idb.del("keypackages", m.ref);
      if (!(e instanceof ApiError && e.status === 400)) throw e;
    }
  }

  // ---- local state ----

  private async load(gid: string): Promise<GroupRec> {
    let rec = this.groups.get(gid);
    if (rec?.loaded) return rec;
    rec = { loaded: true, lastSeq: 0, joinedEpoch: 0, status: "unknown", messages: new Map() };
    const stored = await idb.get<StoredGroup>("groups", gid);
    if (stored) {
      rec.lastSeq = stored.lastSeq;
      rec.joinedEpoch = stored.joinedEpoch;
      rec.rewindTo = stored.rewindTo;
      if (stored.state) {
        const decoded = decodeGroupState(stored.state, 0);
        if (decoded) {
          rec.state = { ...decoded[0], clientConfig: this.config };
          rec.status = "ready";
        }
      }
    }
    for (const [, m] of await idb.entries<ShownMessage>("plaintext", gid + ":")) rec.messages.set(m.seq, m);
    this.groups.set(gid, rec);
    return rec;
  }

  private async persist(gid: string, rec: GroupRec, shown?: ShownMessage) {
    const g: StoredGroup = {
      state: rec.state ? encodeGroupState(rec.state) : undefined,
      lastSeq: rec.lastSeq,
      joinedEpoch: rec.joinedEpoch,
      rewindTo: rec.rewindTo,
    };
    const writes: { store: idb.StoreName; key: string; value: unknown }[] = [{ store: "groups", key: gid, value: g }];
    if (shown) writes.push({ store: "plaintext", key: plainKey(gid, shown.seq), value: shown });
    await idb.putMany(writes);
  }

  status(gid: string): GroupStatus {
    return this.groups.get(gid)?.status ?? "unknown";
  }

  messages(gid: string): ShownMessage[] {
    const rec = this.groups.get(gid);
    if (!rec) return [];
    return Array.from(rec.messages.values()).sort((a, b) => a.seq - b.seq);
  }

  // ---- receiving ----

  sync(gid: string): Promise<void> {
    return this.lock(gid, () => this.syncLocked(gid));
  }

  private async syncLocked(gid: string) {
    const rec = await this.load(gid);
    for (;;) {
      const batch = await api.messages(gid, rec.lastSeq, FETCH_LIMIT);
      for (const m of batch) await this.apply(gid, rec, m);
      if (batch.length < FETCH_LIMIT) break;
    }
    if (rec.state) rec.status = "ready";
    else rec.status = (await api.epoch(gid)).epoch === 0 ? "empty" : "waiting";
    this.emit(gid);
  }

  private async apply(gid: string, rec: GroupRec, m: WireMessage) {
    const base = {
      seq: m.seq,
      groupId: gid,
      senderUser: m.sender_user,
      senderDevice: m.sender_device,
      createdAt: m.created_at,
    };
    let shown: ShownMessage | undefined;
    let fresh: ShownMessage | undefined;
    let consumed: Uint8Array[] = [];
    // A Welcome may arrive after the messages that follow it; remember where
    // to start again once we have group state.
    if (!rec.state && rec.rewindTo === undefined) rec.rewindTo = m.seq - 1;

    if (m.kind === "application") {
      const prev = rec.messages.get(m.seq);
      if (m.deleted || !m.data) {
        shown = { ...base, state: "redacted" };
      } else if (prev?.state === "ok") {
        // Sent from this device; we kept the text when we sent it.
      } else if (m.sender_device === this.me.deviceId || !rec.state || m.epoch < rec.joinedEpoch) {
        shown = { ...base, state: "unavailable" };
      } else {
        shown = { ...base, state: "unavailable" };
        try {
          const decoded = decodeMlsMessage(fromB64(m.data), 0)?.[0];
          if (decoded?.wireformat !== "mls_private_message") throw new Error("not a private message");
          const r = await processMessage(decoded, rec.state, emptyPskIndex, acceptAll, this.cs);
          rec.state = r.newState;
          consumed = r.consumed;
          if (r.kind === "applicationMessage") {
            const body = JSON.parse(fromUtf8.decode(r.message)) as { text?: unknown; from?: unknown; files?: unknown };
            // The node's sender metadata must match what the sender sealed inside.
            if (typeof body.text === "string" && body.from === m.sender_device) {
              const files = Array.isArray(body.files)
                ? body.files.slice(0, 10).map(parseFileRef).filter((f): f is FileRef => !!f)
                : [];
              shown = { ...base, state: "ok", text: body.text, ...(files.length ? { files } : {}) };
              fresh = shown;
            }
          }
        } catch (e) {
          console.warn("could not decrypt message", gid, m.seq, e);
        }
      }
    } else if (m.kind === "commit" && rec.state && m.data) {
      const current = Number(rec.state.groupContext.epoch);
      if (m.epoch === current) {
        try {
          const decoded = decodeMlsMessage(fromB64(m.data), 0)?.[0];
          if (decoded?.wireformat !== "mls_private_message" && decoded?.wireformat !== "mls_public_message") {
            throw new Error("not a handshake message");
          }
          const r = await processMessage(decoded, rec.state, emptyPskIndex, acceptAll, this.cs);
          consumed = r.consumed;
          if (r.newState.groupActiveState.kind === "active") {
            rec.state = r.newState;
          } else {
            rec.state = undefined; // removed from the group
          }
        } catch (e) {
          console.error("could not apply commit; waiting to be re-added", gid, m.seq, e);
          rec.state = undefined;
        }
      } else if (m.epoch > current) {
        console.error("missed a commit; waiting to be re-added", gid, m.seq);
        rec.state = undefined;
      }
      // m.epoch < current: our own commit, or one from before we joined.
    }

    rec.lastSeq = m.seq;
    if (shown) rec.messages.set(m.seq, shown);
    if (!rec.state) rec.status = "waiting";
    await this.persist(gid, rec, shown);
    for (const c of consumed) zeroOutUint8Array(c);
    if (fresh) {
      for (const fn of this.incoming) {
        try {
          fn(fresh);
        } catch (e) {
          console.warn("incoming listener", e);
        }
      }
    }
  }

  async markDeleted(gid: string, seq: number) {
    await this.lock(gid, async () => {
      const rec = await this.load(gid);
      const prev = rec.messages.get(seq);
      if (!prev) return;
      const shown: ShownMessage = { ...prev, state: "redacted", text: undefined, files: undefined };
      rec.messages.set(seq, shown);
      await idb.put("plaintext", plainKey(gid, seq), shown);
    });
    this.emit(gid);
  }

  // ---- Welcomes ----

  takeWelcomes(): Promise<string[]> {
    return this.lock("__welcomes", async () => {
      const ws = await api.takeWelcomes();
      const joined: string[] = [];
      for (const w of ws) {
        try {
          if (await this.join(w.group_id, fromB64(w.data))) joined.push(w.group_id);
        } catch (e) {
          console.error("could not join from Welcome", w.group_id, e);
        }
      }
      if (ws.length > 0) {
        const left = await idb.entries<StoredKeyPackage>("keypackages");
        if (left.length < 10) await this.replenishKeyPackages();
      }
      for (const gid of joined) await this.sync(gid);
      return joined;
    });
  }

  private async join(gid: string, data: Uint8Array): Promise<boolean> {
    const msg = decodeMlsMessage(data, 0)?.[0];
    if (msg?.wireformat !== "mls_welcome") return false;
    for (const secret of msg.welcome.secrets) {
      const ref = toHex(secret.newMember);
      const stored = await idb.get<StoredKeyPackage>("keypackages", ref);
      if (!stored) continue;
      const kpMsg = decodeMlsMessage(stored.publicPackage, 0)?.[0];
      if (kpMsg?.wireformat !== "mls_key_package") continue;
      const state = await joinGroup(
        msg.welcome,
        kpMsg.keyPackage,
        stored.privatePackage,
        emptyPskIndex,
        this.cs,
        undefined,
        undefined,
        this.config,
      );
      if (fromUtf8.decode(state.groupContext.groupId) !== gid) throw new Error("Welcome is for a different group");
      await idb.del("keypackages", ref);
      return this.lock(gid, async () => {
        const rec = await this.load(gid);
        if (rec.state && rec.state.groupContext.epoch >= state.groupContext.epoch) return false;
        rec.state = state;
        rec.joinedEpoch = Number(state.groupContext.epoch);
        rec.status = "ready";
        if (rec.rewindTo !== undefined) rec.lastSeq = Math.min(rec.lastSeq, rec.rewindTo);
        rec.rewindTo = undefined;
        await this.persist(gid, rec);
        return true;
      });
    }
    return false;
  }

  // ---- sending ----

  send(gid: string, text: string, files: FileRef[] = []): Promise<void> {
    return this.lock(gid, async () => {
      for (let attempt = 0; attempt < 4; attempt++) {
        await this.syncLocked(gid);
        const rec = await this.load(gid);
        if (!rec.state) {
          if (rec.status !== "empty") throw new NotReadyError("this device has not been added to the group yet");
          await this.commitChanges(gid, rec, true);
          continue;
        }
        const epoch = Number(rec.state.groupContext.epoch);
        const body = utf8.encode(
          JSON.stringify(files.length ? { v: 1, text, from: this.me.deviceId, files } : { v: 1, text, from: this.me.deviceId }),
        );
        const am = await createApplicationMessage(rec.state, body, this.cs);
        // Keep the advanced ratchet even if the post fails, so a key is never reused.
        rec.state = am.newState;
        await this.persist(gid, rec);
        const wire = encodeMlsMessage({ version: "mls10", wireformat: "mls_private_message", privateMessage: am.privateMessage });
        try {
          const { seq } = await api.send(gid, "application", epoch, toB64(wire), files.map((f) => f.id));
          const shown: ShownMessage = {
            seq,
            groupId: gid,
            senderUser: this.me.userId,
            senderDevice: this.me.deviceId,
            createdAt: Math.floor(Date.now() / 1000),
            state: "ok",
            text,
            ...(files.length ? { files } : {}),
          };
          rec.messages.set(seq, shown);
          await this.persist(gid, rec, shown);
          for (const c of am.consumed) zeroOutUint8Array(c);
          this.emit(gid);
          return;
        } catch (e) {
          if (e instanceof ApiError && e.status === 409) continue; // a commit landed first
          throw e;
        }
      }
      throw new Error("the group kept changing; try again");
    });
  }

  // ---- membership ----

  reconcile(gid: string): Promise<void> {
    return this.lock(gid, async () => {
      for (let attempt = 0; attempt < 3; attempt++) {
        await this.syncLocked(gid);
        const rec = await this.load(gid);
        if (!rec.state) return; // waiting for a Welcome, or nobody has created the group yet
        const r = await this.commitChanges(gid, rec, false);
        if (r !== "conflict") return;
      }
    });
  }

  /**
   * Brings the group's leaves in line with the devices the node says belong
   * in it: adds missing devices from claimed KeyPackages and removes people
   * who left and devices that were revoked. With create, makes the group
   * first. Returns "conflict" when another commit for this epoch won.
   */
  private async commitChanges(gid: string, rec: GroupRec, create: boolean): Promise<"done" | "nothing" | "conflict"> {
    let devices: GroupDevice[];
    try {
      devices = await api.groupDevices(gid);
    } catch (e) {
      if (e instanceof ApiError && (e.status === 403 || e.status === 404)) return "nothing"; // we lost access
      throw e;
    }
    let state = rec.state;
    if (create) {
      const kp = await this.newKeyPackage();
      state = await createGroup(utf8.encode(gid), kp.publicPackage, kp.privatePackage, [], this.cs, this.config);
    }
    if (!state) return "nothing";

    const key = (u: string, d: string) => `${u}:${d}`;
    const meKey = key(this.me.userId, this.me.deviceId);
    const present = leaves(state);
    const presentKeys = new Set(present.map((l) => key(l.userId, l.deviceId)));
    const wantedKeys = new Set(devices.map((d) => key(d.user_id, d.device_id)));

    // People who left and devices that were revoked or lost.
    const removes = present
      .filter((l) => !wantedKeys.has(key(l.userId, l.deviceId)) && key(l.userId, l.deviceId) !== meKey)
      .map((l) => l.leafIndex);

    // Devices that should be here but aren't, grouped by owner.
    const missing = new Map<string, Set<string>>();
    for (const d of devices) {
      const k = key(d.user_id, d.device_id);
      if (presentKeys.has(k) || k === meKey) continue;
      if (!missing.has(d.user_id)) missing.set(d.user_id, new Set());
      missing.get(d.user_id)!.add(d.device_id);
    }
    const adds: { deviceId: string; keyPackage: KeyPackage }[] = [];
    for (const [uid, want] of missing) {
      let claimed;
      try {
        claimed = await api.claimKeyPackages(uid);
      } catch (e) {
        if (e instanceof ApiError && (e.status === 404 || e.status === 403)) continue; // no keys yet; retried on "keys"
        throw e;
      }
      for (const c of claimed) {
        if (!want.has(c.device_id)) continue;
        const kp = this.checkKeyPackage(uid, c.device_id, fromB64(c.key_package));
        if (kp) adds.push({ deviceId: c.device_id, keyPackage: kp });
      }
    }

    if (!create && adds.length === 0 && removes.length === 0) return "nothing";

    const proposals: Proposal[] = [
      ...removes.map((removed): Proposal => ({ proposalType: "remove", remove: { removed } })),
      ...adds.map((a): Proposal => ({ proposalType: "add", add: { keyPackage: a.keyPackage } })),
    ];
    const epoch = Number(state.groupContext.epoch);
    const res = await createCommit(
      { state, cipherSuite: this.cs },
      { extraProposals: proposals, ratchetTreeExtension: true },
    );
    try {
      await api.send(gid, "commit", epoch, toB64(encodeMlsMessage(res.commit)));
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) return "conflict";
      throw e;
    }
    rec.state = res.newState;
    rec.status = "ready";
    if (create) {
      rec.joinedEpoch = epoch;
      rec.rewindTo = undefined;
    }
    await this.persist(gid, rec);
    for (const c of res.consumed) zeroOutUint8Array(c);

    if (res.welcome && adds.length > 0) {
      const welcome = toB64(encodeMlsMessage({ version: "mls10", wireformat: "mls_welcome", welcome: res.welcome }));
      for (const a of adds) {
        try {
          await api.sendWelcome(gid, a.deviceId, welcome);
        } catch (e) {
          console.error("could not deliver Welcome", gid, a.deviceId, e);
        }
      }
    }
    this.emit(gid);
    return "done";
  }

  private checkKeyPackage(userId: string, deviceId: string, bytes: Uint8Array): KeyPackage | undefined {
    try {
      const msg = decodeMlsMessage(bytes, 0)?.[0];
      if (msg?.wireformat !== "mls_key_package") return undefined;
      const kp = msg.keyPackage;
      if (kp.cipherSuite !== SUITE) return undefined;
      const id = parseIdentity(kp.leafNode.credential);
      if (!id || id.userId !== userId || id.deviceId !== deviceId) return undefined;
      return kp;
    } catch {
      return undefined;
    }
  }
}
