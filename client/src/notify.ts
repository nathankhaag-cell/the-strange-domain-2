// Telling the person a message arrived: a short tone and an OS notification.
//
// The OS notification never includes message text unless the person turns
// previews on: by default it only says which chat has a new message.
//
// - Browsers and the desktop app use the Notification API.
// - The Android app uses Capacitor's LocalNotifications plugin when it is
//   present. Android pauses the app's WebView soon after it goes to the
//   background, so notifications there only arrive while the app is running.

const SOUND_KEY = "sd.sound";
const NOTIFY_KEY = "sd.notify";
const PREVIEW_KEY = "sd.notifyPreview";
const ASKED_KEY = "sd.notifyAsked";

function lsGet(k: string): string | null {
  try {
    return localStorage.getItem(k);
  } catch {
    return null;
  }
}
function lsSet(k: string, v: string) {
  try {
    localStorage.setItem(k, v);
  } catch {
    /* storage blocked */
  }
}

export interface NotifyPrefs {
  sound: boolean;
  notify: boolean;
  preview: boolean;
}

export function loadPrefs(): NotifyPrefs {
  return {
    sound: lsGet(SOUND_KEY) !== "off",
    notify: lsGet(NOTIFY_KEY) === "on",
    preview: lsGet(PREVIEW_KEY) === "on",
  };
}

export function savePrefs(p: NotifyPrefs) {
  lsSet(SOUND_KEY, p.sound ? "on" : "off");
  lsSet(NOTIFY_KEY, p.notify ? "on" : "off");
  lsSet(PREVIEW_KEY, p.preview ? "on" : "off");
}

/** Whether the one-time "turn on notifications?" prompt was answered. */
export function askedBefore(): boolean {
  return lsGet(ASKED_KEY) === "1";
}
export function markAsked() {
  lsSet(ASKED_KEY, "1");
}

// ---- sound ----

let audio: AudioContext | null = null;

function audioContext(): AudioContext | null {
  if (audio) return audio;
  const AC = window.AudioContext ?? (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
  if (!AC) return null;
  try {
    audio = new AC();
  } catch {
    return null;
  }
  return audio;
}

// Browsers only let audio start after the person has interacted with the
// page, so unlock it on the first tap or key press.
if (typeof window !== "undefined") {
  const unlock = () => {
    const ctx = audioContext();
    void ctx?.resume().catch(() => undefined);
    window.removeEventListener("pointerdown", unlock);
    window.removeEventListener("keydown", unlock);
  };
  window.addEventListener("pointerdown", unlock);
  window.addEventListener("keydown", unlock);
}

let noise: AudioBuffer | null = null;

/** 100 ms of white noise, made once and reused for the crackle. */
function noiseBuffer(ctx: AudioContext): AudioBuffer {
  if (noise && noise.sampleRate === ctx.sampleRate) return noise;
  const len = Math.ceil(ctx.sampleRate * 0.1);
  noise = ctx.createBuffer(1, len, ctx.sampleRate);
  const d = noise.getChannelData(0);
  for (let i = 0; i < len; i++) d[i] = Math.random() * 2 - 1;
  return noise;
}

/** A faint burst of low-passed noise, like the crackle of a valve warming up. */
function crackle(ctx: AudioContext, out: AudioNode, t: number, dur: number, peak: number) {
  const src = ctx.createBufferSource();
  src.buffer = noiseBuffer(ctx);
  const low = ctx.createBiquadFilter();
  low.type = "lowpass";
  low.frequency.value = 900;
  const g = ctx.createGain();
  g.gain.setValueAtTime(0.0001, t);
  g.gain.exponentialRampToValueAtTime(peak, t + 0.01);
  g.gain.exponentialRampToValueAtTime(0.0001, t + dur);
  src.connect(low).connect(g).connect(out);
  src.start(t);
  src.stop(t + dur + 0.01);
}

/**
 * The message sound: a vacuum tube hum, made with WebAudio so no sound file
 * is shipped. About 600 ms: a 60 Hz sawtooth whose low-pass filter opens and
 * closes like a valve warming up and settling, with a faint crackle at the
 * start. The harmonics keep it audible on small laptop and phone speakers.
 * Peaks stay far below full scale, so it never clips.
 */
export function playTone() {
  const ctx = audioContext();
  if (!ctx) return;
  if (ctx.state === "suspended") void ctx.resume().catch(() => undefined);
  const t = ctx.currentTime + 0.01;

  const out = ctx.createGain();
  out.gain.value = 1;
  out.connect(ctx.destination);

  crackle(ctx, out, t, 0.08, 0.025);

  const osc = ctx.createOscillator();
  osc.type = "sawtooth";
  osc.frequency.setValueAtTime(60, t);
  const warm = ctx.createBiquadFilter();
  warm.type = "lowpass";
  warm.Q.value = 4;
  warm.frequency.setValueAtTime(140, t);
  warm.frequency.exponentialRampToValueAtTime(700, t + 0.18);
  warm.frequency.exponentialRampToValueAtTime(260, t + 0.6);
  const g = ctx.createGain();
  g.gain.setValueAtTime(0.0001, t);
  g.gain.exponentialRampToValueAtTime(0.09, t + 0.06);
  g.gain.setValueAtTime(0.09, t + 0.35);
  g.gain.exponentialRampToValueAtTime(0.0001, t + 0.6);
  osc.connect(warm).connect(g).connect(out);
  osc.start(t);
  osc.stop(t + 0.62);

  osc.onended = () => setTimeout(() => out.disconnect(), 100);
}

// ---- OS notifications ----

interface LocalNotificationsPlugin {
  checkPermissions(): Promise<{ display: string }>;
  requestPermissions(): Promise<{ display: string }>;
  schedule(o: { notifications: { id: number; title: string; body: string; extra?: unknown }[] }): Promise<unknown>;
  addListener?(ev: string, fn: (a: { notification: { extra?: { gid?: string } } }) => void): unknown;
}

function nativePlugin(): LocalNotificationsPlugin | undefined {
  const cap = (window as unknown as {
    Capacitor?: { isNativePlatform?: () => boolean; Plugins?: { LocalNotifications?: LocalNotificationsPlugin } };
  }).Capacitor;
  if (!cap?.isNativePlatform?.()) return undefined;
  return cap.Plugins?.LocalNotifications;
}

export type Permission = "granted" | "denied" | "default" | "unsupported";

export async function permission(): Promise<Permission> {
  const native = nativePlugin();
  if (native) {
    try {
      const r = await native.checkPermissions();
      return r.display === "granted" ? "granted" : r.display === "denied" ? "denied" : "default";
    } catch {
      return "unsupported";
    }
  }
  if (typeof Notification === "undefined") return "unsupported";
  return Notification.permission;
}

export async function requestPermission(): Promise<Permission> {
  const native = nativePlugin();
  if (native) {
    try {
      const r = await native.requestPermissions();
      return r.display === "granted" ? "granted" : "denied";
    } catch {
      return "unsupported";
    }
  }
  if (typeof Notification === "undefined") return "unsupported";
  try {
    return await Notification.requestPermission();
  } catch {
    return "unsupported";
  }
}

let onOpen: ((gid: string) => void) | null = null;
let listening = false;

/** What to do when the person taps a notification. */
export function onNotificationOpen(fn: (gid: string) => void) {
  onOpen = fn;
  const native = nativePlugin();
  if (native?.addListener && !listening) {
    listening = true;
    native.addListener("localNotificationActionPerformed", (a) => {
      const gid = a.notification.extra?.gid;
      if (gid && onOpen) onOpen(gid);
    });
  }
}

let nextId = 1;

export async function showNotification(gid: string, title: string, body: string) {
  const native = nativePlugin();
  if (native) {
    try {
      await native.schedule({ notifications: [{ id: nextId++, title, body, extra: { gid } }] });
    } catch (e) {
      console.warn("notification", e);
    }
    return;
  }
  if (typeof Notification === "undefined" || Notification.permission !== "granted") return;
  try {
    const n = new Notification(title, { body, tag: gid, silent: true });
    n.onclick = () => {
      window.focus();
      onOpen?.(gid);
      n.close();
    };
  } catch (e) {
    console.warn("notification", e);
  }
}
