import { useEffect, useLayoutEffect, useRef, useState } from "preact/hooks";
import type { JSX } from "preact";
import { Perm, RANK_MEMBER, RANK_OWNER, type Conclave, type DomainDetail, type Member, type Role } from "../api";
import {
  dismissRecoveryHint,
  getEngine,
  openGroup,
  deleteMessage,
  selectDomain,
  sendMessage,
  setEffects,
  setHonorific,
  signOut,
  useApp,
  type AppState,
} from "../app";
import type { ShownMessage } from "../crypto/types";
import { Badge, REDACTED, TAGLINE, errText, fmtTime, initials } from "./common";
import { nodeHost } from "../node";
import { Lock, RoleIcon, Speaker } from "./icons";
import { actorFor, canActOn, has, roleBadge } from "./perms";
import {
  AddDomainModal,
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
  | { kind: "member"; userId: string };

export function Main() {
  const app = useApp();
  const [modal, setModal] = useState<ModalKind | null>(null);
  const [showMembers, setShowMembers] = useState(false);
  const close = () => setModal(null);

  const domain = app.domains.find((d) => d.id === app.selDomain);
  const detail = app.selDomain ? app.details[app.selDomain] : undefined;
  const conclave = app.conclaves.find((c) => c.id === app.selGroup);

  return (
    <div class="main">
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
        <button type="button" class="btn-small" onClick={() => void signOut()}>
          Sign out
        </button>
      </header>

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

      <div class="body">
        <nav class="rail" aria-label="Domains">
          {app.domains.map((d) => (
            <button
              type="button"
              class={d.id === app.selDomain ? "rail-item active" : "rail-item"}
              aria-label={d.name}
              aria-current={d.id === app.selDomain ? "true" : undefined}
              title={d.name}
              onClick={() => void selectDomain(d.id)}
            >
              {initials(d.name)}
            </button>
          ))}
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

        <Sidebar app={app} openModal={setModal} />

        <main class="pane">
          {app.selGroup ? (
            <MessagePane
              key={app.selGroup}
              app={app}
              groupId={app.selGroup}
              detail={conclave ? undefined : detail}
              domainId={conclave ? undefined : domain?.id}
              conclave={conclave}
              onToggleMembers={() => setShowMembers(!showMembers)}
            />
          ) : (
            <div class="empty-pane">
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
            open={showMembers}
            onMember={(userId) => setModal({ kind: "member", userId })}
            onSummons={() => setModal({ kind: "summons" })}
          />
        )}
      </div>

      {modal?.kind === "addDomain" && <AddDomainModal onClose={close} />}
      {modal?.kind === "channel" && domain && <ChannelModal domainId={domain.id} onClose={close} />}
      {modal?.kind === "summons" && domain && <SummonsModal domainId={domain.id} onClose={close} />}
      {modal?.kind === "conclave" && <ConclaveModal onClose={close} />}
      {modal?.kind === "devices" && <DevicesModal onClose={close} />}
      {modal?.kind === "member" && domain && (
        <MemberModal domainId={domain.id} userId={modal.userId} onClose={close} />
      )}
    </div>
  );
}

function conclaveLabel(app: AppState, c: Conclave): string {
  const others = c.members.filter((m) => m.user_id !== app.me?.user_id).map((m) => m.callsign);
  if (others.length === 0) return "Only you";
  if (c.members.length === 2) return others[0];
  return `${others.slice(0, 3).join(", ")}${others.length > 3 ? "…" : ""} (${c.members.length})`;
}

function Sidebar({ app, openModal }: { app: AppState; openModal: (m: ModalKind) => void }) {
  const domain = app.domains.find((d) => d.id === app.selDomain);
  const detail = app.selDomain ? app.details[app.selDomain] : undefined;
  const me = domain && app.me ? actorFor(app, domain.id, app.me.user_id) : undefined;
  const owner = detail?.members.find((m) => m.user_id === domain?.owner_id);
  const ownerRole = detail?.roles.find((r) => r.rank === RANK_OWNER);
  const myMember = detail?.members.find((m) => m.user_id === app.me?.user_id);
  const myRole = detail?.roles.find((r) => r.id === myMember?.role_id);
  const [voiceNote, setVoiceNote] = useState(false);
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
                onClick={() => void openGroup(c.id)}
              >
                #{c.name}
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
              <button type="button" class="chan voice" onClick={() => setVoiceNote(!voiceNote)}>
                <Speaker />
                {c.name}
              </button>
            ))}
          {voiceNote && <div class="muted small">Voice is not available in this client yet.</div>}
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
          <button type="button" class={c.id === app.selGroup ? "chan active" : "chan"} onClick={() => void openGroup(c.id)}>
            {conclaveLabel(app, c)}
          </button>
        ))}
        <div class="section-head sub">
          <span>CONCLAVES</span>
        </div>
        {conclaves.map((c) => (
          <button type="button" class={c.id === app.selGroup ? "chan active" : "chan"} onClick={() => void openGroup(c.id)}>
            {conclaveLabel(app, c)}
          </button>
        ))}
      </div>

      <div class="grow" />
      <div class="me-box">
        <div class="me-icon">{myRole ? <RoleIcon rank={myRole.rank} /> : <Lock />}</div>
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
      <button type="button" class="btn-small" onClick={() => openModal({ kind: "devices" })}>
        Devices
      </button>
    </aside>
  );
}

function senderName(
  app: AppState,
  userId: string,
  detail?: DomainDetail,
  conclave?: Conclave,
): { name: string; role?: Role } {
  const m = detail?.members.find((x) => x.user_id === userId);
  if (m) return { name: m.callsign, role: detail?.roles.find((r) => r.id === m.role_id) };
  const c = conclave?.members.find((x) => x.user_id === userId);
  if (c) return { name: c.callsign };
  for (const d of Object.values(app.details)) {
    const x = d.members.find((mm) => mm.user_id === userId);
    if (x) return { name: x.callsign };
  }
  return { name: "Former member" };
}

function MessagePane(props: {
  app: AppState;
  groupId: string;
  detail?: DomainDetail;
  domainId?: string;
  conclave?: Conclave;
  onToggleMembers: () => void;
}) {
  const { app, groupId, detail, domainId, conclave } = props;
  const engine = getEngine();
  const messages = engine ? engine.messages(groupId) : [];
  const status = engine ? engine.status(groupId) : "unknown";
  const channel = detail?.channels.find((c) => c.id === groupId);
  const [text, setText] = useState("");
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
    if (!body || busy) return;
    setBusy(true);
    setErr("");
    try {
      await sendMessage(groupId, body);
      setText("");
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
        <div class="pane-title">{title}</div>
        <div class="pane-sub">
          {kind} • {count} {count === 1 ? "member" : "members"}
        </div>
        <div class="grow" />
        {detail && (
          <button type="button" class="btn-small members-toggle" onClick={props.onToggleMembers}>
            Members
          </button>
        )}
      </div>

      <div class="messages" ref={listRef} aria-live="polite" data-status={status}>
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
              <div class={accent ? "avatar accent" : "avatar"}>{who.name.slice(0, 1).toUpperCase()}</div>
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
                  <div class="msg-text">{m.text}</div>
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
      <form class="compose" onSubmit={onSend}>
        <label for="compose" class="prompt">
          &gt;_
        </label>
        <input
          id="compose"
          value={text}
          placeholder={title ? `Message ${title}` : "Message"}
          disabled={!!blocked}
          maxLength={4000}
          autocomplete="off"
          onInput={(e) => setText((e.target as HTMLInputElement).value)}
        />
        <span class="cursor" aria-hidden="true" />
        <button type="submit" class="btn-primary" disabled={!!blocked || busy || !text.trim()}>
          SEND
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
  onMember: (userId: string) => void;
  onSummons: () => void;
}) {
  const { app, detail } = props;
  const me = app.me ? actorFor(app, props.domainId, app.me.user_id) : undefined;
  const groups = detail.roles
    .map((r) => ({ role: r, members: detail.members.filter((m) => m.role_id === r.id) }))
    .filter((g) => g.members.length > 0);

  return (
    <aside class={props.open ? "members open" : "members"} aria-label="Members">
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
                <RoleIcon rank={g.role.rank} />
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
    </aside>
  );
}
