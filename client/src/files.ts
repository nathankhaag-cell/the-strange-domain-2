// Attachments, encrypted end to end.
//
// Each file is encrypted on this device with a fresh random AES-256-GCM key
// and IV. Only the ciphertext goes to the node (POST /groups/{gid}/blobs).
// The key, IV, a SHA-256 of the ciphertext and the file's name, type, size
// and dimensions travel inside the MLS message, so only group members can
// read them. Receivers fetch the ciphertext, check its hash, and decrypt it
// into an object URL in memory.
//
// Only common raster images (PNG, JPEG, GIF, WebP), checked by their first
// bytes, are ever shown inline. Everything else, SVG and HTML included, is
// offered as a download only.

import { api } from "./api";
import { fromB64, toB64 } from "./b64";

export interface FileRef {
  /** Blob id on the node. */
  id: string;
  /** AES-256-GCM key, base64. */
  key: string;
  /** 12-byte GCM IV, base64. */
  iv: string;
  /** SHA-256 of the ciphertext, base64. */
  sha256: string;
  name: string;
  mime: string;
  /** Plaintext size in bytes. */
  size: number;
  w?: number;
  h?: number;
}

const INLINE_TYPES = ["image/png", "image/jpeg", "image/gif", "image/webp"];

/** The sender's claimed type says this may be an inline image (checked again after decrypting). */
export function isInlineImage(f: FileRef): boolean {
  return INLINE_TYPES.includes(f.mime);
}

/** The raster image type the bytes really are, or undefined. */
export function sniffImage(b: Uint8Array): string | undefined {
  const at = (i: number, ...v: number[]) => v.every((x, j) => b[i + j] === x);
  if (at(0, 0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a)) return "image/png";
  if (at(0, 0xff, 0xd8, 0xff)) return "image/jpeg";
  if (at(0, 0x47, 0x49, 0x46, 0x38)) return "image/gif";
  if (at(0, 0x52, 0x49, 0x46, 0x46) && at(8, 0x57, 0x45, 0x42, 0x50)) return "image/webp";
  return undefined;
}

/** Validates a FileRef that arrived in a message; returns undefined if malformed. */
export function parseFileRef(x: unknown): FileRef | undefined {
  if (!x || typeof x !== "object") return undefined;
  const o = x as Record<string, unknown>;
  const str = (v: unknown, max: number) => typeof v === "string" && v.length > 0 && v.length <= max;
  if (!str(o.id, 32) || !/^[0-9a-f]{32}$/.test(o.id as string)) return undefined;
  if (!str(o.key, 64) || !str(o.iv, 32) || !str(o.sha256, 64)) return undefined;
  try {
    if (fromB64(o.key as string).length !== 32 || fromB64(o.iv as string).length !== 12) return undefined;
    if (fromB64(o.sha256 as string).length !== 32) return undefined;
  } catch {
    return undefined;
  }
  if (typeof o.size !== "number" || !Number.isFinite(o.size) || o.size < 0) return undefined;
  const dim = (v: unknown) => (typeof v === "number" && Number.isFinite(v) && v > 0 && v < 100000 ? Math.round(v) : undefined);
  return {
    id: o.id as string,
    key: o.key as string,
    iv: o.iv as string,
    sha256: o.sha256 as string,
    name: cleanName(typeof o.name === "string" ? o.name : ""),
    mime: typeof o.mime === "string" ? o.mime.slice(0, 100).toLowerCase() : "application/octet-stream",
    size: o.size,
    w: dim(o.w),
    h: dim(o.h),
  };
}

/** A file name safe to show and to save as: no paths, no control characters. */
export function cleanName(name: string): string {
  const base = name.split(/[\\/]/).pop() ?? "";
  // eslint-disable-next-line no-control-regex
  const clean = base.replace(/[\u0000-\u001f\u007f-\u009f‪-‮⁦-⁩]/g, "").trim().slice(0, 200);
  return clean || "file";
}

export function fmtSize(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(n < 10 * 1024 ? 1 : 0)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

const buf = (u: Uint8Array) => u as Uint8Array<ArrayBuffer>;

async function sha256(data: Uint8Array): Promise<Uint8Array> {
  return new Uint8Array(await crypto.subtle.digest("SHA-256", buf(data)));
}

async function imageSize(file: Blob): Promise<{ w: number; h: number } | undefined> {
  try {
    const bmp = await createImageBitmap(file);
    const out = { w: bmp.width, h: bmp.height };
    bmp.close();
    return out;
  } catch {
    return undefined;
  }
}

/** Encrypts a file and uploads the ciphertext for a group. Returns what goes in the message. */
export async function encryptAndUpload(gid: string, file: File): Promise<FileRef> {
  const plain = new Uint8Array(await file.arrayBuffer());
  const rawKey = crypto.getRandomValues(new Uint8Array(32));
  const iv = crypto.getRandomValues(new Uint8Array(12));
  const key = await crypto.subtle.importKey("raw", rawKey, "AES-GCM", false, ["encrypt"]);
  const ct = new Uint8Array(await crypto.subtle.encrypt({ name: "AES-GCM", iv }, key, plain));
  const hash = await sha256(ct);
  const { id } = await api.uploadBlob(gid, ct);
  const mime = (file.type || "application/octet-stream").toLowerCase();
  const ref: FileRef = {
    id,
    key: toB64(rawKey),
    iv: toB64(iv),
    sha256: toB64(hash),
    name: cleanName(file.name),
    mime,
    size: file.size,
  };
  const inlineType = sniffImage(plain.subarray(0, 16));
  if (inlineType) {
    const dims = await imageSize(file);
    if (dims) Object.assign(ref, dims);
    // The sender sees its own image without downloading it again.
    cache.set(id, Promise.resolve({ url: URL.createObjectURL(new Blob([plain], { type: inlineType })), inline: true }));
  }
  rawKey.fill(0);
  return ref;
}

interface Decrypted {
  url: string;
  /** True when the bytes are a raster image that may be shown inline. */
  inline: boolean;
}

const cache = new Map<string, Promise<Decrypted>>();

async function decrypt(f: FileRef): Promise<Uint8Array> {
  const ct = await api.fetchBlob(f.id);
  const want = fromB64(f.sha256);
  const got = await sha256(ct);
  if (got.length !== want.length || got.some((b, i) => b !== want[i])) throw new Error("The file does not match its message.");
  const key = await crypto.subtle.importKey("raw", buf(fromB64(f.key)), "AES-GCM", false, ["decrypt"]);
  try {
    return new Uint8Array(await crypto.subtle.decrypt({ name: "AES-GCM", iv: buf(fromB64(f.iv)) }, key, buf(ct)));
  } catch {
    throw new Error("The file could not be decrypted.");
  }
}

/**
 * Decrypts an attachment into an object URL. Images that really are PNG,
 * JPEG, GIF or WebP get their image type; everything else is
 * application/octet-stream so the browser never renders it.
 */
export function openFile(f: FileRef): Promise<Decrypted> {
  let p = cache.get(f.id);
  if (!p) {
    p = decrypt(f).then((plain) => {
      const type = isInlineImage(f) ? sniffImage(plain.subarray(0, 16)) : undefined;
      return { url: URL.createObjectURL(new Blob([buf(plain)], { type: type ?? "application/octet-stream" })), inline: !!type };
    });
    p.catch(() => cache.delete(f.id));
    cache.set(f.id, p);
  }
  return p;
}

/** Decrypts an attachment and saves it with its name. */
export async function saveFile(f: FileRef): Promise<void> {
  const { url } = await openFile(f);
  const a = document.createElement("a");
  a.href = url;
  a.download = f.name;
  a.rel = "noopener";
  document.body.appendChild(a);
  a.click();
  a.remove();
}

/** Forgets decrypted copies of a deleted message's files. */
export function forgetFiles(files: FileRef[] | undefined) {
  for (const f of files ?? []) {
    const p = cache.get(f.id);
    cache.delete(f.id);
    void p?.then((d) => URL.revokeObjectURL(d.url)).catch(() => undefined);
  }
}

// ---- profile pictures ----

const avatars = new Map<string, Promise<string | null>>();
/** Profile pictures already loaded, so they render without a flash. Key: "uid:version". */
export const avatarLoaded = new Map<string, string>();

/** Object URL for a user's profile picture at a version, or null if it can't be loaded. */
export function avatarUrl(uid: string, version: number): Promise<string | null> {
  const k = `${uid}:${version}`;
  let p = avatars.get(k);
  if (!p) {
    p = api
      .fetchAvatar(uid, version)
      .then((b) => {
        if (b.type !== "image/png" && b.type !== "image/webp") return null;
        const url = URL.createObjectURL(b);
        avatarLoaded.set(k, url);
        return url;
      })
      .catch(() => {
        avatars.delete(k);
        return null;
      });
    avatars.set(k, p);
  }
  return p;
}


/**
 * Crops an image to a centered square (with zoom and offset from the
 * picker) and re-encodes it at size x size as WebP, or PNG where the browser
 * can't make WebP. Re-encoding drops all metadata in the original file.
 */
export async function renderAvatar(
  source: ImageBitmap,
  view: { zoom: number; x: number; y: number },
  size = 256,
): Promise<Blob> {
  const canvas = document.createElement("canvas");
  canvas.width = size;
  canvas.height = size;
  const ctx = canvas.getContext("2d");
  if (!ctx) throw new Error("This browser cannot edit pictures.");
  const side = Math.min(source.width, source.height) / view.zoom;
  const sx = (source.width - side) / 2 - view.x * side;
  const sy = (source.height - side) / 2 - view.y * side;
  ctx.imageSmoothingQuality = "high";
  ctx.drawImage(source, sx, sy, side, side, 0, 0, size, size);
  const toBlob = (type: string, q?: number) => new Promise<Blob | null>((r) => canvas.toBlob(r, type, q));
  let out = await toBlob("image/webp", 0.9);
  if (!out || out.type !== "image/webp") out = await toBlob("image/png");
  if (!out) throw new Error("This browser cannot save pictures.");
  return out;
}
