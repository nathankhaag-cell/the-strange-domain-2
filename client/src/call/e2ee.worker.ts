// The call's encryption worker: seals every outgoing encoded frame and opens
// every incoming one (see frame.ts). Frames reach it in one of two ways:
//
// - RTCRtpScriptTransform (Firefox, Safari, newer Chromium): the browser
//   fires "rtctransform" here with the frame streams.
// - Insertable streams (Chromium, Electron, Android WebView):
//   createEncodedStreams() in the page, which posts the streams here.
//
// Nothing leaves without encryption: with no key, outgoing frames are
// dropped rather than sent in the clear.

import { clearLength, importKey, openFrame, sealFrame, type MediaKind } from "./frame";

interface EncodedFrame {
  data: ArrayBuffer;
  type?: string; // video: "key" | "delta"; audio frames have none
}

type Op = "encrypt" | "decrypt";

const keys = new Map<number, CryptoKey>();
let sendKeyId = -1;
let sendKey: CryptoKey | undefined;
const counts = { encrypted: 0, decrypted: 0, failed: 0, noKey: 0 };

/** Keys for at most this many recent epochs are kept for receiving. */
const KEEP = 4;

function transform(op: Op, kind: MediaKind): TransformStream<EncodedFrame, EncodedFrame> {
  return new TransformStream<EncodedFrame, EncodedFrame>({
    async transform(frame, ctl) {
      const data = new Uint8Array(frame.data);
      if (op === "encrypt") {
        const key = sendKey;
        if (!key) {
          counts.noKey++;
          return;
        }
        const clear = clearLength(kind, frame.type === "key", data.length);
        const sealed = await sealFrame(key, sendKeyId, data, clear);
        frame.data = sealed.buffer as ArrayBuffer;
        counts.encrypted++;
        ctl.enqueue(frame);
        return;
      }
      if (data.length === 0) return;
      const opened = await openFrame(keys, data);
      if (!opened) {
        counts.failed++;
        return;
      }
      frame.data = opened.buffer as ArrayBuffer;
      counts.decrypted++;
      ctl.enqueue(frame);
    },
  });
}

function pipe(op: Op, kind: MediaKind, readable: ReadableStream<EncodedFrame>, writable: WritableStream<EncodedFrame>) {
  readable
    .pipeThrough(transform(op, kind))
    .pipeTo(writable)
    .catch(() => undefined); // the sender or receiver went away
}

type Msg =
  | { type: "key"; keyId: number; raw: Uint8Array }
  | { type: "send"; keyId: number }
  | { type: "streams"; op: Op; kind: MediaKind; readable: ReadableStream<EncodedFrame>; writable: WritableStream<EncodedFrame> }
  | { type: "stats" }
  | { type: "scramble" };

const scope = self as unknown as {
  onmessage: ((e: MessageEvent<Msg>) => void) | null;
  onrtctransform: ((e: { transformer: { options: { op: Op; kind: MediaKind }; readable: ReadableStream<EncodedFrame>; writable: WritableStream<EncodedFrame> } }) => void) | null;
  postMessage(m: unknown): void;
};

// Messages are handled strictly in order (a key is imported before the
// "send" that switches to it).
let queue: Promise<void> = Promise.resolve();
scope.onmessage = (e) => {
  queue = queue.then(() => handle(e.data)).catch(() => undefined);
};

async function handle(m: Msg) {
  switch (m.type) {
    case "key": {
      const k = await importKey(m.raw);
      keys.delete(m.keyId);
      keys.set(m.keyId, k);
      while (keys.size > KEEP) keys.delete(keys.keys().next().value!);
      break;
    }
    case "send":
      sendKeyId = m.keyId;
      sendKey = keys.get(m.keyId);
      break;
    case "streams":
      pipe(m.op, m.kind, m.readable, m.writable);
      break;
    case "stats":
      scope.postMessage({ type: "stats", ...counts, keyIds: Array.from(keys.keys()), sendKeyId });
      break;
    case "scramble": {
      // Test hook (only reachable when call debugging is on, see session.ts):
      // replace every receive key with a random one, as a receiver without
      // the call key would have. Frames then fail to open.
      for (const id of Array.from(keys.keys())) {
        keys.set(id, await importKey(crypto.getRandomValues(new Uint8Array(32))));
      }
      break;
    }
  }
}

scope.onrtctransform = (e) => {
  const t = e.transformer;
  pipe(t.options.op, t.options.kind, t.readable, t.writable);
};
