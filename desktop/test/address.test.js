"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const { normalize, needsSecureSwitch } = require("../src/address");

test("normalize fills in scheme and default port", () => {
  assert.equal(normalize("192.168.1.20"), "http://192.168.1.20:8743");
  assert.equal(normalize(" 192.168.1.20:8743 "), "http://192.168.1.20:8743");
  assert.equal(normalize("localhost:9000"), "http://localhost:9000");
  assert.equal(normalize("pi.local"), "http://pi.local:8743");
  assert.equal(normalize("http://pi.local"), "http://pi.local");
  assert.equal(normalize("https://node.example.com/some/path?x=1"), "https://node.example.com");
  assert.equal(normalize("HTTPS://Node.Example.com:8743"), "https://node.example.com:8743");
  assert.equal(normalize("[::1]:8743"), "http://[::1]:8743");
});

test("normalize rejects what is not a node address", () => {
  for (const bad of ["", "   ", "ftp://x", "file:///etc/passwd", "javascript://alert(1)", "http://user:pw@host", "http://"]) {
    assert.throws(() => normalize(bad), undefined, bad);
  }
});

test("only remote plain-http origins need the secure switch", () => {
  assert.equal(needsSecureSwitch("http://192.168.1.20:8743"), true);
  assert.equal(needsSecureSwitch("http://pi.local:8743"), true);
  assert.equal(needsSecureSwitch("https://192.168.1.20:8743"), false);
  assert.equal(needsSecureSwitch("http://localhost:8743"), false);
  assert.equal(needsSecureSwitch("http://127.0.0.1:8743"), false);
  assert.equal(needsSecureSwitch("http://[::1]:8743"), false);
});
