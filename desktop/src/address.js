// Node addresses: what the person types, turned into an origin.
"use strict";

const DEFAULT_PORT = "8743";

/**
 * Turns what the person typed into an origin such as http://192.168.1.20:8743.
 * With no scheme it assumes http:// and, with no port, the node's default port.
 */
function normalize(input) {
  let s = String(input ?? "").trim();
  if (!s) throw new Error("Enter the node's address.");
  const hasScheme = /^[a-z][a-z0-9+.-]*:\/\//i.test(s);
  if (!hasScheme) s = "http://" + s;
  let u;
  try {
    u = new URL(s);
  } catch {
    throw new Error("That is not a valid address.");
  }
  if (u.protocol !== "http:" && u.protocol !== "https:") throw new Error("The address must start with http:// or https://.");
  if (u.username || u.password) throw new Error("The address must not contain a user name or password.");
  if (!u.hostname) throw new Error("That is not a valid address.");
  if (!hasScheme && !u.port) u.port = DEFAULT_PORT;
  return u.origin;
}

function isLoopback(hostname) {
  return hostname === "localhost" || hostname === "127.0.0.1" || hostname === "[::1]";
}

/** Plain-http origins other than localhost need the secure-origin switch. */
function needsSecureSwitch(origin) {
  const u = new URL(origin);
  return u.protocol === "http:" && !isLoopback(u.hostname);
}

module.exports = { normalize, isLoopback, needsSecureSwitch };
