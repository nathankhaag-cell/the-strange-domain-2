// Checks the one background shortcut a page may ask for (Toggle mute).
//
// Pages served by the node can ask the app to register a system-wide
// shortcut. To keep that from being used to watch typing, only a single
// shortcut is ever registered, and it must use Ctrl, Alt or the Super/Cmd
// key, or be a function key, so plain letters and words are never taken.
"use strict";

const MODIFIERS = new Set(["Control", "Alt", "Shift", "Super"]);
const STRONG = new Set(["Control", "Alt", "Super"]);
const NAMED = new Set([
  "Space",
  "Tab",
  "Backspace",
  "Delete",
  "Insert",
  "Return",
  "Up",
  "Down",
  "Left",
  "Right",
  "Home",
  "End",
  "PageUp",
  "PageDown",
]);
const PUNCT = new Set(["-", "=", "[", "]", "\\", ";", "'", ",", ".", "/", "`"]);

function isKey(k) {
  return (
    /^[A-Z0-9]$/.test(k) ||
    /^F([1-9]|1[0-9]|2[0-4])$/.test(k) ||
    /^num[0-9]$/.test(k) ||
    NAMED.has(k) ||
    PUNCT.has(k)
  );
}

/** @param {unknown} acc */
function isSafeAccelerator(acc) {
  if (typeof acc !== "string" || acc.length > 64) return false;
  const parts = acc.split("+");
  // "Control++" style (a plus key) is not offered.
  if (parts.some((p) => p === "")) return false;
  const key = parts.pop();
  const mods = parts;
  if (!key || !isKey(key)) return false;
  if (new Set(mods).size !== mods.length) return false;
  if (!mods.every((m) => MODIFIERS.has(m))) return false;
  const fn = /^F([1-9]|1[0-9]|2[0-4])$/.test(key);
  return fn || mods.some((m) => STRONG.has(m));
}

module.exports = { isSafeAccelerator };
