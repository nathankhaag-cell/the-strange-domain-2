import { useEffect, useState } from "preact/hooks";
import type { JSX } from "preact";
import { api, Perm, type Ban, type Device } from "../api";
import { has as nodeHas } from "../compat";
import {
  acceptSummons,
  createChannel,
  createConclave,
  createDomain,
  moderate,
  refreshDevices,
  revokeDevice,
  setRecoveryCode,
  useApp,
  openGroup,
} from "../app";
import { newRecoveryCode } from "../keys";
import { Field, Modal, errText } from "./common";
import { actorFor, canActOn, canLiftBan, grantableRoles, has, roleBadge } from "./perms";

function useAction() {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const run = async (fn: () => Promise<void>) => {
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
  return { busy, err, run };
}

const prevent = (fn: () => void) => (e: JSX.TargetedEvent<HTMLFormElement>) => {
  e.preventDefault();
  fn();
};

export function AddDomainModal({ onClose }: { onClose: () => void }) {
  const [name, setName] = useState("");
  const [code, setCode] = useState("");
  const a = useAction();
  const b = useAction();
  return (
    <Modal title="Domains" onClose={onClose}>
      <form class="modal-form" onSubmit={prevent(() => a.run(async () => (await createDomain(name), onClose())))}>
        <h3>Create a domain</h3>
        <Field id="dname" label="Domain name" value={name} onInput={setName} required maxLength={64} />
        {a.err && <div class="error">{a.err}</div>}
        <button type="submit" class="btn-primary" disabled={a.busy}>
          Create domain
        </button>
      </form>
      <form class="modal-form" onSubmit={prevent(() => b.run(async () => (await acceptSummons(code), onClose())))}>
        <h3>Accept a Summons</h3>
        <Field id="scode" label="Summons code" value={code} onInput={setCode} required />
        {b.err && <div class="error">{b.err}</div>}
        <button type="submit" class="btn-primary" disabled={b.busy}>
          Accept Summons
        </button>
      </form>
    </Modal>
  );
}

export function ChannelModal({ domainId, onClose }: { domainId: string; onClose: () => void }) {
  const [name, setName] = useState("");
  const [kind, setKind] = useState<"text" | "voice">("text");
  const a = useAction();
  return (
    <Modal title="New Chapel or Voice Relay" onClose={onClose}>
      <form class="modal-form" onSubmit={prevent(() => a.run(async () => (await createChannel(domainId, name, kind), onClose())))}>
        <Field id="cname" label="Name" value={name} onInput={setName} required maxLength={64} />
        <fieldset class="radios">
          <legend>Kind</legend>
          <label>
            <input type="radio" name="kind" checked={kind === "text"} onChange={() => setKind("text")} /> Chapel (text)
          </label>
          <label>
            <input type="radio" name="kind" checked={kind === "voice"} onChange={() => setKind("voice")} /> Voice Relay
          </label>
        </fieldset>
        {a.err && <div class="error">{a.err}</div>}
        <button type="submit" class="btn-primary" disabled={a.busy}>
          Create
        </button>
      </form>
    </Modal>
  );
}

export function SummonsModal({ domainId, onClose }: { domainId: string; onClose: () => void }) {
  const [uses, setUses] = useState("1");
  const [hours, setHours] = useState("24");
  const [code, setCode] = useState("");
  const a = useAction();
  const create = () =>
    a.run(async () => {
      const r = await api.createSummons(domainId, Math.max(0, parseInt(uses) || 0), Math.max(0, parseInt(hours) || 0) * 3600);
      setCode(r.summons);
    });
  return (
    <Modal title="Create a Summons" onClose={onClose}>
      {code ? (
        <div class="modal-form">
          <p>Send this code to the person you are inviting. They enter it when they enlist, or under + if they already have an account.</p>
          <CopyCode value={code} />
          <button type="button" class="btn-outline" onClick={onClose}>
            Done
          </button>
        </div>
      ) : (
        <form class="modal-form" onSubmit={prevent(create)}>
          <Field id="uses" label="Uses (0 = unlimited)" value={uses} onInput={setUses} type="number" />
          <Field id="hours" label="Expires after hours (0 = never)" value={hours} onInput={setHours} type="number" />
          {a.err && <div class="error">{a.err}</div>}
          <button type="submit" class="btn-primary" disabled={a.busy}>
            Create Summons
          </button>
        </form>
      )}
    </Modal>
  );
}

function CopyCode({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div class="code-box">
      <code data-testid="code">{value}</code>
      <button
        type="button"
        class="btn-small"
        onClick={() =>
          navigator.clipboard?.writeText(value).then(
            () => setCopied(true),
            () => setCopied(false),
          )
        }
      >
        {copied ? "Copied" : "Copy"}
      </button>
    </div>
  );
}

export function ConclaveModal({ onClose }: { onClose: () => void }) {
  const app = useApp();
  const [picked, setPicked] = useState<string[]>([]);
  const a = useAction();
  const people = new Map<string, string>();
  for (const d of Object.values(app.details)) {
    for (const m of d.members) if (m.user_id !== app.me?.user_id) people.set(m.user_id, m.callsign);
  }
  const list = Array.from(people.entries()).sort((x, y) => x[1].localeCompare(y[1]));
  const toggle = (id: string) => setPicked(picked.includes(id) ? picked.filter((x) => x !== id) : [...picked, id]);
  return (
    <Modal title="New Confession or Conclave" onClose={onClose}>
      <form class="modal-form" onSubmit={prevent(() => a.run(async () => (await createConclave(picked), onClose())))}>
        <p class="muted">Pick one person for a Confession, or several for a Conclave. You can choose anyone who shares a domain with you.</p>
        <div class="checklist">
          {list.length === 0 && <div class="muted">Nobody shares a domain with you yet.</div>}
          {list.map(([id, name]) => (
            <label>
              <input type="checkbox" checked={picked.includes(id)} onChange={() => toggle(id)} /> {name}
            </label>
          ))}
        </div>
        {a.err && <div class="error">{a.err}</div>}
        <button type="submit" class="btn-primary" disabled={a.busy || picked.length === 0}>
          {picked.length <= 1 ? "Start Confession" : "Start Conclave"}
        </button>
      </form>
    </Modal>
  );
}

export function MemberModal({ domainId, userId, onClose }: { domainId: string; userId: string; onClose: () => void }) {
  const app = useApp();
  const detail = app.details[domainId];
  const member = detail?.members.find((m) => m.user_id === userId);
  const role = detail?.roles.find((r) => r.id === member?.role_id);
  const me = app.me ? actorFor(app, domainId, app.me.user_id) : undefined;
  const target = actorFor(app, domainId, userId);
  const [reason, setReason] = useState("");
  const [banFor, setBanFor] = useState(BAN_LENGTHS[0].seconds);
  const a = useAction();
  if (!detail || !member || !me || !target) {
    return (
      <Modal title="Member" onClose={onClose}>
        <p>This person is no longer in the domain.</p>
      </Modal>
    );
  }
  const isMe = userId === app.me?.user_id;
  const grantable = isMe ? [] : grantableRoles(me, target, detail.roles, member.role_id);
  const canRole = grantable.length > 0;
  const timedBans = nodeHas(app.info, "bans");
  const length = BAN_LENGTHS.find((l) => l.seconds === banFor) ?? BAN_LENGTHS[0];
  const canMute = !isMe && canActOn(me, Perm.Mute, target);
  const canKick = !isMe && canActOn(me, Perm.Kick, target);
  const canBan = !isMe && canActOn(me, Perm.Ban, target);
  const act = (fn: () => Promise<void>, closeAfter = false) =>
    a.run(async () => {
      await moderate(domainId, fn);
      if (closeAfter) onClose();
    });

  return (
    <Modal title={member.callsign} onClose={onClose}>
      <div class="modal-form">
        <dl class="kv">
          <dt>Role</dt>
          <dd>{roleBadge(app, role, userId)}</dd>
          {member.muted && (
            <>
              <dt>Status</dt>
              <dd>Muted</dd>
            </>
          )}
        </dl>
        {!isMe && (
          <button
            type="button"
            class="btn-outline"
            onClick={() =>
              a.run(async () => {
                const existing = app.conclaves.find(
                  (c) => c.members.length === 2 && c.members.some((m) => m.user_id === userId),
                );
                if (existing) await openGroup(existing.id);
                else await createConclave([userId]);
                onClose();
              })
            }
          >
            Open Confession
          </button>
        )}
        {canRole && (
          <label class="field">
            <span>Change role</span>
            <select
              value=""
              onChange={(e) => {
                const rid = (e.target as HTMLSelectElement).value;
                if (rid) void act(() => api.assignRole(domainId, userId, rid));
              }}
            >
              <option value="">Choose a role</option>
              {grantable.map((r) => (
                <option value={r.id}>{r.name}</option>
              ))}
            </select>
          </label>
        )}
        {canMute && (
          <button type="button" class="btn-outline" disabled={a.busy} onClick={() => act(() => api.mute(domainId, userId, !member.muted))}>
            {member.muted ? "Unmute" : "Mute"}
          </button>
        )}
        {canKick && (
          <button
            type="button"
            class="btn-danger"
            disabled={a.busy}
            onClick={() => confirm(`Kick ${member.callsign}?`) && act(() => api.kick(domainId, userId), true)}
          >
            Kick
          </button>
        )}
        {canBan && (
          <>
            {timedBans && (
              <label class="field">
                <span>Ban length</span>
                <select value={String(banFor)} onChange={(e) => setBanFor(Number((e.target as HTMLSelectElement).value))}>
                  {BAN_LENGTHS.map((l) => (
                    <option value={String(l.seconds)}>{l.label}</option>
                  ))}
                </select>
              </label>
            )}
            <Field id="reason" label="Ban reason (optional)" value={reason} onInput={setReason} maxLength={200} />
            <button
              type="button"
              class="btn-danger"
              disabled={a.busy}
              onClick={() =>
                confirm(
                  !timedBans
                    ? `Ban ${member.callsign}?`
                    : length.seconds === 0
                      ? `Ban ${member.callsign} permanently?`
                      : `Ban ${member.callsign} for ${length.label}?`,
                ) && act(() => api.ban(domainId, userId, reason, timedBans ? length.seconds : undefined), true)
              }
            >
              Ban
            </button>
          </>
        )}
        {!isMe && !canRole && !canMute && !canKick && !canBan && has(me, Perm.Kick) && (
          <p class="muted small">You cannot act on someone of equal or higher rank.</p>
        )}
        {a.err && <div class="error">{a.err}</div>}
      </div>
    </Modal>
  );
}

/** Ban lengths offered when banning; 0 is permanent. */
const BAN_LENGTHS = [
  { seconds: 3600, label: "1 hour" },
  { seconds: 24 * 3600, label: "24 hours" },
  { seconds: 7 * 24 * 3600, label: "7 days" },
  { seconds: 30 * 24 * 3600, label: "30 days" },
  { seconds: 0, label: "Permanent" },
];

/** How long a ban has left, e.g. "3 days left". */
function timeLeft(expiresAt: number, nowMs: number): string {
  if (expiresAt === 0) return "Permanent";
  const s = expiresAt - Math.floor(nowMs / 1000);
  const n = (v: number, unit: string) => `${v} ${unit}${v === 1 ? "" : "s"} left`;
  if (s >= 86400) return n(Math.floor(s / 86400), "day");
  if (s >= 3600) return n(Math.floor(s / 3600), "hour");
  if (s >= 60) return n(Math.floor(s / 60), "minute");
  return "Less than a minute left";
}

export function BansModal({ domainId, onClose }: { domainId: string; onClose: () => void }) {
  const app = useApp();
  const me = app.me ? actorFor(app, domainId, app.me.user_id) : undefined;
  const [bans, setBans] = useState<Ban[] | null>(null);
  const [now, setNow] = useState(Date.now());
  const a = useAction();
  const load = () => a.run(async () => setBans(await api.bans(domainId)));
  // Reload when the domain changes (a ban, an unban or an expired ban).
  useEffect(() => void load(), [domainId, app.details[domainId]]);
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 15_000);
    return () => clearInterval(t);
  }, []);
  const shown = (bans ?? []).filter((b) => b.expires_at === 0 || b.expires_at * 1000 > now);
  return (
    <Modal title="Bans" onClose={onClose}>
      <div class="modal-form">
        {bans && shown.length === 0 && <p>No one is banned.</p>}
        <ul class="device-list">
          {shown.map((b) => (
            <li>
              <div>
                <div>{b.callsign}</div>
                <div class="muted small">
                  {timeLeft(b.expires_at, now)}
                  {b.banned_by && ` · Banned by ${b.banned_by}`}
                </div>
                {b.reason && <div class="muted small">Reason: {b.reason}</div>}
              </div>
              {me && canLiftBan(me, b.former_rank) && (
                <button
                  type="button"
                  class="btn-outline btn-small"
                  disabled={a.busy}
                  onClick={() => confirm(`Unban ${b.callsign}?`) && a.run(() => moderate(domainId, () => api.unban(domainId, b.user_id)))}
                >
                  Unban
                </button>
              )}
            </li>
          ))}
        </ul>
        {a.err && <div class="error">{a.err}</div>}
      </div>
    </Modal>
  );
}

export function DevicesModal({ onClose }: { onClose: () => void }) {
  const app = useApp();
  const [link, setLink] = useState("");
  const [recovery, setRecovery] = useState("");
  const [saved, setSaved] = useState(false);
  const a = useAction();
  useEffect(() => {
    void refreshDevices();
  }, []);
  return (
    <Modal title="Devices" onClose={onClose}>
      <div class="modal-form">
        <ul class="device-list">
          {app.devices.map((d: Device) => (
            <li>
              <div>
                <div>
                  {d.name} {d.id === app.me?.device_id && <span class="tag">THIS DEVICE</span>}
                </div>
                <div class="muted small">Added {new Date(d.created_at * 1000).toLocaleString()}</div>
              </div>
              <button
                type="button"
                class="btn-danger btn-small"
                disabled={a.busy}
                onClick={() =>
                  confirm(
                    d.id === app.me?.device_id
                      ? "Revoke this device? It will be signed out and its local data erased."
                      : `Revoke ${d.name}? It will be signed out and removed from your groups.`,
                  ) && a.run(() => revokeDevice(d.id))
                }
              >
                Revoke
              </button>
            </li>
          ))}
        </ul>

        <h3>Link a new device</h3>
        {link ? (
          <>
            <p>On the new device, choose Link device and enter this code. It works once, within ten minutes.</p>
            <CopyCode value={link} />
          </>
        ) : (
          <button
            type="button"
            class="btn-outline"
            disabled={a.busy}
            onClick={() => a.run(async () => setLink((await api.linkCode()).code))}
          >
            Create link code
          </button>
        )}

        <h3>Recovery code</h3>
        {saved ? (
          <p>Recovery code saved. Keep it somewhere safe and offline.</p>
        ) : recovery ? (
          <>
            <p>Write this code down and keep it offline. It is shown only once. Saving it replaces any earlier recovery code.</p>
            <CopyCode value={recovery} />
            <button
              type="button"
              class="btn-primary"
              disabled={a.busy}
              onClick={() =>
                a.run(async () => {
                  await setRecoveryCode(recovery);
                  setSaved(true);
                })
              }
            >
              I have written it down. Save
            </button>
          </>
        ) : (
          <button type="button" class="btn-outline" onClick={() => setRecovery(newRecoveryCode())}>
            Set recovery code
          </button>
        )}
        {a.err && <div class="error">{a.err}</div>}
      </div>
    </Modal>
  );
}
