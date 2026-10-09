// The device identity key (Ed25519) that signs sign-in challenges.
//
// Where the browser supports Ed25519 in WebCrypto, the private key is created
// non-extractable and stored in IndexedDB as a CryptoKey: page scripts can use
// it to sign but nobody can read its bytes. Older browsers fall back to a raw
// key from @noble/curves, also kept in IndexedDB.

import { ed25519 } from "@noble/curves/ed25519.js";
import * as idb from "./idb";
import { utf8 } from "./b64";

export const AUTH_CONTEXT = "strange-domain-auth-v1:";
export const RECOVERY_CONTEXT = "strange-domain-recovery-v1:";

export interface DeviceIdentity {
  userId: string;
  deviceId: string;
  callsign: string;
  deviceName: string;
  publicKey: Uint8Array;
  // Exactly one of these is set.
  privateKey?: CryptoKey;
  rawPrivateKey?: Uint8Array;
}

/** A key pair that has not been registered with the node yet. */
export interface NewKey {
  publicKey: Uint8Array;
  privateKey?: CryptoKey;
  rawPrivateKey?: Uint8Array;
}

export async function generateDeviceKey(): Promise<NewKey> {
  try {
    const kp = (await crypto.subtle.generateKey({ name: "Ed25519" }, false, ["sign", "verify"])) as CryptoKeyPair;
    const pub = new Uint8Array(await crypto.subtle.exportKey("raw", kp.publicKey));
    return { publicKey: pub, privateKey: kp.privateKey };
  } catch {
    const raw = ed25519.utils.randomSecretKey();
    return { publicKey: ed25519.getPublicKey(raw), rawPrivateKey: raw };
  }
}

export async function sign(key: { privateKey?: CryptoKey; rawPrivateKey?: Uint8Array }, msg: Uint8Array): Promise<Uint8Array> {
  if (key.privateKey) {
    return new Uint8Array(await crypto.subtle.sign({ name: "Ed25519" }, key.privateKey, msg as BufferSource));
  }
  if (key.rawPrivateKey) return ed25519.sign(msg, key.rawPrivateKey);
  throw new Error("device key missing");
}

export function withContext(context: string, nonce: Uint8Array): Uint8Array {
  const prefix = utf8.encode(context);
  const out = new Uint8Array(prefix.length + nonce.length);
  out.set(prefix);
  out.set(nonce, prefix.length);
  return out;
}

export async function loadIdentity(): Promise<DeviceIdentity | undefined> {
  return idb.get<DeviceIdentity>("device", "identity");
}

export async function saveIdentity(id: DeviceIdentity): Promise<void> {
  await idb.put("device", "identity", id);
}

// ---- recovery codes ----

const CROCKFORD = "0123456789ABCDEFGHJKMNPQRSTVWXYZ";

/** A new recovery code: 20 Crockford base32 characters (100 bits) in groups of four. */
export function newRecoveryCode(): string {
  const b = crypto.getRandomValues(new Uint8Array(20));
  const chars = Array.from(b, (x) => CROCKFORD[x % 32]).join("");
  return chars.match(/.{4}/g)!.join("-");
}

export function normalizeCode(code: string): string {
  return code.toUpperCase().replace(/[\s-]/g, "").replace(/O/g, "0").replace(/[IL]/g, "1");
}

/**
 * Derives the recovery key pair from a recovery code. The node stores only the
 * public half. The callsign salts the derivation so equal codes on different
 * accounts give different keys.
 */
export async function recoveryKeyFromCode(code: string, callsign: string): Promise<{ publicKey: Uint8Array; rawPrivateKey: Uint8Array }> {
  const material = await crypto.subtle.importKey("raw", utf8.encode(normalizeCode(code)), "PBKDF2", false, ["deriveBits"]);
  const salt = utf8.encode(RECOVERY_CONTEXT + callsign.trim().toLowerCase());
  const bits = await crypto.subtle.deriveBits({ name: "PBKDF2", hash: "SHA-256", salt, iterations: 310_000 }, material, 256);
  const seed = new Uint8Array(bits);
  return { publicKey: ed25519.getPublicKey(seed), rawPrivateKey: seed };
}
