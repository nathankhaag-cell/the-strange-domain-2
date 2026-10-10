// Key bindings for calls: Toggle mute and Push to talk. Set in Settings and
// saved on this device. They work while the app's window is focused, in
// every build. They are ignored while typing in a text field unless the
// binding uses Ctrl, Alt or Cmd/Meta.
//
// In the desktop app, Toggle mute can also work while the app is in the
// background (an Electron global shortcut, desktop/src/accelerator.js).
// Push to talk cannot: background shortcuts report only that a key was
// pressed, never that it was let go.
//
// A binding is stored as its modifiers and the key's physical code, e.g.
// "Ctrl+Shift+KeyM", so it stays the same whatever the keyboard layout.

import { useEffect, useState } from "preact/hooks";
import { getCall } from "./app";
import { desktop } from "./desktop";

export type Action = "mute" | "ptt";
export interface Bindings {
  mute?: string;
  ptt?: string;
  /** Desktop app: Toggle mute also works in the background. */
  bgMute?: boolean;
}

const KEY = "sd.keys";
const MODS = ["Ctrl", "Alt", "Shift", "Meta"] as const;
const isMac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);

let bindings: Bindings = load();
/** Desktop: whether the background shortcut is registered now. */
let globalOn = false;
let globalError = "";
/** Settings is waiting for a key; bindings are paused meanwhile. */
let capturing = false;
let held: string | null = null;
const listeners = new Set<() => void>();

function load(): Bindings {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) ?? "{}") as Record<string, unknown>;
    const out: Bindings = {};
    if (typeof v.mute === "string" && parseCombo(v.mute)) out.mute = v.mute;
    if (typeof v.ptt === "string" && parseCombo(v.ptt)) out.ptt = v.ptt;
    if (v.bgMute === true) out.bgMute = true;
    return out;
  } catch {
    return {};
  }
}

function persist() {
  try {
    localStorage.setItem(KEY, JSON.stringify(bindings));
  } catch {
    /* private mode: kept until the page closes */
  }
}

function notify() {
  for (const l of listeners) l();
}

export function subscribe(l: () => void): () => void {
  listeners.add(l);
  return () => listeners.delete(l);
}

export function getBindings(): Bindings {
  return bindings;
}

/** The bindings, re-rendering when they change. */
export function useBindings(): Bindings {
  const [, setN] = useState(0);
  useEffect(() => subscribe(() => setN((n) => n + 1)), []);
  return bindings;
}

export function globalStatus(): { on: boolean; error: string } {
  return { on: globalOn, error: globalError };
}

export function setBinding(action: Action, combo: string | undefined) {
  bindings = { ...bindings, [action]: combo };
  if (!combo) delete bindings[action];
  persist();
  void syncGlobal();
  notify();
}

export function setBackgroundMute(on: boolean) {
  bindings = { ...bindings, bgMute: on || undefined };
  if (!on) delete bindings.bgMute;
  persist();
  void syncGlobal();
  notify();
}

export function setCapturing(on: boolean) {
  capturing = on;
  if (on) release();
}

// ---- combos ----

interface Combo {
  mods: Set<string>;
  code: string;
}

function parseCombo(s: string): Combo | null {
  const parts = s.split("+");
  const code = parts.pop();
  if (!code || !/^[A-Za-z0-9]+$/.test(code) || isModifierCode(code)) return null;
  if (!parts.every((p) => (MODS as readonly string[]).includes(p))) return null;
  return { mods: new Set(parts), code };
}

function isModifierCode(code: string): boolean {
  return /^(Control|Shift|Alt|Meta|OS)(Left|Right)?$/.test(code) || code === "CapsLock" || code === "Fn" || code === "FnLock";
}

/** The binding a key press stands for, or null for a modifier on its own. */
export function comboOf(e: KeyboardEvent): string | null {
  if (!e.code || isModifierCode(e.code) || !/^[A-Za-z0-9]+$/.test(e.code)) return null;
  const mods: string[] = [];
  if (e.ctrlKey) mods.push("Ctrl");
  if (e.altKey) mods.push("Alt");
  if (e.shiftKey) mods.push("Shift");
  if (e.metaKey) mods.push("Meta");
  return [...mods, e.code].join("+");
}

const PUNCT: Record<string, string> = {
  Minus: "-",
  Equal: "=",
  BracketLeft: "[",
  BracketRight: "]",
  Backslash: "\\",
  Semicolon: ";",
  Quote: "'",
  Comma: ",",
  Period: ".",
  Slash: "/",
  Backquote: "`",
};
const NAMED: Record<string, string> = {
  ArrowUp: "Up",
  ArrowDown: "Down",
  ArrowLeft: "Left",
  ArrowRight: "Right",
  Enter: "Enter",
  Space: "Space",
};

function keyLabel(code: string): string {
  if (/^Key[A-Z]$/.test(code)) return code.slice(3);
  if (/^Digit[0-9]$/.test(code)) return code.slice(5);
  if (/^Numpad[0-9]$/.test(code)) return "Num " + code.slice(6);
  if (code in PUNCT) return PUNCT[code];
  if (code in NAMED) return NAMED[code];
  return code;
}

/** How a binding is shown, e.g. "Ctrl+Shift+M". */
export function comboLabel(s: string | undefined): string {
  const c = s ? parseCombo(s) : null;
  if (!c) return "";
  const names: Record<string, string> = { Ctrl: "Ctrl", Alt: isMac ? "Option" : "Alt", Shift: "Shift", Meta: isMac ? "Cmd" : "Meta" };
  return [...MODS.filter((m) => c.mods.has(m)).map((m) => names[m]), keyLabel(c.code)].join("+");
}

/**
 * The Electron accelerator for a binding, or null when it cannot be a
 * background shortcut: it must use Ctrl, Alt or Cmd/Meta, or be a function
 * key, so plain typing is never taken from other apps.
 */
export function toAccelerator(s: string | undefined): string | null {
  const c = s ? parseCombo(s) : null;
  if (!c) return null;
  let key: string | null = null;
  const code = c.code;
  if (/^Key[A-Z]$/.test(code)) key = code.slice(3);
  else if (/^Digit[0-9]$/.test(code)) key = code.slice(5);
  else if (/^Numpad[0-9]$/.test(code)) key = "num" + code.slice(6);
  else if (/^F([1-9]|1[0-9]|2[0-4])$/.test(code)) key = code;
  else if (code in PUNCT) key = PUNCT[code];
  else if (code === "Enter") key = "Return";
  else if (code.startsWith("Arrow")) key = code.slice(5);
  else if (["Space", "Tab", "Backspace", "Delete", "Insert", "Home", "End", "PageUp", "PageDown"].includes(code)) key = code;
  if (!key) return null;
  const strong = c.mods.has("Ctrl") || c.mods.has("Alt") || c.mods.has("Meta");
  if (!strong && !/^F\d+$/.test(key)) return null;
  const mods: string[] = [];
  if (c.mods.has("Ctrl")) mods.push("Control");
  if (c.mods.has("Alt")) mods.push("Alt");
  if (c.mods.has("Shift")) mods.push("Shift");
  if (c.mods.has("Meta")) mods.push("Super");
  return [...mods, key].join("+");
}

// ---- acting on a binding ----

function toggleMute() {
  const s = getCall();
  if (!s) return;
  const v = s.view;
  // Same rule as the Mute button: not in push-to-talk mode, and only with a
  // working microphone and permission to speak.
  if (v.ptt || !v.canSpeak || !v.micOk) return;
  s.setMuted(!v.muted);
}

function release() {
  if (held) {
    held = null;
    getCall()?.talk(false);
  }
}

function typingIn(t: EventTarget | null): boolean {
  if (!(t instanceof HTMLElement)) return false;
  if (t.isContentEditable) return true;
  if (t.tagName === "TEXTAREA" || t.tagName === "SELECT") return true;
  if (t.tagName === "INPUT") {
    const type = (t as HTMLInputElement).type;
    return !["checkbox", "radio", "button", "submit", "reset", "range", "color", "file"].includes(type);
  }
  return false;
}

function onKeyDown(e: KeyboardEvent) {
  if (capturing) return;
  const combo = comboOf(e);
  if (!combo) return;
  const strong = e.ctrlKey || e.altKey || e.metaKey;
  if (!strong && typingIn(e.target)) return;
  // Space and Enter press a focused button; leave them to it.
  if (!strong && (e.code === "Space" || e.code === "Enter") && e.target instanceof HTMLElement && e.target.closest("button, a, [role=button]")) return;
  const muteHere = combo === bindings.mute && !globalOn;
  const ptt = combo === bindings.ptt;
  if (!muteHere && !ptt) return;
  e.preventDefault();
  if (e.repeat) return;
  if (muteHere) toggleMute();
  if (ptt && getCall()?.view.ptt) {
    held = combo;
    getCall()?.talk(true);
  }
}

function onKeyUp(e: KeyboardEvent) {
  if (!held) return;
  const c = parseCombo(held);
  if (!c) return release();
  const modLetGo =
    (c.mods.has("Ctrl") && !e.ctrlKey) || (c.mods.has("Alt") && !e.altKey) || (c.mods.has("Shift") && !e.shiftKey) || (c.mods.has("Meta") && !e.metaKey);
  if (e.code === c.code || modLetGo) release();
}

async function syncGlobal() {
  const d = desktop();
  if (!d) return;
  const acc = bindings.bgMute ? toAccelerator(bindings.mute) : null;
  globalError = "";
  if (bindings.bgMute && bindings.mute && !acc) {
    globalError = "This shortcut cannot work in the background. Use one with Ctrl, Alt or a function key.";
  }
  let ok = false;
  try {
    ok = await d.setGlobalMute(acc);
  } catch {
    ok = false;
  }
  globalOn = !!acc && ok;
  if (acc && !ok) globalError = "Could not set this shortcut in the background. Another app may be using it.";
  notify();
}

let installed = false;

/** Starts listening for the bindings. Call once at start-up. */
export function installHotkeys() {
  if (installed || typeof window === "undefined") return;
  installed = true;
  window.addEventListener("keydown", onKeyDown);
  window.addEventListener("keyup", onKeyUp);
  window.addEventListener("blur", release);
  const d = desktop();
  if (d) {
    d.onGlobalMute(() => toggleMute());
    void syncGlobal();
  }
}
