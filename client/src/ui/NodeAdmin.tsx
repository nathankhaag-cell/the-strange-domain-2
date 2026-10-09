// The node admin's view of the node: traffic, totals, per-domain counts and
// recent errors, from GET /api/v1/admin/stats (admin only; the node answers
// 403 to everyone else). It never shows message contents, keys or anyone's
// network address; the node does not send them.

import { useEffect, useRef, useState } from "preact/hooks";
import { getToken } from "../api";
import { apiUrl } from "../node";
import { errText } from "./common";
import "./admin.css";

export interface AdminStats {
  version: string;
  platform: string;
  uptime_s: number;
  db_bytes: number;
  counts: { users: number; devices: number; domains: number };
  online_users: number;
  traffic: {
    requests: number;
    requests_per_min: number;
    errors: number;
    rejected: number;
    bytes_in: number;
    bytes_out: number;
    messages: number;
    streams: number;
    devices: number;
  };
  domains: { id: string; name: string; members: number; online: number; channels: number; messages: number }[];
  recent_errors: { time: number; route: string; status: number }[];
  update: { current: string; latest: string; url: string; available: boolean } | null;
  /** Absent when calls are off (-rtc=false) or on older nodes. */
  calls?: {
    rooms: number;
    participants: number;
    bytes_in: number;
    bytes_out: number;
    in_bps: number;
    out_bps: number;
    video: boolean;
    udp_port?: number;
    tcp_port?: number;
  };
}

function fmtRate(bps: number): string {
  if (bps < 1000) return `${bps} bit/s`;
  if (bps < 1_000_000) return `${(bps / 1000).toFixed(0)} kbit/s`;
  return `${(bps / 1_000_000).toFixed(1)} Mbit/s`;
}

const POLL_MS = 2000;

async function fetchStats(): Promise<AdminStats> {
  const res = await fetch(apiUrl("/admin/stats"), {
    headers: { Authorization: "Bearer " + (getToken() ?? "") },
    cache: "no-store",
  });
  if (!res.ok) {
    let msg = res.statusText;
    try {
      msg = (await res.json()).error ?? msg;
    } catch {
      /* not JSON */
    }
    throw new Error(msg.charAt(0).toUpperCase() + msg.slice(1) + ".");
  }
  return (await res.json()) as AdminStats;
}

export function fmtBytes(n: number): string {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${i === 0 ? v : v.toFixed(1)} ${units[i]}`;
}

export function fmtUptime(s: number): string {
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (d > 0) return `${d}d ${h}h ${m}m`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m ${s % 60}s`;
}

function safeLink(url: string): string | null {
  return url.startsWith("https://github.com/") ? url : null;
}

export function NodeAdmin({ onClose }: { onClose: () => void }) {
  const [stats, setStats] = useState<AdminStats | null>(null);
  const [err, setErr] = useState("");
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let stopped = false;
    let timer: number | undefined;
    const tick = async () => {
      if (!document.hidden) {
        try {
          const s = await fetchStats();
          if (stopped) return;
          setStats(s);
          setErr("");
        } catch (x) {
          if (stopped) return;
          setErr(errText(x));
        }
      }
      if (!stopped) timer = window.setTimeout(tick, POLL_MS);
    };
    void tick();
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    ref.current?.querySelector<HTMLElement>("button")?.focus();
    return () => {
      stopped = true;
      window.clearTimeout(timer);
      window.removeEventListener("keydown", onKey);
    };
  }, []);

  const t = stats?.traffic;
  const upd = stats?.update?.available ? stats.update : null;
  const updLink = upd ? safeLink(upd.url) : null;

  return (
    <div class="modal-back" onClick={(e) => e.target === e.currentTarget && onClose()}>
      <div class="modal node-admin" role="dialog" aria-modal="true" aria-label="Node admin" ref={ref}>
        <div class="modal-head">
          <h2>Node admin</h2>
          <button type="button" class="btn-icon" aria-label="Close" onClick={onClose}>
            ×
          </button>
        </div>

        {err && <div class="error">{err}</div>}
        {!stats && !err && <div class="muted">Loading…</div>}

        {upd && (
          <div class="note admin-update">
            A newer node version is available: {upd.latest} (this node runs {upd.current}).{" "}
            {updLink && (
              <a href={updLink} target="_blank" rel="noopener noreferrer" class="accent">
                Download page
              </a>
            )}
          </div>
        )}

        {stats && t && (
          <>
            <section>
              <h3>Traffic</h3>
              <dl class="stat-grid admin-grid" aria-live="off">
                <Stat label="Devices online" value={t.devices} />
                <Stat label="Live streams" value={t.streams} />
                <Stat label="Users online" value={stats.online_users} />
                <Stat label="Requests / min" value={t.requests_per_min} />
                <Stat label="Messages relayed" value={t.messages} />
                <Stat label="Requests" value={t.requests} />
                <Stat label="Data in" value={fmtBytes(t.bytes_in)} />
                <Stat label="Data out" value={fmtBytes(t.bytes_out)} />
                <Stat label="Errors" value={t.errors} warn={t.errors > 0} />
              </dl>
              <p class="small muted">
                Rejected requests (4xx): {t.rejected}. Counts start when the node starts. Updated every 2 seconds.
              </p>
            </section>

            {stats.calls && (
              <section>
                <h3>Calls</h3>
                <dl class="stat-grid admin-grid" aria-live="off">
                  <Stat label="Calls now" value={stats.calls.rooms} />
                  <Stat label="People in calls" value={stats.calls.participants} />
                  <Stat label="Media in" value={fmtRate(stats.calls.in_bps)} />
                  <Stat label="Media out" value={fmtRate(stats.calls.out_bps)} />
                  <Stat label="Media data in" value={fmtBytes(stats.calls.bytes_in)} />
                  <Stat label="Media data out" value={fmtBytes(stats.calls.bytes_out)} />
                </dl>
                <p class="small muted">
                  Call media is end-to-end encrypted; the node forwards it without being able to hear or see it.{" "}
                  {stats.calls.udp_port ? `UDP port ${stats.calls.udp_port}` : "UDP ports chosen per call"}
                  {stats.calls.tcp_port ? `, TCP port ${stats.calls.tcp_port}` : ""}. Video{" "}
                  {stats.calls.video ? "on" : "off"}.
                </p>
              </section>
            )}

            <section>
              <h3>Node</h3>
              <dl class="kv">
                <dt>Version</dt>
                <dd>{stats.version}</dd>
                <dt>Platform</dt>
                <dd>{stats.platform}</dd>
                <dt>Uptime</dt>
                <dd>{fmtUptime(stats.uptime_s)}</dd>
                <dt>Database</dt>
                <dd>{fmtBytes(stats.db_bytes)}</dd>
                <dt>Users</dt>
                <dd>{stats.counts.users}</dd>
                <dt>Devices</dt>
                <dd>{stats.counts.devices}</dd>
                <dt>Domains</dt>
                <dd>{stats.counts.domains}</dd>
              </dl>
            </section>

            <section>
              <h3>Domains</h3>
              {stats.domains.length === 0 ? (
                <p class="muted">No domains yet.</p>
              ) : (
                <div class="admin-table-wrap">
                  <table class="admin-table">
                    <thead>
                      <tr>
                        <th scope="col">Domain</th>
                        <th scope="col">Members</th>
                        <th scope="col">Online</th>
                        <th scope="col">Channels</th>
                        <th scope="col">Messages</th>
                      </tr>
                    </thead>
                    <tbody>
                      {stats.domains.map((d) => (
                        <tr key={d.id}>
                          <td>{d.name}</td>
                          <td>{d.members}</td>
                          <td>{d.online}</td>
                          <td>{d.channels}</td>
                          <td>{d.messages}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </section>

            <section>
              <h3>Recent errors</h3>
              {stats.recent_errors.length === 0 ? (
                <p class="muted">No errors since the node started.</p>
              ) : (
                <ul class="admin-errors">
                  {stats.recent_errors.map((e, i) => (
                    <li key={i}>
                      <span class="muted">{new Date(e.time * 1000).toLocaleTimeString()}</span> {e.status} {e.route}
                    </li>
                  ))}
                </ul>
              )}
            </section>
          </>
        )}
      </div>
    </div>
  );
}

function Stat({ label, value, warn }: { label: string; value: string | number; warn?: boolean }) {
  return (
    <div>
      <dt>{label}</dt>
      <dd class={warn ? "error" : undefined}>{value}</dd>
    </div>
  );
}
