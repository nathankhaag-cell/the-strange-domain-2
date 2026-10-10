"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const { restore, snapshot, MIN } = require("../src/window-state");
const { isSafeAccelerator } = require("../src/accelerator");

const screen = [{ x: 0, y: 0, width: 1920, height: 1040 }];

test("restore uses defaults when nothing was saved", () => {
  assert.deepEqual(restore(null, screen), { width: 1280, height: 820, maximized: false, fullscreen: false });
  assert.deepEqual(restore("junk", screen), { width: 1280, height: 820, maximized: false, fullscreen: false });
});

test("restore keeps a visible position, size and flags", () => {
  const r = restore({ x: 100, y: 50, width: 900, height: 700, maximized: true, fullscreen: true }, screen);
  assert.deepEqual(r, { x: 100, y: 50, width: 900, height: 700, maximized: true, fullscreen: true });
});

test("restore drops a position on a display that is gone", () => {
  const r = restore({ x: 3000, y: 50, width: 900, height: 700 }, screen);
  assert.equal(r.x, undefined);
  assert.equal(r.y, undefined);
  const above = restore({ x: 100, y: -500, width: 900, height: 700 }, screen);
  assert.equal(above.x, undefined);
  const second = restore({ x: 2000, y: 100, width: 900, height: 700 }, [...screen, { x: 1920, y: 0, width: 1280, height: 1000 }]);
  assert.equal(second.x, 2000);
});

test("restore enforces the minimum and fits the biggest display", () => {
  const small = restore({ width: 10, height: 10 }, screen);
  assert.equal(small.width, MIN.width);
  assert.equal(small.height, MIN.height);
  const big = restore({ width: 9000, height: 9000 }, screen);
  assert.equal(big.width, 1920);
  assert.equal(big.height, 1040);
  assert.equal(restore({ width: NaN, height: "x" }, screen).width, 1280);
});

test("snapshot rounds and keeps the flags", () => {
  assert.deepEqual(snapshot({ x: 1.4, y: 2.6, width: 800, height: 600 }, 1, 0), {
    x: 1,
    y: 3,
    width: 800,
    height: 600,
    maximized: true,
    fullscreen: false,
  });
});

test("only strong background shortcuts are allowed", () => {
  for (const ok of ["Control+Shift+M", "Alt+M", "Super+F", "F8", "Shift+F9", "Control+Space", "Control+Alt+num5", "Alt+-", "F24"]) {
    assert.equal(isSafeAccelerator(ok), true, ok);
  }
  const bad = ["M", "Shift+M", "Space", "Control", "Control+", "Control++", "Control+Control+M", "Ctrl+M", "Hyper+M", "F25", "Control+MM", "", null, 5];
  bad.push("Alt+" + "x".repeat(70));
  for (const b of bad) {
    assert.equal(isSafeAccelerator(b), false, String(b));
  }
});
