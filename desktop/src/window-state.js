// Remembers the window's size, position, and whether it was maximized or
// full screen, across restarts. Pure functions here; main.js does the I/O
// against the real window and screen.
"use strict";

const fs = require("node:fs");
const path = require("node:path");

const DEFAULTS = { width: 1280, height: 820 };
const MIN = { width: 360, height: 480 };

function num(v) {
  return typeof v === "number" && Number.isFinite(v) ? Math.round(v) : undefined;
}

/**
 * Turns what was saved into BrowserWindow options. The position is kept only
 * if enough of the window (its title bar) would be on one of the displays,
 * so a window saved on a screen that is gone opens centred instead.
 *
 * @param {unknown} saved
 * @param {{x: number, y: number, width: number, height: number}[]} workAreas
 */
function restore(saved, workAreas) {
  const s = saved && typeof saved === "object" ? /** @type {Record<string, unknown>} */ (saved) : {};
  let width = num(s.width) ?? DEFAULTS.width;
  let height = num(s.height) ?? DEFAULTS.height;
  width = Math.max(MIN.width, width);
  height = Math.max(MIN.height, height);
  const out = { width, height, maximized: s.maximized === true, fullscreen: s.fullscreen === true };
  const x = num(s.x);
  const y = num(s.y);
  if (x !== undefined && y !== undefined) {
    const visible = workAreas.some((a) => {
      // The top 32px (the title bar) must overlap the area by at least
      // 100px horizontally, so the window can be grabbed and moved.
      const overlapX = Math.min(x + width, a.x + a.width) - Math.max(x, a.x);
      const topInside = y >= a.y && y + 32 <= a.y + a.height;
      return overlapX >= 100 && topInside;
    });
    if (visible) {
      out.x = x;
      out.y = y;
    }
  }
  // A window bigger than every display would open off-screen.
  const biggest = workAreas.reduce((m, a) => ({ width: Math.max(m.width, a.width), height: Math.max(m.height, a.height) }), {
    width: 0,
    height: 0,
  });
  if (biggest.width > 0) out.width = Math.max(MIN.width, Math.min(out.width, biggest.width));
  if (biggest.height > 0) out.height = Math.max(MIN.height, Math.min(out.height, biggest.height));
  return out;
}

/** What to save: the normal (not maximized) bounds and the two flags. */
function snapshot(normalBounds, maximized, fullscreen) {
  return {
    x: num(normalBounds.x),
    y: num(normalBounds.y),
    width: num(normalBounds.width),
    height: num(normalBounds.height),
    maximized: !!maximized,
    fullscreen: !!fullscreen,
  };
}

function load(file) {
  try {
    return JSON.parse(fs.readFileSync(file, "utf8"));
  } catch {
    return null;
  }
}

function save(file, state) {
  try {
    fs.mkdirSync(path.dirname(file), { recursive: true });
    fs.writeFileSync(file, JSON.stringify(state) + "\n");
  } catch {
    /* not worth bothering anyone about */
  }
}

module.exports = { DEFAULTS, MIN, restore, snapshot, load, save };
