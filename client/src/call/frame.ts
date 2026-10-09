// End-to-end encryption of call media, one encoded frame at a time (in the
// spirit of SFrame, RFC 9605, simplified).
//
// Every audio and video frame is sealed with AES-256-GCM under the call key,
// which each device derives from the call's MLS group (MlsCrypto.mediaKey).
// The node relays the sealed frames and never has the key.
//
// Frame layout:
//
//   [clear header: n bytes] [ciphertext + 16-byte tag] [IV: 12 bytes] [n: 1 byte] [key id: 1 byte]
//
// - The first bytes of each frame stay readable because the browser's RTP
//   packetiser and depacketiser parse them: 1 byte for Opus (the TOC byte),
//   10 bytes of a VP8 key frame and 3 of a VP8 delta frame (the VP8 payload
//   header: frame type and size). They are authenticated, not hidden. Audio
//   samples and picture data are encrypted.
// - The key id is the MLS epoch modulo 256, so a receiver picks the key for
//   the epoch the sender used while the group changes.
// - The IV is random for every frame.
// - The clear header and the two trailer bytes are additional authenticated
//   data: a frame that was changed in transit fails to decrypt.

export const IV_LEN = 12;
export const TAG_LEN = 16;
export const TRAILER_LEN = 2;
export const OVERHEAD = TAG_LEN + IV_LEN + TRAILER_LEN;

export type MediaKind = "audio" | "video";

/** How many leading bytes of a frame stay readable (see above). */
export function clearLength(kind: MediaKind, keyFrame: boolean, size: number): number {
  const n = kind === "audio" ? 1 : keyFrame ? 10 : 3;
  return Math.min(n, size);
}

export function importKey(raw: Uint8Array): Promise<CryptoKey> {
  return crypto.subtle.importKey("raw", raw as Uint8Array<ArrayBuffer>, { name: "AES-GCM" }, false, ["encrypt", "decrypt"]);
}

export async function sealFrame(key: CryptoKey, keyId: number, frame: Uint8Array, clear: number): Promise<Uint8Array> {
  const iv = crypto.getRandomValues(new Uint8Array(IV_LEN));
  const trailer = new Uint8Array([clear, keyId & 0xff]);
  const aad = new Uint8Array(clear + TRAILER_LEN);
  aad.set(frame.subarray(0, clear));
  aad.set(trailer, clear);
  const ct = new Uint8Array(
    await crypto.subtle.encrypt(
      { name: "AES-GCM", iv, additionalData: aad, tagLength: TAG_LEN * 8 },
      key,
      frame.subarray(clear) as Uint8Array<ArrayBuffer>,
    ),
  );
  const out = new Uint8Array(clear + ct.length + IV_LEN + TRAILER_LEN);
  out.set(frame.subarray(0, clear));
  out.set(ct, clear);
  out.set(iv, clear + ct.length);
  out.set(trailer, clear + ct.length + IV_LEN);
  return out;
}

/** Opens a sealed frame, or returns null (unknown key id, damaged or not sealed). */
export async function openFrame(keys: ReadonlyMap<number, CryptoKey>, sealed: Uint8Array): Promise<Uint8Array | null> {
  const len = sealed.length;
  if (len < OVERHEAD) return null;
  const keyId = sealed[len - 1];
  const clear = sealed[len - 2];
  if (clear > 10 || len < clear + OVERHEAD) return null;
  const key = keys.get(keyId);
  if (!key) return null;
  const iv = sealed.subarray(len - TRAILER_LEN - IV_LEN, len - TRAILER_LEN);
  const ct = sealed.subarray(clear, len - TRAILER_LEN - IV_LEN);
  const aad = new Uint8Array(clear + TRAILER_LEN);
  aad.set(sealed.subarray(0, clear));
  aad.set(sealed.subarray(len - TRAILER_LEN), clear);
  try {
    const pt = new Uint8Array(
      await crypto.subtle.decrypt(
        { name: "AES-GCM", iv: iv as Uint8Array<ArrayBuffer>, additionalData: aad, tagLength: TAG_LEN * 8 },
        key,
        ct as Uint8Array<ArrayBuffer>,
      ),
    );
    const out = new Uint8Array(clear + pt.length);
    out.set(sealed.subarray(0, clear));
    out.set(pt, clear);
    return out;
  } catch {
    return null;
  }
}
