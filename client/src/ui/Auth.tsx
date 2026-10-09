import { useState } from "preact/hooks";
import type { JSX } from "preact";
import { enlist, forgetDevice, linkDevice, reconnect, recoverAccount, useApp } from "../app";
import { Lock } from "./icons";
import { Badge, Field, errText, TAGLINE } from "./common";

type Tab = "enlist" | "reconnect" | "link" | "recover";

export function Auth() {
  const app = useApp();
  const [tab, setTab] = useState<Tab>(app.identity ? "reconnect" : "enlist");

  return (
    <div class="auth">
      <section class="auth-hero" aria-label="The Strange Domain">
        <div class="auth-hero-mark">
          <Badge size="lg" />
          <h1 class="auth-title">
            THE
            <br />
            STRANGE
            <br />
            DOMAIN
            <span class="cursor cursor-lg" aria-hidden="true" />
          </h1>
        </div>
        <div class="auth-hero-foot">
          <p class="tagline">{TAGLINE}</p>
          <dl class="stat-grid">
            <div>
              <dt>NODE</dt>
              <dd>{location.host}</dd>
            </div>
            <div>
              <dt>ENCRYPTION</dt>
              <dd class="accent">MLS E2E</dd>
            </div>
            <div>
              <dt>VERSION</dt>
              <dd>{app.info?.version ?? "OFFLINE"}</dd>
            </div>
          </dl>
        </div>
      </section>

      <section class="auth-panel" aria-label="Sign in">
        <div class="auth-card">
          <div class="tabs" role="tablist">
            {(
              [
                ["enlist", "Enlist"],
                ["reconnect", "Reconnect"],
                ["link", "Link device"],
                ["recover", "Recover"],
              ] as [Tab, string][]
            ).map(([t, label]) => (
              <button
                type="button"
                role="tab"
                aria-selected={tab === t}
                class={tab === t ? "tab active" : "tab"}
                onClick={() => setTab(t)}
              >
                {label}
              </button>
            ))}
          </div>
          {app.notice && <div class="note">{app.notice}</div>}
          {tab === "enlist" && <EnlistForm hasIdentity={!!app.identity} />}
          {tab === "reconnect" && <ReconnectForm />}
          {tab === "link" && <LinkForm hasIdentity={!!app.identity} />}
          {tab === "recover" && <RecoverForm hasIdentity={!!app.identity} />}
        </div>
      </section>
    </div>
  );
}

function useSubmit(fn: () => Promise<void>) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const onSubmit = async (e: JSX.TargetedEvent<HTMLFormElement>) => {
    e.preventDefault();
    setBusy(true);
    setErr("");
    try {
      await fn();
    } catch (x) {
      setErr(errText(x));
    } finally {
      setBusy(false);
    }
  };
  return { busy, err, onSubmit };
}

function KeyNote() {
  return (
    <div class="key-note">
      <Lock />
      <div>This device creates its own key. The private key never leaves this browser.</div>
    </div>
  );
}

function ExistingDevice() {
  const app = useApp();
  return (
    <div class="note">
      This browser already holds the device key for <b>{app.identity?.callsign}</b>. Use Reconnect, or forget this
      device to start over.
      <div>
        <button type="button" class="btn-link" onClick={() => void forgetDevice()}>
          Forget this device
        </button>
      </div>
    </div>
  );
}

function EnlistForm({ hasIdentity }: { hasIdentity: boolean }) {
  const [summons, setSummons] = useState(new URLSearchParams(location.search).get("summons") ?? "");
  const [callsign, setCallsign] = useState("");
  const [device, setDevice] = useState(defaultDeviceName());
  const s = useSubmit(() => enlist(callsign, device, summons));
  if (hasIdentity) return <ExistingDevice />;
  return (
    <form class="auth-form" onSubmit={s.onSubmit}>
      <h2>Create an account</h2>
      <Field id="summons" label="Summons code (optional for the first account)" value={summons} onInput={setSummons} />
      <Field id="callsign" label="Callsign" value={callsign} onInput={setCallsign} required maxLength={32} />
      <Field id="device" label="Device name" value={device} onInput={setDevice} required maxLength={64} />
      <KeyNote />
      {s.err && <div class="error" role="alert">{s.err}</div>}
      <button type="submit" class="btn-primary" disabled={s.busy}>
        ENLIST
      </button>
    </form>
  );
}

function ReconnectForm() {
  const app = useApp();
  const s = useSubmit(() => reconnect());
  if (!app.identity) {
    return (
      <div class="auth-form">
        <h2>Sign in</h2>
        <p class="muted">
          There is no device key in this browser. Enlist with a Summons, link this device from one you already use,
          or recover your account with your recovery code.
        </p>
      </div>
    );
  }
  return (
    <form class="auth-form" onSubmit={s.onSubmit}>
      <h2>Sign in</h2>
      <dl class="kv">
        <dt>Callsign</dt>
        <dd>{app.identity.callsign}</dd>
        <dt>Device</dt>
        <dd>{app.identity.deviceName}</dd>
      </dl>
      {s.err && <div class="error" role="alert">{s.err}</div>}
      <button type="submit" class="btn-primary" disabled={s.busy}>
        RECONNECT
      </button>
      <button type="button" class="btn-link" onClick={() => void forgetDevice()}>
        Forget this device
      </button>
    </form>
  );
}

function LinkForm({ hasIdentity }: { hasIdentity: boolean }) {
  const [code, setCode] = useState("");
  const [device, setDevice] = useState(defaultDeviceName());
  const s = useSubmit(() => linkDevice(code, device));
  if (hasIdentity) return <ExistingDevice />;
  return (
    <form class="auth-form" onSubmit={s.onSubmit}>
      <h2>Link this device</h2>
      <p class="muted">On a device you already use, open Devices and create a link code. It works once, for ten minutes.</p>
      <Field id="linkcode" label="Link code" value={code} onInput={setCode} required placeholder="XXXXX-XXXXX" />
      <Field id="device" label="Device name" value={device} onInput={setDevice} required maxLength={64} />
      <KeyNote />
      {s.err && <div class="error" role="alert">{s.err}</div>}
      <button type="submit" class="btn-primary" disabled={s.busy}>
        Link device
      </button>
    </form>
  );
}

function RecoverForm({ hasIdentity }: { hasIdentity: boolean }) {
  const [callsign, setCallsign] = useState("");
  const [code, setCode] = useState("");
  const [device, setDevice] = useState(defaultDeviceName());
  const s = useSubmit(() => recoverAccount(callsign, code, device));
  if (hasIdentity) return <ExistingDevice />;
  return (
    <form class="auth-form" onSubmit={s.onSubmit}>
      <h2>Recover your account</h2>
      <p class="muted">
        Recovery signs out and removes every other device on your account. Messages kept only on those devices cannot
        be read here.
      </p>
      <Field id="callsign" label="Callsign" value={callsign} onInput={setCallsign} required />
      <Field id="recovery" label="Recovery code" value={code} onInput={setCode} required placeholder="XXXX-XXXX-XXXX-XXXX-XXXX" />
      <Field id="device" label="Device name" value={device} onInput={setDevice} required maxLength={64} />
      {s.err && <div class="error" role="alert">{s.err}</div>}
      <button type="submit" class="btn-primary" disabled={s.busy}>
        Recover
      </button>
    </form>
  );
}

function defaultDeviceName(): string {
  const ua = navigator.userAgent;
  const os = /Android/.test(ua)
    ? "Android"
    : /iPhone|iPad/.test(ua)
      ? "iOS"
      : /Mac OS/.test(ua)
        ? "Mac"
        : /Windows/.test(ua)
          ? "Windows"
          : /Linux/.test(ua)
            ? "Linux"
            : "";
  const browser = /Firefox\//.test(ua) ? "Firefox" : /Edg\//.test(ua) ? "Edge" : /Chrome\//.test(ua) ? "Chrome" : /Safari\//.test(ua) ? "Safari" : "Browser";
  return [browser, os].filter(Boolean).join(" on ");
}
