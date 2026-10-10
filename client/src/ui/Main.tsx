import { useEffect, useLayoutEffect, useRef, useState } from "preact/hooks";
import type { JSX } from "preact";
import { Perm, RANK_MEMBER, RANK_OWNER, type Conclave, type DomainDetail, type Member, type Role } from "../api";
import {
  dismissNotifyAsk,
  dismissRecoveryHint,
  enableNotifications,
  getEngine,
  openGroup,
  deleteMessage,
  selectDomain,
  sendMessage,
  setEffects,
  setHonorific,
  signOut,
  toggleMuteChat,
  useApp,
  type AppState,
} from "../app";
import type { ShownMessage } from "../crypto/types";
import { Badge, REDACTED, TAGLINE, errText, fmtTime, initials } from "./common";
import { BUNDLED, nodeHost } from "../node";
import { OLD_NODE_NOTE, compatMessage, compatStatus, has as nodeHas } from "../compat";
import { Lock, MenuIcon, Paperclip, RoleIcon, Speaker } from "./icons";
import { fmtSize } from "../files";
import { Attachments, Avatar } from "./Media";
import { SettingsModal } from "./Settings";
import { NodeAdmin } from "./NodeAdmin";
import { actorFor, canActOn, has, roleBadge } from "./perms";
import { CallBar, CallButtons, ConclaveCall, IncomingCall, RelayRoster, VoicePane } from "./Call";
import {
  AddDomainModal,
  BansModal,
  ChannelModal,
  ConclaveModal,
  DevicesModal,
  MemberModal,
  SummonsModal,
} from "./Modals";

type ModalKind =
  | { kind: "addDomain" }
  | { kind: "channel" }
  | { kind: "summons" }
  | { kind: "conclave" }
  | { kind: "devices" }
  | { kind: "settings" }
  | { kind: "bans" }
  | { kind: "member"; userId: string };

type Drawer = "left" | "right" | null;

/** Width of the screen edge where a swipe opens a drawer. */
const EDGE = 32;
const SWIPE = 60;

export function Main() {
  const app = useApp();
  const [modal, setModal] = useState<ModalKind | null>(null);
  const [drawer, setDrawer] = useState<Drawer>(null);
  const [nodeAdmin, setNodeAdmin] = useState(false);
  const close = () => setModal(null);
  const touch = useRef<{ x: number; y: number } | null>(null);
  const phone = useMedia("(max-width: 720px)");

  const domain = app.domains.find((d) => d.id === app.selDomain);
  const detail = app.selDomain ? app.details[app.selDomain] : undefined;
  const conclave = app.conclaves.find((c) => c.id === app.selGroup);
  const hasMembers = !!(domain && detail && !conclave);
  const unreadElsewhere = Object.entries(app.unread).some(([g, n]) => n > 0 && g !== app.selGroup);

  // Phones: swipe from the left edge for the domain and Chapel list, from
  // the right edge for members; swipe the other way (or tap outside) to close.
  // Listeners are added directly: Preact only maps onTouch* props when the
  // browser reports touch support at load.
  const mainRef = useRef<HTMLDivElement>(null);
  const latest = useRef({ drawer, hasMembers });
  latest.current = { drawer, hasMembers };
  useEffect(() => {
    const el = mainRef.current;
    if (!el) return;
    const onStart = (e: TouchEvent) => {
      const t = e.touches[0];
      touch.current = e.touches.length === 1 && t ? { x: t.clientX, y: t.clientY } : null;
    };
    const onEnd = (e: TouchEvent) => {
      const start = touch.current;
      const t = e.changedTouches[0];
      touch.current = null;
      if (!start || !t || !window.matchMedia("(max-width: 720px)").matches) return;
      const dx = t.clientX - start.x;
      const dy = t.clientY - start.y;
      if (Math.abs(dx) < SWIPE || Math.abs(dy) > Math.abs(dx) * 0.7) return;
      const { drawer: d, hasMembers: hm } = latest.current;
      if (dx > 0) {
        if (d === "right") setDrawer(null);
        else if (!d && start.x <= EDGE) setDrawer("left");
      } else {
        if (d === "left") setDrawer(null);
        else if (!d && start.x >= window.innerWidth - EDGE && hm) setDrawer("right");
      }
    };
    el.addEventListener("touchstart", onStart, { passive: true });
    el.addEventListener("touchend", onEnd, { passive: true });
    return () => {
      el.removeEventListener("touchstart", onStart);
      el.removeEventListener("touchend", onEnd);
    };
  }, []);

  return (
    <div class="main" ref={mainRef}>
      <header class="topbar">
        <Badge />
        <div class="brand">
          <div class="brand-name">THE STRANGE DOMAIN</div>
          <div class="brand-tag">{TAGLINE}</div>
        </div>
        <div class="grow" />
        <div class="e2e-chip" title="Messages are encrypted on your devices with MLS. The node cannot read them.">
          <Lock size={16} />
          END-TO-END ENCRYPTED
        </div>
        <div class={app.online ? "node-status" : "node-status offline"} title={`Node: ${nodeHost()}`}>
          NODE {app.online ? "ONLINE" : "OFFLINE"}
        </div>
        <button type="button" class="btn-small" aria-pressed={!app.effects} onClick={() => setEffects(!app.effects)}>
          {app.effects ? "Reduce effects" : "Effects off"}
        </button>
        {app.me?.is_node_admin && (
          <button type="button" class="btn-small" onClick={() => setNodeAdmin(true)}>
            Node admin
          </button>
        )}
        <button type="button" class="btn-small" onClick={() => void signOut()}>
          Sign out
        </button>
      </header>
      {nodeAdmin && <NodeAdmin onClose={() => setNodeAdmin(false)} />}

      {BUNDLED && compatStatus(app.info) !== "ok" && (
        <div class="banner" role="status">
          <span>{compatMessage(compatStatus(app.info))}</span>
        </div>
      )}

      <IncomingCall app={app} />
      <CallBar app={app} />

      {app.askNotify && (
        <div class="banner">
          <span>Show a notification when a message arrives? Notifications only say which chat has a new message, not what it says.</span>
          <button type="button" class="btn-small" onClick={() => void enableNotifications()}>
            Turn on
          </button>
          <button type="button" class="btn-small" onClick={dismissNotifyAsk}>
            Not now
          </button>
        </div>
      )}

      {app.showRecoveryHint && (
        <div class="banner">
          <span>No recovery code is set for this account on this device. If you lose every device, a recovery code is the only way back in.</span>
          <button type="button" class="btn-small" onClick={() => setModal({ kind: "devices" })}>
            Set recovery code
          </button>
          <button type="button" class="btn-small" onClick={dismissRecoveryHint}>
            Later
          </button>
        </div>
      )}

      <div class={drawer ? `body drawer-${drawer}` : "body"}>
        <div class={drawer === "left" ? "nav-drawer open" : "nav-drawer"} inert={phone && drawer !== "left"}>
        <div class="drawer-brand">
          <Badge />
          <div class="brand">
            <div class="brand-name">THE STRANGE DOMAIN</div>
            <div class={app.online ? "node-status" : "node-status offline"}>NODE {app.online ? "ONLINE" : "OFFLINE"}</div>
          </div>
        </div>
        <div class="nav-cols">
        <nav class="rail" aria-label="Domains">
          {app.domains.map((d) => {
            const n = domainUnread(app, d.id);
            return (
              <button
                type="button"
                class={d.id === app.selDomain ? "rail-item active" : "rail-item"}
                aria-label={n ? `${d.name}, ${n} unread` : d.name}
                aria-current={d.id === app.selDomain ? "true" : undefined}
                title={d.name}
                onClick={() => void selectDomain(d.id)}
              >
                {initials(d.name)}
                {n > 0 && <span class="unread rail-unread">{n > 99 ? "99+" : n}</span>}
              </button>
            );
          })}
          <div class="rail-sep" />
          <button
            type="button"
            class="rail-add"
            aria-label="Create a domain or accept a Summons"
            title="Create a domain or accept a Summons"
            onClick={() => setModal({ kind: "addDomain" })}
          >
            +
          </button>
        </nav>

        <Sidebar app={app} openModal={setModal} onNavigate={() => setDrawer(null)} />
        </div>
        </div>

        <main class="pane">
          {app.selGroup && !conclave && detail?.channels.some((c) => c.id === app.selGroup && c.kind === "voice") ? (
            <VoicePane
              key={app.selGroup}
              app={app}
              groupId={app.selGroup}
              detail={detail}
              onMenu={() => setDrawer("left")}
              onToggleMembers={() => setDrawer(drawer === "right" ? null : "right")}
            />
          ) : app.selGroup ? (
            <MessagePane
              key={app.selGroup}
              app={app}
              groupId={app.selGroup}
              detail={conclave ? undefined : detail}
              domainId={conclave ? undefined : domain?.id}
              conclave={conclave}
              unreadElsewhere={unreadElsewhere}
              onMenu={() => setDrawer("left")}
              onToggleMembers={() => setDrawer(drawer === "right" ? null : "right")}
            />
          ) : (
            <div class="empty-pane">
              <button type="button" class="btn-icon menu-btn" aria-label="Open the domain and Chapel list" onClick={() => setDrawer("left")}>
                <MenuIcon />
              </button>
              {app.domains.length === 0 ? (
                <>
                  <p>You are not in a domain yet.</p>
                  <button type="button" class="btn-primary" onClick={() => setModal({ kind: "addDomain" })}>
                    Create a domain or accept a Summons
                  </button>
                </>
              ) : (
                <p>Choose a Chapel.</p>
              )}
            </div>
          )}
        </main>

        {domain && detail && !conclave && (
          <MemberList
            app={app}
            domainId={domain.id}
            detail={detail}
            open={drawer === "right"}
            inert={phone && drawer !== "right"}
            onMember={(userId) => setModal({ kind: "member", userId })}
            onSummons={() => setModal({ kind: "summons" })}
            onBans={() => setModal({ kind: "bans" })}
          />
        )}
        {drawer && <div class="scrim" aria-hidden="true" onClick={() => setDrawer(null)} />}
      </div>

      {modal?.kind === "addDomain" && <AddDomainModal onClose={close} />}
      {modal?.kind === "channel" && domain && <ChannelModal domainId={domain.id} onClose={close} />}
      {modal?.kind === "summons" && domain && <SummonsModal domainId={domain.id} onClose={close} />}
      {modal?.kind === "bans" && domain && <BansModal domainId={domain.id} onClose={close} />}
      {modal?.kind === "conclave" && <ConclaveModal onClose={close} />}
      {modal?.kind === "devices" && <DevicesModal onClose={close} />}
      {modal?.kind === "settings" && <SettingsModal onClose={close} onNodeAdmin={() => { close(); setNodeAdmin(true); }} />}
      {modal?.kind === "member" && domain && (
        <MemberModal domainId={domain.id} userId={modal.userId} onClose={close} />
      )}
    </div>
  );
}

function useMedia(query: string): boolean {
  const [on, setOn] = useState(() => typeof matchMedia !== "undefined" && matchMedia(query).matches);
  useEffect(() => {
    const mq = matchMedia(query);
    const fn = () => setOn(mq.matches);
    mq.addEventListener("change", fn);
    return () => mq.removeEventListener("change", fn);
  }, [query]);
  return on;
}

function domainUnread(app: AppState, domainId: string): number {
  const d = app.details[domainId];
  if (!d) return 0;
  return d.channels.reduce((n, c) => n + (app.unread[c.id] ?? 0), 0);
}

function UnreadCount({ app, gid }: { app: AppState; gid: string }) {
  const n = app.unread[gid] ?? 0;
  if (!n) return null;
  return (
    <span class="unread" aria-label={`${n} unread`}>
      {n > 99 ? "99+" : n}
    </span>
  );
}

function conclaveLabel(app: AppState, c: Conclave): string {
  const others = c.members.filter((m) => m.user_id !== app.me?.user_id).map((m) => m.callsign);
  if (others.length === 0) return "Only you";
  if (c.members.length === 2) return others[0];
  return `${others.slice(0, 3).join(", ")}${others.length > 3 ? "…" : ""} (${c.members.length})`;
}

function Sidebar({ app, openModal, onNavigate }: { app: AppState; openModal: (m: ModalKind) => void; onNavigate: () => void }) {
  const go = (gid: string) => {
    onNavigate();
    void openGroup(gid);
  };
  const domain = app.domains.find((d) => d.id === app.selDomain);
  const detail = app.selDomain ? app.details[app.selDomain] : undefined;
  const me = domain && app.me ? actorFor(app, domain.id, app.me.user_id) : undefined;
  const owner = detail?.members.find((m) => m.user_id === domain?.owner_id);
  const ownerRole = detail?.roles.find((r) => r.rank === RANK_OWNER);
  const myMember = detail?.members.find((m) => m.user_id === app.me?.user_id);
  const myRole = detail?.roles.find((r) => r.id === myMember?.role_id);
  const confessions = app.conclaves.filter((c) => c.members.length === 2);
  const conclaves = app.conclaves.filter((c) => c.members.length !== 2);

  return (
    <aside class="sidebar" aria-label="Chapels">
      {domain && detail ? (
        <div class="sidebar-head">
          <div class="domain-name">{domain.name.toUpperCase()}</div>
          {owner && (
            <div class="domain-sub">
              {(ownerRole?.name ?? "Owner").toUpperCase()}: {owner.callsign.toUpperCase()}
            </div>
          )}
        </div>
      ) : (
        <div class="sidebar-head">
          <div class="domain-name">NO DOMAIN</div>
        </div>
      )}

      {detail && (
        <div class="section">
          <div class="section-head">
            <span>CHAPELS</span>
            {me && has(me, Perm.ManageChannels) && (
              <button type="button" class="btn-tiny" aria-label="Create a Chapel or Voice Relay" onClick={() => openModal({ kind: "channel" })}>
                +
              </button>
            )}
          </div>
          {detail.channels
            .filter((c) => c.kind === "text")
            .map((c) => (
              <button
                type="button"
                class={c.id === app.selGroup ? "chan active" : "chan"}
                aria-current={c.id === app.selGroup ? "true" : undefined}
                onClick={() => go(c.id)}
              >
                <span class="chan-name">#{c.name}</span>
                <UnreadCount app={app} gid={c.id} />
              </button>
            ))}
        </div>
      )}

      {detail && detail.channels.some((c) => c.kind === "voice") && (
        <div class="section">
          <div class="section-head">
            <span>VOICE RELAYS</span>
          </div>
          {detail.channels
            .filter((c) => c.kind === "voice")
            .map((c) => (
              <>
                <button
                  type="button"
                  class={c.id === app.selGroup ? "chan voice active" : "chan voice"}
                  aria-current={c.id === app.selGroup ? "true" : undefined}
                  onClick={() => go(c.id)}
                >
                  <Speaker />
                  <span class="chan-name">{c.name}</span>
                  {app.call?.gid === c.id && <span class="tag">IN CALL</span>}
                </button>
                <RelayRoster app={app} gid={c.id} />
              </>
            ))}
        </div>
      )}

      <div class="section">
        <div class="section-head">
          <span>CONFESSIONS</span>
          <button type="button" class="btn-tiny" aria-label="Start a Confession or Conclave" onClick={() => openModal({ kind: "conclave" })}>
            +
          </button>
        </div>
        {confessions.map((c) => (
          <button type="button" class={c.id === app.selGroup ? "chan active" : "chan"} onClick={() => go(c.id)}>
            <span class="chan-name">{conclaveLabel(app, c)}</span>
            <UnreadCount app={app} gid={c.id} />
          </button>
        ))}
        <div class="section-head sub">
          <span>CONCLAVES</span>
        </div>
        {conclaves.map((c) => (
          <button type="button" class={c.id === app.selGroup ? "chan active" : "chan"} onClick={() => go(c.id)}>
            <span class="chan-name">{conclaveLabel(app, c)}</span>
            <UnreadCount app={app} gid={c.id} />
          </button>
        ))}
      </div>

      <div class="grow" />
      <div class="me-box">
        <button type="button" class="me-pic" aria-label="Settings and profile picture" onClick={() => openModal({ kind: "settings" })}>
          <Avatar uid={app.me?.user_id ?? ""} version={app.me?.avatar} class="me-icon">
            {myRole ? <RoleIcon rank={myRole.rank} /> : <Lock />}
          </Avatar>
        </button>
        <div class="me-text">
          <div>{app.me?.callsign.toUpperCase()}</div>
          {myRole && <div class="me-role">{roleBadge(app, myRole, app.me!.user_id)}</div>}
          <label class="honorific">
            <span class="sr-only">Brother or Sister</span>
            <select
              value={app.honorific ?? ""}
              onChange={(e) => setHonorific((e.target as HTMLSelectElement).value as "Brother" | "Sister")}
            >
              <option value="" disabled>
                Brother / Sister
              </option>
              <option value="Brother">Brother</option>
              <option value="Sister">Sister</option>
            </select>
          </label>
        </div>
      </div>
      <div class="sidebar-buttons">
        <button type="button" class="btn-small" onClick={() => openModal({ kind: "devices" })}>
          Devices
        </button>
        <button type="button" class="btn-small" onClick={() => openModal({ kind: "settings" })}>
          Settings
        </button>
      </div>
    </aside>
  );
}

function senderName(
  app: AppState,
  userId: string,
  detail?: DomainDetail,
  conclave?: Conclave,
): { name: string; role?: Role; avatar?: number } {
  const m = detail?.members.find((x) => x.user_id === userId);
  if (m) return { name: m.callsign, role: detail?.roles.find((r) => r.id === m.role_id), avatar: m.avatar };
  const c = conclave?.members.find((x) => x.user_id === userId);
  if (c) return { name: c.callsign, avatar: c.avatar };
  for (const d of Object.values(app.details)) {
    const x = d.members.find((mm) => mm.user_id === userId);
    if (x) return { name: x.callsign, avatar: x.avatar };
  }
  return { name: "Former member" };
}

function MessagePane(props: {
  app: AppState;
  groupId: string;
  detail?: DomainDetail;
  domainId?: string;
  conclave?: Conclave;
  unreadElsewhere: boolean;
  onMenu: () => void;
  onToggleMembers: () => void;
}) {
  const { app, groupId, detail, domainId, conclave } = props;
  const engine = getEngine();
  const messages = engine ? engine.messages(groupId) : [];
  const status = engine ? engine.status(groupId) : "unknown";
  const channel = detail?.channels.find((c) => c.id === groupId);
  const [text, setText] = useState("");
  const [files, setFiles] = useState<File[]>([]);
  const [dragging, setDragging] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const listRef = useRef<HTMLDivElement>(null);
  const meActor = domainId && app.me ? actorFor(app, domainId, app.me.user_id) : undefined;
  const myMember = detail?.members.find((m) => m.user_id === app.me?.user_id);

  useLayoutEffect(() => {
    const el = listRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [messages.length, groupId]);

  useEffect(() => setErr(""), [groupId]);

  // Off when the node is older than this client (phone app only).
  const attachOk = nodeHas(app.info, "attachments");

  const addFiles = (list: FileList | File[] | null | undefined) => {
    if (!attachOk) return;
    const add = Array.from(list ?? []).filter((f) => f.size > 0 || f.name);
    if (add.length === 0) return;
    setErr("");
    setFiles((cur) => [...cur, ...add].slice(0, app.limits?.max_files ?? 10));
  };

  let blocked = "";
  if (status === "waiting") {
    blocked =
      "This device has not been added to this group's encryption yet. Another member's device adds it automatically when it is online. Sending is disabled until then.";
  } else if (myMember?.muted) {
    blocked = "You are muted in this domain.";
  } else if (meActor && !has(meActor, Perm.SendMessages)) {
    blocked = "Your role cannot send messages here.";
  }

  const title = channel ? `#${channel.name}` : conclave ? conclaveLabel(app, conclave) : "";
  const kind = channel ? "Chapel" : conclave ? (conclave.members.length === 2 ? "Confession" : "Conclave") : "";
  const count = channel ? (detail?.members.length ?? 0) : (conclave?.members.length ?? 0);

  const onSend = async (e: JSX.TargetedEvent<HTMLFormElement>) => {
    e.preventDefault();
    const body = text.trim();
    if ((!body && files.length === 0) || busy) return;
    setBusy(true);
    setErr("");
    try {
      await sendMessage(groupId, body, files);
      setText("");
      setFiles([]);
    } catch (x) {
      setErr(errText(x));
    } finally {
      setBusy(false);
    }
  };

  const canDelete = (m: ShownMessage) => {
    if (m.state === "redacted") return false;
    if (m.senderUser === app.me?.user_id) return true;
    if (!domainId || !meActor) return false;
    const target = actorFor(app, domainId, m.senderUser);
    if (!target) return has(meActor, Perm.DeleteMessages);
    return canActOn(meActor, Perm.DeleteMessages, target);
  };

  return (
    <>
      <div class="pane-head">
        <button type="button" class="btn-icon menu-btn" aria-label="Open the domain and Chapel list" onClick={props.onMenu}>
          <MenuIcon />
          {props.unreadElsewhere && <span class="menu-dot" aria-label="Unread messages in other chats" />}
        </button>
        <div class="pane-titles">
          <div class="pane-title">{title}</div>
          <div class="pane-sub">
            {kind} • {count} {count === 1 ? "member" : "members"}
          </div>
        </div>
        <div class="grow" />
        {conclave && <CallButtons app={app} groupId={groupId} />}
        <button
          type="button"
          class="btn-small"
          aria-pressed={app.mutedChats.includes(groupId)}
          title="Mute stops the sound and notifications for this chat"
          onClick={() => toggleMuteChat(groupId)}
        >
          {app.mutedChats.includes(groupId) ? "Unmute" : "Mute"}
        </button>
        {detail && (
          <button type="button" class="btn-small members-toggle" onClick={props.onToggleMembers}>
            Members
          </button>
        )}
      </div>

      {conclave && <ConclaveCall app={app} groupId={groupId} />}

      <div
        class={dragging ? "messages dragging" : "messages"}
        ref={listRef}
        aria-live="polite"
        data-status={status}
        onDragOver={(e) => {
          if (blocked || !attachOk || !e.dataTransfer?.types.includes("Files")) return;
          e.preventDefault();
          setDragging(true);
        }}
        onDragLeave={() => setDragging(false)}
        onDrop={(e) => {
          setDragging(false);
          if (blocked || !attachOk || !e.dataTransfer?.files.length) return;
          e.preventDefault();
          addFiles(e.dataTransfer.files);
        }}
      >
        <div class="msg-system">— {status === "unknown" ? LOADING_SHORT : "MLS • END-TO-END ENCRYPTED"} —</div>
        {status === "empty" && messages.length === 0 && (
          <div class="msg-system">No messages yet. Encryption for this group is set up when the first message is sent.</div>
        )}
        {messages.map((m) => {
          const who = senderName(app, m.senderUser, detail, conclave);
          const accent = (who.role?.rank ?? 0) >= 500;
          if (m.state === "redacted") {
            return (
              <div class="msg redacted" data-seq={m.seq}>
                <div class="avatar-spacer" />
                <div>{REDACTED}</div>
              </div>
            );
          }
          return (
            <div class="msg" data-seq={m.seq}>
              <Avatar uid={m.senderUser} version={who.avatar} class={accent ? "avatar accent" : "avatar"}>
                {who.name.slice(0, 1).toUpperCase()}
              </Avatar>
              <div class="msg-main">
                <div class="msg-meta">
                  <span class={accent ? "accent" : ""}>{who.name.toUpperCase()}</span>
                  {who.role && (
                    <span class={accent ? "role-badge accent" : "role-badge"}>{roleBadge(app, who.role, m.senderUser)}</span>
                  )}
                  <span class="msg-time">{fmtTime(m.createdAt)}</span>
                  {canDelete(m) && (
                    <button
                      type="button"
                      class="btn-tiny msg-delete"
                      onClick={() => void deleteMessage(groupId, m.seq).catch((x) => setErr(errText(x)))}
                    >
                      Delete
                    </button>
                  )}
                </div>
                {m.state === "ok" ? (
                  <>
                    {m.text && <div class="msg-text">{m.text}</div>}
                    {m.files && m.files.length > 0 && <Attachments files={m.files} />}
                  </>
                ) : (
                  <div class="msg-text muted">Not readable on this device (sent before this device joined the group).</div>
                )}
              </div>
            </div>
          );
        })}
      </div>

      {blocked && <div class="compose-note">{blocked}</div>}
      {err && (
        <div class="compose-note error" role="alert">
          {err}
        </div>
      )}
      {files.length > 0 && (
        <ul class="pending-files" aria-label="Files to send">
          {files.map((f, i) => (
            <li>
              <span class="att-name">{f.name || "Pasted image"}</span>
              <span class="muted small">{fmtSize(f.size)}</span>
              <button
                type="button"
                class="btn-icon"
                aria-label={`Remove ${f.name || "file"}`}
                disabled={busy}
                onClick={() => setFiles(files.filter((_, j) => j !== i))}
              >
                ×
              </button>
            </li>
          ))}
        </ul>
      )}
      <form class="compose" onSubmit={onSend}>
        <label for="compose" class="prompt">
          &gt;_
        </label>
        <button
          type="button"
          class="btn-icon attach-btn"
          aria-label="Attach files"
          title={attachOk ? "Attach files" : OLD_NODE_NOTE}
          disabled={!!blocked || busy || !attachOk}
          onClick={() => fileInput.current?.click()}
        >
          <Paperclip />
        </button>
        <input
          ref={fileInput}
          type="file"
          multiple
          class="sr-only"
          tabIndex={-1}
          aria-hidden="true"
          onChange={(e) => {
            const input = e.target as HTMLInputElement;
            addFiles(input.files);
            input.value = "";
          }}
        />
        <input
          id="compose"
          value={text}
          placeholder={title ? `Message ${title}` : "Message"}
          disabled={!!blocked}
          maxLength={4000}
          autocomplete="off"
          onInput={(e) => setText((e.target as HTMLInputElement).value)}
          onPaste={(e) => {
            const pasted = e.clipboardData?.files;
            if (attachOk && pasted && pasted.length > 0) {
              e.preventDefault();
              addFiles(pasted);
            }
          }}
        />
        <span class="cursor" aria-hidden="true" />
        <button type="submit" class="btn-primary" disabled={!!blocked || busy || (!text.trim() && files.length === 0)}>
          {busy && files.length > 0 ? "SENDING" : "SEND"}
        </button>
      </form>
    </>
  );
}

const LOADING_SHORT = "LOADING";

function MemberList(props: {
  app: AppState;
  domainId: string;
  detail: DomainDetail;
  open: boolean;
  inert: boolean;
  onMember: (userId: string) => void;
  onSummons: () => void;
  onBans: () => void;
}) {
  const { app, detail } = props;
  const me = app.me ? actorFor(app, props.domainId, app.me.user_id) : undefined;
  const groups = detail.roles
    .map((r) => ({ role: r, members: detail.members.filter((m) => m.role_id === r.id) }))
    .filter((g) => g.members.length > 0);

  return (
    <aside class={props.open ? "members open" : "members"} aria-label="Members" inert={props.inert}>
      {groups.map((g) => {
        const accent = g.role.rank >= 500;
        return (
          <div class="member-group">
            <div class={accent ? "member-head accent" : "member-head"}>
              {g.role.rank === RANK_MEMBER ? "PARISHIONERS" : g.role.name.toUpperCase()} — {g.members.length}
            </div>
            {g.members.map((m: Member) => (
              <button
                type="button"
                class={accent ? "member accent" : "member"}
                onClick={() => props.onMember(m.user_id)}
                title={roleBadge(app, g.role, m.user_id)}
              >
                <Avatar uid={m.user_id} version={m.avatar} class="member-pic">
                  <RoleIcon rank={g.role.rank} />
                </Avatar>
                <span>{m.callsign}</span>
                {m.user_id === app.me?.user_id && <span class="muted small">(you)</span>}
                {m.muted && <span class="tag">MUTED</span>}
              </button>
            ))}
          </div>
        );
      })}
      <div class="grow" />
      {me && has(me, Perm.CreateInvite) && (
        <button type="button" class="btn-outline" onClick={props.onSummons}>
          CREATE SUMMONS
        </button>
      )}
      {me && has(me, Perm.Ban) && nodeHas(app.info, "bans") && (
        <button type="button" class="btn-outline" onClick={props.onBans}>
          BANS
        </button>
      )}
    </aside>
  );
}
