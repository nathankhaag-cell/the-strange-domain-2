// One call, from this device's side: a Voice Relay or a call in a
// Confession or Conclave.
//
// Media goes to the node's relay (internal/rtc) over WebRTC; signalling goes
// over the WebSocket /api/v1/rtc. The node makes every offer and this side
// answers. The first m-lines of the node's first offer are this device's own
// slots (microphone, camera, screen); the node says which, and which other
// participant each later m-line carries.
//
// Every frame is end-to-end encrypted in e2ee.worker.ts with the call key
// from the MLS group (MlsCrypto.mediaKey). When the group's epoch changes
// (someone joins or leaves the group, a device is added or revoked) the key
// changes: receivers accept the new key at once and this device starts
// sending with it a second later, so others have time to process the commit.
// A browser that cannot transform encoded frames cannot join: calls are never
// sent unencrypted.

import { getToken, type CallPart } from "../api";
import { streamUrl } from "../node";
import type { MessageCrypto } from "../crypto/types";

export type Slot = "audio" | "camera" | "screen";

export interface RemoteMedia {
  audio?: MediaStreamTrack;
  camera?: MediaStreamTrack;
  screen?: MediaStreamTrack;
}

export interface CallView {
  gid: string;
  relay: boolean;
  phase: "connecting" | "live";
  selfId?: string;
  /** False when muted in the domain: this device listens only. */
  canSpeak: boolean;
  /** Whether a microphone is available (access granted). */
  micOk: boolean;
  muted: boolean;
  ptt: boolean;
  /** Push to talk is held down. */
  talking: boolean;
  camera: boolean;
  screen: boolean;
  videoAllowed: boolean;
  participants: CallPart[];
  declined: string[];
  /** Who is talking now, by participant id (this device under selfId). */
  speaking: Record<string, boolean>;
  remote: Record<string, RemoteMedia>;
  localCamera?: MediaStream;
  localScreen?: MediaStream;
  /** True once someone else has been in the call. */
  hadPeers: boolean;
  /** Ringing the other members (a call this device started). */
  calling: boolean;
}

/** How this browser can encrypt call media, or null if it cannot. */
export function e2eeSupport(): "insertable" | "script" | null {
  if (typeof RTCRtpSender === "undefined" || typeof Worker === "undefined") return null;
  if ("createEncodedStreams" in RTCRtpSender.prototype) return "insertable";
  if (typeof (globalThis as { RTCRtpScriptTransform?: unknown }).RTCRtpScriptTransform === "function") return "script";
  return null;
}

export const NO_E2EE =
  "This browser cannot encrypt calls end to end, so it cannot join them. Use a current version of Chrome, Edge, Firefox or Safari, or the desktop or Android app.";
export const NOT_IN_GROUP =
  "This device has not been added to this call's encryption yet. Another member's device adds it automatically when it is online. Try again in a moment.";

// Bandwidth-conscious defaults for a Raspberry Pi node and mesh links.
const AUDIO_BPS = 32_000;
const CAMERA: MediaTrackConstraints = { width: { ideal: 640, max: 640 }, height: { ideal: 360, max: 360 }, frameRate: { ideal: 15, max: 15 } };
const CAMERA_BPS = 500_000;
const SCREEN: MediaTrackConstraints = { width: { max: 1280 }, height: { max: 720 }, frameRate: { max: 5 } };
const SCREEN_BPS = 800_000;
const MIC: MediaTrackConstraints = { echoCancellation: true, noiseSuppression: true, autoGainControl: true, channelCount: 1 };
/** How long a call this device started rings before it gives up. */
const RING_MS = 60_000;
/** How long after a key change this device starts sending with the new key. */
const KEY_SWITCH_MS = 1000;
const SPEAKING_RMS = 0.02;

interface Options {
  gid: string;
  relay: boolean;
  ring: boolean;
  video: boolean;
  /** Ask for the microphone (false when known to be muted in the domain). */
  wantMic: boolean;
  engine: MessageCrypto;
  nameOf: (uid: string) => string;
  onUpdate: (v: CallView) => void;
  onEnd: (reason: string) => void;
}

interface OfferMsg {
  type: "offer";
  sdp: string;
  tracks: Record<string, { participant: string; slot: Slot }>;
  publish: Record<string, string>;
}

type ServerMsg =
  | { type: "welcome"; id: string; can_speak: boolean; video: boolean }
  | OfferMsg
  | { type: "room"; room: { participants: CallPart[]; declined: string[] } }
  | { type: "speak"; can_speak: boolean }
  | { type: "declined"; user_id: string }
  | { type: "bye"; reason: string }
  | { type: "error"; error: string };

export class CallSession {
  readonly gid: string;
  view: CallView;
  private mode: "insertable" | "script";
  private ws?: WebSocket;
  private pc?: RTCPeerConnection;
  private worker?: Worker;
  private queue: Promise<void> = Promise.resolve();
  private ended = false;
  private publishMids: Record<string, Slot> = {};
  private tracksByMid: Record<string, { participant: string; slot: Slot }> = {};
  private transformed = new WeakSet<object>();
  private senders: Partial<Record<Slot, RTCRtpSender>> = {};
  private mic?: MediaStreamTrack;
  private cam?: MediaStreamTrack;
  private scr?: MediaStreamTrack;
  private players = new Map<string, { el: HTMLAudioElement; track: MediaStreamTrack }>();
  private meters = new Map<string, { track: MediaStreamTrack; analyser: AnalyserNode; buf: Float32Array<ArrayBuffer>; last: number }>();
  private ac?: AudioContext;
  private timers: number[] = [];
  private keyEpoch = -1;

  constructor(private o: Options) {
    this.gid = o.gid;
    this.mode = e2eeSupport() ?? "insertable";
    this.view = {
      gid: o.gid,
      relay: o.relay,
      phase: "connecting",
      canSpeak: true,
      micOk: false,
      muted: false,
      ptt: loadPtt(),
      talking: false,
      camera: false,
      screen: false,
      videoAllowed: false,
      participants: [],
      declined: [],
      speaking: {},
      remote: {},
      hadPeers: false,
      calling: o.ring && !o.relay,
    };
  }

  private update() {
    if (!this.ended) this.o.onUpdate({ ...this.view });
  }

  // ---- start and end ----

  async start() {
    if (!e2eeSupport()) throw new Error(NO_E2EE);
    const status = await this.o.engine.prepare(this.gid);
    if (status !== "ready") throw new Error(NOT_IN_GROUP);
    this.worker = new Worker(new URL("./e2ee.worker.ts", import.meta.url), { type: "module" });
    if (!(await this.refreshKey(true))) throw new Error(NOT_IN_GROUP);
    if (this.ended) return;

    // Audio for speaking indicators; created on the Join or Call press.
    try {
      this.ac = new AudioContext();
      void this.ac.resume().catch(() => undefined);
    } catch {
      /* no WebAudio: no speaking indicators */
    }
    if (this.o.wantMic) {
      try {
        const s = await navigator.mediaDevices.getUserMedia({ audio: MIC });
        this.mic = s.getAudioTracks()[0];
        this.view.micOk = !!this.mic;
        this.applyMic();
      } catch {
        this.view.micOk = false; // listen only
      }
    }
    if (this.ended) {
      this.mic?.stop();
      return;
    }
    if (this.o.video) {
      try {
        const s = await navigator.mediaDevices.getUserMedia({ video: CAMERA });
        this.cam = s.getVideoTracks()[0];
      } catch {
        /* no camera: voice only */
      }
    }
    this.connect();
    this.timers.push(window.setInterval(() => this.measure(), 120));
    if (this.view.calling) {
      this.timers.push(
        window.setTimeout(() => {
          if (!this.view.hadPeers) this.end("No answer.");
        }, RING_MS),
      );
    }
    this.debugHook();
  }

  /** Leaves the call. reason, if given, is shown to the person. */
  end(reason = "") {
    if (this.ended) return;
    this.ended = true;
    try {
      if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify({ type: "leave" }));
    } catch {
      /* closing anyway */
    }
    this.ws?.close();
    this.pc?.close();
    for (const t of [this.mic, this.cam, this.scr]) t?.stop();
    for (const p of this.players.values()) {
      p.el.pause();
      p.el.srcObject = null;
    }
    this.players.clear();
    this.meters.clear();
    for (const t of this.timers) {
      window.clearInterval(t);
      window.clearTimeout(t);
    }
    void this.ac?.close().catch(() => undefined);
    this.worker?.terminate();
    if ((window as { __sdCall?: unknown }).__sdCall) delete (window as { __sdCall?: unknown }).__sdCall;
    this.o.onEnd(reason);
  }

  // ---- keys ----

  /** Called when the group's MLS state changed: picks up a new epoch's key. */
  groupChanged() {
    void this.refreshKey(false);
  }

  private async refreshKey(first: boolean): Promise<boolean> {
    const k = await this.o.engine.mediaKey(this.gid);
    if (!k || !this.worker) return false;
    if (k.epoch === this.keyEpoch) return true;
    this.keyEpoch = k.epoch;
    const keyId = k.epoch & 0xff;
    this.worker.postMessage({ type: "key", keyId, raw: k.key });
    if (first) {
      this.worker.postMessage({ type: "send", keyId });
    } else {
      const epoch = k.epoch;
      this.timers.push(
        window.setTimeout(() => {
          if (this.keyEpoch === epoch) this.worker?.postMessage({ type: "send", keyId });
        }, KEY_SWITCH_MS),
      );
    }
    return true;
  }

  // ---- signalling ----

  private send(m: unknown) {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(m));
  }

  private connect() {
    const ws = new WebSocket(streamUrl("/rtc"));
    this.ws = ws;
    ws.onopen = () =>
      ws.send(JSON.stringify({ token: getToken(), room: this.gid, ring: this.o.ring, video: this.o.video }));
    ws.onmessage = (e) => {
      let m: ServerMsg;
      try {
        m = JSON.parse(String(e.data));
      } catch {
        return;
      }
      this.queue = this.queue
        .then(() => this.onMessage(m))
        .catch((err) => {
          console.warn("call", err);
          this.end("The call could not be set up on this device.");
        });
    };
    ws.onclose = () => {
      if (!this.ended) this.end("The connection to the node was lost.");
    };
  }

  private async onMessage(m: ServerMsg) {
    if (this.ended) return;
    switch (m.type) {
      case "welcome":
        this.view.selfId = m.id;
        this.view.canSpeak = m.can_speak;
        this.view.videoAllowed = m.video;
        if (!m.video) {
          this.cam?.stop();
          this.cam = undefined;
        }
        this.view.camera = !!this.cam;
        this.view.localCamera = this.cam ? new MediaStream([this.cam]) : undefined;
        this.applyMic();
        this.sendState();
        this.update();
        break;
      case "offer":
        await this.onOffer(m);
        break;
      case "room": {
        this.view.participants = m.room.participants;
        this.view.declined = m.room.declined;
        const others = m.room.participants.filter((p) => p.id !== this.view.selfId).length;
        if (others > 0) {
          this.view.hadPeers = true;
          this.view.calling = false;
        } else if (!this.view.relay && this.view.hadPeers) {
          this.end("The call ended.");
          return;
        }
        this.collectRemote();
        break;
      }
      case "speak":
        this.view.canSpeak = m.can_speak;
        this.applyMic();
        void this.attachSlot("audio");
        this.sendState();
        this.update();
        break;
      case "declined": {
        if (!this.view.declined.includes(m.user_id)) this.view.declined = [...this.view.declined, m.user_id];
        this.update();
        break;
      }
      case "bye":
        this.end(m.reason);
        break;
      case "error":
        this.end(sentence(m.error));
        break;
    }
  }

  /** Called by the app with the Conclave's member ids: hangs up when all others declined. */
  checkDeclined(memberIds: string[], me: string) {
    if (this.view.relay || this.view.hadPeers || !this.view.calling) return;
    const others = memberIds.filter((u) => u !== me);
    if (others.length > 0 && others.every((u) => this.view.declined.includes(u))) {
      const names = others.map(this.o.nameOf).join(", ");
      this.end(`${names} declined.`);
    }
  }

  private ensurePC(): RTCPeerConnection {
    if (this.pc) return this.pc;
    const cfg: RTCConfiguration & { encodedInsertableStreams?: boolean } = {
      // No STUN or TURN: the node is the only peer, reachable on the LAN or
      // at the public address it announces. Works with no internet.
      iceServers: [],
      bundlePolicy: "max-bundle",
      rtcpMuxPolicy: "require",
    };
    if (this.mode === "insertable") cfg.encodedInsertableStreams = true;
    const pc = new RTCPeerConnection(cfg);
    pc.onicecandidate = (e) => {
      if (e.candidate) this.send({ type: "candidate", candidate: e.candidate.toJSON() });
    };
    pc.onconnectionstatechange = () => {
      if (pc.connectionState === "connected" && this.view.phase !== "live") {
        this.view.phase = "live";
        this.update();
      } else if (pc.connectionState === "failed") {
        this.end("The call's media connection failed. The node's call port may be blocked by a firewall.");
      }
    };
    pc.ontrack = () => this.collectRemote();
    this.pc = pc;
    return pc;
  }

  private async onOffer(m: OfferMsg) {
    const pc = this.ensurePC();
    for (const [slot, mid] of Object.entries(m.publish)) this.publishMids[mid] = slot as Slot;
    this.tracksByMid = m.tracks ?? {};
    await pc.setRemoteDescription({ type: "offer", sdp: m.sdp });
    const fresh: Slot[] = [];
    for (const t of pc.getTransceivers()) {
      if (!t.mid) continue;
      const slot = this.publishMids[t.mid];
      if (slot) {
        if (!this.senders[slot]) {
          t.direction = "sendonly";
          this.attach(t.sender, "encrypt", slot === "audio" ? "audio" : "video");
          this.senders[slot] = t.sender;
          fresh.push(slot);
        }
      } else if (t.receiver.track) {
        this.attach(t.receiver, "decrypt", t.receiver.track.kind === "audio" ? "audio" : "video");
      }
    }
    const answer = await pc.createAnswer();
    await pc.setLocalDescription(answer);
    this.send({ type: "answer", sdp: pc.localDescription?.sdp ?? answer.sdp });
    for (const slot of fresh) await this.attachSlot(slot);
    this.collectRemote();
  }

  /** Sets up end-to-end encryption on a sender or receiver (once each). */
  private attach(x: RTCRtpSender | RTCRtpReceiver, op: "encrypt" | "decrypt", kind: "audio" | "video") {
    if (this.transformed.has(x) || !this.worker) return;
    this.transformed.add(x);
    if (this.mode === "script") {
      const ST = (globalThis as unknown as { RTCRtpScriptTransform: new (w: Worker, o: unknown) => unknown }).RTCRtpScriptTransform;
      (x as unknown as { transform: unknown }).transform = new ST(this.worker, { op, kind });
    } else {
      const s = (x as unknown as { createEncodedStreams(): { readable: ReadableStream; writable: WritableStream } }).createEncodedStreams();
      this.worker.postMessage({ type: "streams", op, kind, readable: s.readable, writable: s.writable }, [
        s.readable as unknown as Transferable,
        s.writable as unknown as Transferable,
      ]);
    }
  }

  /** Puts this device's current track (or nothing) on one of its slots. */
  private async attachSlot(slot: Slot) {
    const sender = this.senders[slot];
    if (!sender) return;
    const track = slot === "audio" ? (this.view.canSpeak ? this.mic : undefined) : slot === "camera" ? this.cam : this.scr;
    try {
      await sender.replaceTrack(track ?? null);
    } catch (e) {
      console.warn("replaceTrack", slot, e);
    }
    if (!track) return;
    const p = sender.getParameters();
    if (!p.encodings || p.encodings.length === 0) p.encodings = [{}];
    const enc = p.encodings[0];
    if (slot === "audio") enc.maxBitrate = AUDIO_BPS;
    else if (slot === "camera") Object.assign(enc, { maxBitrate: CAMERA_BPS, maxFramerate: 15 });
    else Object.assign(enc, { maxBitrate: SCREEN_BPS, maxFramerate: 5 });
    await sender.setParameters(p).catch(() => undefined);
  }

  // ---- remote media ----

  private collectRemote() {
    if (!this.pc || this.ended) return;
    const remote: Record<string, RemoteMedia> = {};
    for (const t of this.pc.getTransceivers()) {
      const info = t.mid ? this.tracksByMid[t.mid] : undefined;
      if (!info) continue;
      if (t.currentDirection !== "recvonly" && t.currentDirection !== "sendrecv") continue;
      (remote[info.participant] ??= {})[info.slot] = t.receiver.track;
    }
    // Remove people who left.
    const present = new Set(this.view.participants.map((p) => p.id));
    for (const pid of Object.keys(remote)) if (!present.has(pid)) delete remote[pid];
    this.view.remote = remote;

    // Play everyone's audio; measure it for the speaking indicator.
    for (const [pid, media] of Object.entries(remote)) {
      const track = media.audio;
      const cur = this.players.get(pid);
      if (!track) continue;
      if (cur?.track === track) continue;
      const el = cur?.el ?? new Audio();
      el.autoplay = true;
      el.srcObject = new MediaStream([track]);
      void el.play().catch(() => undefined);
      this.players.set(pid, { el, track });
      this.meter(pid, track);
    }
    for (const [pid, p] of this.players) {
      if (!remote[pid]?.audio) {
        p.el.pause();
        p.el.srcObject = null;
        this.players.delete(pid);
        this.meters.delete(pid);
      }
    }
    this.update();
  }

  private meter(key: string, track: MediaStreamTrack) {
    if (!this.ac) return;
    try {
      const src = this.ac.createMediaStreamSource(new MediaStream([track]));
      const analyser = this.ac.createAnalyser();
      analyser.fftSize = 512;
      src.connect(analyser);
      this.meters.set(key, { track, analyser, buf: new Float32Array(analyser.fftSize), last: 0 });
    } catch {
      /* not measurable */
    }
  }

  private measure() {
    const now = Date.now();
    const speaking: Record<string, boolean> = {};
    for (const [key, m] of this.meters) {
      m.analyser.getFloatTimeDomainData(m.buf);
      let sum = 0;
      for (const v of m.buf) sum += v * v;
      const rms = Math.sqrt(sum / m.buf.length);
      const live = key === this.view.selfId ? !!this.mic?.enabled : true;
      if (live && rms > SPEAKING_RMS) m.last = now;
      speaking[key] = now - m.last < 400;
    }
    const changed = Object.keys(speaking).some((k) => speaking[k] !== !!this.view.speaking[k]) ||
      Object.keys(this.view.speaking).some((k) => !(k in speaking));
    if (changed) {
      this.view.speaking = speaking;
      this.update();
    }
  }

  // ---- controls ----

  private applyMic() {
    if (!this.mic) return;
    const on = this.view.canSpeak && (this.view.ptt ? this.view.talking : !this.view.muted);
    this.mic.enabled = on;
    if (this.view.selfId && !this.meters.has(this.view.selfId)) this.meter(this.view.selfId, this.mic);
  }

  private sendState() {
    const silent = !this.mic || !this.view.canSpeak || (this.view.ptt ? !this.view.talking : this.view.muted);
    this.send({ type: "state", muted: silent, camera: this.view.camera, screen: this.view.screen });
  }

  setMuted(muted: boolean) {
    this.view.muted = muted;
    this.applyMic();
    this.sendState();
    this.update();
  }

  setPtt(on: boolean) {
    this.view.ptt = on;
    this.view.talking = false;
    savePtt(on);
    this.applyMic();
    this.sendState();
    this.update();
  }

  /** Push to talk: the talk key or button is held (true) or let go. */
  talk(down: boolean) {
    if (!this.view.ptt || this.view.talking === down) return;
    this.view.talking = down;
    this.applyMic();
    this.sendState();
    this.update();
  }

  async setCamera(on: boolean) {
    if (on === this.view.camera || !this.view.videoAllowed) return;
    if (on) {
      const s = await navigator.mediaDevices.getUserMedia({ video: CAMERA });
      this.cam = s.getVideoTracks()[0];
      if (this.ended) {
        this.cam?.stop();
        return;
      }
    } else {
      this.cam?.stop();
      this.cam = undefined;
    }
    this.view.camera = !!this.cam;
    this.view.localCamera = this.cam ? new MediaStream([this.cam]) : undefined;
    await this.attachSlot("camera");
    this.sendState();
    this.update();
  }

  async setScreen(on: boolean) {
    if (on === this.view.screen || !this.view.videoAllowed) return;
    if (on) {
      const s = await navigator.mediaDevices.getDisplayMedia({ video: SCREEN, audio: false });
      const t = s.getVideoTracks()[0];
      if (!t) return;
      if (this.ended) {
        t.stop();
        return;
      }
      t.contentHint = "detail";
      t.onended = () => void this.setScreen(false);
      this.scr = t;
    } else {
      this.scr?.stop();
      this.scr = undefined;
    }
    this.view.screen = !!this.scr;
    this.view.localScreen = this.scr ? new MediaStream([this.scr]) : undefined;
    await this.attachSlot("screen");
    this.sendState();
    this.update();
  }

  // ---- debugging (tests) ----

  /**
   * With localStorage "sd.callDebug" set to "1", exposes window.__sdCall for
   * tests: media statistics, the worker's frame counters and a hook that
   * replaces this device's receive keys with random ones.
   */
  private debugHook() {
    let on = false;
    try {
      on = localStorage.getItem("sd.callDebug") === "1";
    } catch {
      /* storage blocked */
    }
    if (!on) return;
    (window as unknown as { __sdCall: unknown }).__sdCall = {
      view: () => this.view,
      scramble: () => this.worker?.postMessage({ type: "scramble" }),
      // The current call key, so a test can check what the node relayed.
      key: async () => {
        const k = await this.o.engine.mediaKey(this.gid);
        return k && { epoch: k.epoch, hex: Array.from(k.key, (b) => b.toString(16).padStart(2, "0")).join("") };
      },
      stats: async () => {
        const frames = await new Promise((resolve) => {
          const w = this.worker;
          if (!w) return resolve(null);
          const fn = (e: MessageEvent) => {
            if (e.data?.type !== "stats") return;
            w.removeEventListener("message", fn);
            resolve(e.data);
          };
          w.addEventListener("message", fn);
          w.postMessage({ type: "stats" });
        });
        const rtp: Record<string, unknown>[] = [];
        const report = await this.pc?.getStats();
        report?.forEach((s) => {
          if (s.type === "inbound-rtp" || s.type === "outbound-rtp") rtp.push({ ...s });
        });
        return { frames, rtp, mode: this.mode };
      },
    };
  }
}

function sentence(s: string): string {
  const t = s.trim();
  if (!t) return "The call could not be joined.";
  return t.charAt(0).toUpperCase() + t.slice(1) + (/[.!?]$/.test(t) ? "" : ".");
}

const PTT_KEY = "sd.pushToTalk";
function loadPtt(): boolean {
  try {
    return localStorage.getItem(PTT_KEY) === "on";
  } catch {
    return false;
  }
}
function savePtt(on: boolean) {
  try {
    localStorage.setItem(PTT_KEY, on ? "on" : "off");
  } catch {
    /* storage blocked */
  }
}
