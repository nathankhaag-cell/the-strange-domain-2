"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { isNewer, parseVersion, compareVersions, updateMode, releasePage, dialogText, RELEASES_PAGE } = require("../src/update-logic");

test("versions compare by semver", () => {
  assert.equal(isNewer("v0.2.0", "0.1.0"), true);
  assert.equal(isNewer("v0.1.0", "0.1.0"), false);
  assert.equal(isNewer("v0.1.0", "0.2.0"), false);
  assert.equal(isNewer("v1.10.0", "1.9.9"), true);
  assert.equal(isNewer("v1.0.0", "1.0.0-beta.3"), true);
  assert.equal(isNewer("v1.0.0-beta.10", "1.0.0-beta.9"), true);
  assert.equal(isNewer("nightly", "1.0.0"), false);
  assert.equal(isNewer("v1.0.0", "dev"), false);
  assert.equal(compareVersions(parseVersion("1.0.0-alpha"), parseVersion("1.0.0-alpha.1")), -1);
  assert.equal(parseVersion("01.0.0"), null);
});

test("only the Windows installer and AppImages install updates themselves", () => {
  assert.equal(updateMode("win32", {}), "install");
  assert.equal(updateMode("linux", { APPIMAGE: "/home/a/The-Strange-Domain.AppImage" }), "install");
  assert.equal(updateMode("linux", {}), "page"); // .deb
  assert.equal(updateMode("darwin", {}), "page");
});

test("the release page link stays on the project's GitHub", () => {
  const ok = "https://github.com/nathankhaag-cell/the-strange-domain-2/releases/tag/v0.2.0";
  assert.equal(releasePage({ html_url: ok }), ok);
  assert.equal(releasePage({ html_url: "https://evil.example/" }), RELEASES_PAGE);
  assert.equal(releasePage(null), RELEASES_PAGE);
});

test("dialogs ask before doing anything", () => {
  const i = dialogText("install", "v0.2.0", "0.1.0");
  assert.deepEqual(i.buttons, ["Update and restart", "Later"]);
  assert.match(i.message, /v0\.2\.0\)\. You have v0\.1\.0\./);
  const p = dialogText("page", "v0.2.0", "0.1.0");
  assert.deepEqual(p.buttons, ["Open download page", "Later"]);
});
