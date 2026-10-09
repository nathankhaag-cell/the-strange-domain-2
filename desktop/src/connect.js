// The connect page: asks for the node's address, checks a node answers
// there, and hands it to the main process.
"use strict";

const $ = (id) => document.getElementById(id);
const form = $("form");
const address = $("address");
const errorBox = $("error");
const submit = $("submit");
const trust = $("trust");
let pending = null;

function showError(msg) {
  errorBox.textContent = msg || "";
  errorBox.hidden = !msg;
}

function busy(on, label) {
  submit.disabled = on;
  submit.textContent = on ? label : "Connect";
}

async function connect(url) {
  busy(true, "Connecting...");
  const r = await window.nodePicker.connect(url);
  if (!r.ok) {
    busy(false);
    showError(r.error);
  }
}

form.addEventListener("submit", async (e) => {
  e.preventDefault();
  showError("");
  trust.hidden = true;
  busy(true, "Checking...");
  const r = await window.nodePicker.probe(address.value);
  if (!r.ok) {
    busy(false);
    showError(r.error);
    return;
  }
  if (r.untrustedFingerprint) {
    pending = r.url;
    $("fingerprint").textContent = r.untrustedFingerprint;
    trust.hidden = false;
    busy(false);
    $("trust-yes").focus();
    return;
  }
  await connect(r.url);
});

$("trust-yes").addEventListener("click", () => {
  trust.hidden = true;
  if (pending) void connect(pending);
});
$("trust-no").addEventListener("click", () => {
  trust.hidden = true;
  pending = null;
  address.focus();
});

(async () => {
  const params = new URLSearchParams(location.search);
  showError(params.get("error"));
  const cur = await window.nodePicker.current();
  if (cur) address.value = cur.replace(/^http:\/\//, "");
  address.focus();
  address.select();
})();
