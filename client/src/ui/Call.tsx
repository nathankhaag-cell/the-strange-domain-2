// Voice and video calls: the Voice Relay pane, the call stage and controls
// (also shown above a Confession or Conclave), the incoming call banner and
// the bar that keeps a call in reach while another chat is open.
//
// Wording follows the approved vocabulary (Voice Relay, Confession,
// Conclave) and plain words for everything else: Join, Leave, Mute, Camera,
// Share screen, Call, Answer, Decline, Hang up.

import { useEffect, useMemo, useRef, useState } from "preact/hooks";
import type { CallPart, DomainDetail } from "../api";
import {
  answerCall,
  callsAvailable,
  callsignOf,
  chatLabel,
  declineCall,
  dismissCallNotice,
  getCall,
  joinCall,
  leaveCall,
  openGroup,
  type AppState,
} from "../app";
import { e2eeSupport, NO_E2EE, type CallView } from "../call/session";
import { has as nodeHas } from "../compat";
import { MenuIcon, Speaker } from "./icons";
import { errText } from "./common";

const canShareScreen = typeof navigator !== "undefined" && !!navigator.mediaDevices?.getDisplayMedia;

/** The Voice Relay pane: who is in it, and the call once joined. */
export function VoicePane(props: {
  app: AppState;
  groupId: string;
  detail?: DomainDetail;
  onMenu: () => void;
  onToggleMembers: () => void;
}) {
  const { app, groupId, detail } = props;
  const channel = detail?.channels.find((c) => c.id === groupId);
  const room = app.rooms.find((r) => r.id === groupId);
  const inCall = app.call?.gid === groupId;
  const count = inCall ? app.call!.participants.length : (room?.participants.length ?? 0);
  const [err, setErr] = useState("");
  useEffect(() => setErr(""), [groupId]);
  const notice = app.callNotice?.gid === groupId ? app.callNotice.text : "";
  const unsupported = !e2eeSupport();

  return (
    <>
      <div class="pane-head">
        <button type="button" class="btn-icon menu-btn" aria-label="Open the domain and Chapel list" onClick={props.onMenu}>
          <MenuIcon />
        </button>
        <div class="pane-titles">
          <div class="pane-title">
            <Speaker /> {channel?.name ?? ""}
          </div>
          <div class="pane-sub">
            Voice Relay • {count} in call
          </div>
        </div>
        <div class="grow" />
        {detail && (
          <button type="button" class="btn-small members-toggle" onClick={props.onToggleMembers}>
            Members
          </button>
        )}
      </div>
      <div class="call-pane">
        <div class="msg-system">— END-TO-END ENCRYPTED VOICE —</div>
        {inCall ? (
          <CallStage app={app} view={app.call!} />
        ) : (
          <div class="call-idle">
            {room && room.participants.length > 0 ? (
              <ul class="call-roster" aria-label="In this Voice Relay">
                {room.participants.map((p) => (
                  <li>{callsignOf(p.user_id).toUpperCase()}</li>
                ))}
              </ul>
            ) : (
              <p class="muted">Nobody is in this Voice Relay.</p>
            )}
            {notice && (
              <div class="compose-note" role="status">
                {notice}{" "}
                <button type="button" class="btn-tiny" onClick={dismissCallNotice}>
                  OK
                </button>
              </div>
            )}
            {err && (
              <div class="compose-note error" role="alert">
                {err}
              </div>
            )}
            {!callsAvailable() ? (
              <p class="muted">Calls are turned off on this node.</p>
            ) : unsupported ? (
              <p class="compose-note error">{NO_E2EE}</p>
            ) : (
              <button
                type="button"
                class="btn-primary"
                onClick={() => void joinCall(groupId).catch((x) => setErr(errText(x)))}
              >
                Join
              </button>
            )}
          </div>
        )}
      </div>
      {inCall && <CallControls view={app.call!} />}
    </>
  );
}

/** A call above a Confession or Conclave's messages. */
export function ConclaveCall({ app, groupId }: { app: AppState; groupId: string }) {
  const notice = app.callNotice?.gid === groupId ? app.callNotice.text : "";
  if (app.call?.gid === groupId) {
    return (
      <div class="conclave-call">
        <CallStage app={app} view={app.call} />
        <CallControls view={app.call} />
      </div>
    );
  }
  const room = app.rooms.find((r) => r.id === groupId && r.participants.length > 0);
  if (!notice && !room) return null;
  return (
    <div class="conclave-call idle">
      {room && (
        <div class="call-strip">
          <span>
            In call: {room.participants.map((p) => callsignOf(p.user_id)).join(", ")}
          </span>
          {e2eeSupport() && (
            <button type="button" class="btn-small" onClick={() => void joinCall(groupId)}>
              Join
            </button>
          )}
        </div>
      )}
      {notice && (
        <div class="compose-note" role="status">
          {notice}{" "}
          <button type="button" class="btn-tiny" onClick={dismissCallNotice}>
            OK
          </button>
        </div>
      )}
    </div>
  );
}

/** Call and Video call buttons for a Confession or Conclave header. */
export function CallButtons({ app, groupId }: { app: AppState; groupId: string }) {
  if (!callsAvailable() || app.call?.gid === groupId) return null;
  const supported = !!e2eeSupport();
  const title = supported ? undefined : NO_E2EE;
  return (
    <>
      <button type="button" class="btn-small" disabled={!supported} title={title} onClick={() => void joinCall(groupId, { ring: true })}>
        Call
      </button>
      {nodeHas(app.info, "video") && app.info?.features?.includes("video") && (
        <button
          type="button"
          class="btn-small"
          disabled={!supported}
          title={title}
          onClick={() => void joinCall(groupId, { ring: true, video: true })}
        >
          Video call
        </button>
      )}
    </>
  );
}

/** Everyone in the call: a tile each, with video when they share it. */
function CallStage({ app, view }: { app: AppState; view: CallView }) {
  const parts = view.participants.length
    ? view.participants
    : [{ id: view.selfId ?? "self", user_id: app.me?.user_id ?? "", device_id: "", muted: view.muted, camera: false, screen: false, can_speak: view.canSpeak, joined: 0 }];
  return (
    <div class="call-stage" data-phase={view.phase}>
      <div class="call-tiles">
        {parts.map((p) => (
          <Tile app={app} view={view} part={p} />
        ))}
      </div>
      <div class="call-status muted small" role="status">
        {view.phase === "connecting"
          ? "Connecting…"
          : view.calling
            ? "Calling…"
            : view.declined.length > 0 && !view.relay
              ? `Declined: ${view.declined.map(callsignOf).join(", ")}`
              : ""}
      </div>
    </div>
  );
}

function Tile({ app, view, part }: { app: AppState; view: CallView; part: CallPart }) {
  const self = part.id === view.selfId;
  const media = self ? undefined : view.remote[part.id];
  const screen = self ? (view.screen ? view.localScreen : undefined) : part.screen ? media?.screen : undefined;
  const camera = self ? (view.camera ? view.localCamera : undefined) : part.camera ? media?.camera : undefined;
  const speaking = !!view.speaking[part.id];
  const silent = part.muted || !part.can_speak;
  const name = (part.user_id === app.me?.user_id ? app.me?.callsign : callsignOf(part.user_id)) ?? "";
  return (
    <div class={speaking ? "call-tile speaking" : "call-tile"} data-participant={part.id} data-speaking={speaking ? "1" : "0"}>
      {screen ? (
        <Video source={screen} muted />
      ) : camera ? (
        <Video source={camera} muted mirror={self} />
      ) : (
        <div class="call-tile-initial" aria-hidden="true">
          {name.slice(0, 1).toUpperCase()}
        </div>
      )}
      <div class="call-tile-name">
        <span>{name.toUpperCase()}</span>
        {self && <span class="muted small">(you)</span>}
        {silent && <span class="tag">MUTED</span>}
      </div>
    </div>
  );
}

/** Plays a stream or a single track in a <video>. */
function Video({ source, muted, mirror }: { source: MediaStream | MediaStreamTrack; muted?: boolean; mirror?: boolean }) {
  const ref = useRef<HTMLVideoElement>(null);
  const stream = useMemo(() => (source instanceof MediaStream ? source : new MediaStream([source])), [source]);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.srcObject = stream;
    void el.play().catch(() => undefined);
  }, [stream]);
  return <video ref={ref} class={mirror ? "call-video mirror" : "call-video"} autoplay playsInline muted={muted} />;
}

/** Mute, push to talk, camera, screen sharing, and leaving. */
function CallControls({ view }: { view: CallView }) {
  const [err, setErr] = useState("");
  const s = getCall();
  const run = (fn: () => Promise<void> | void) => {
    setErr("");
    Promise.resolve()
      .then(fn)
      .catch((x) => setErr(errText(x)));
  };

  // Push to talk: hold the space bar (outside text fields) or the button.
  useEffect(() => {
    if (!view.ptt) return;
    const typing = (t: EventTarget | null) =>
      t instanceof HTMLElement && (t.isContentEditable || ["INPUT", "TEXTAREA", "SELECT", "BUTTON"].includes(t.tagName));
    const down = (e: KeyboardEvent) => {
      if (e.code !== "Space" || e.repeat || typing(e.target)) return;
      e.preventDefault();
      getCall()?.talk(true);
    };
    const up = (e: KeyboardEvent) => {
      if (e.code !== "Space") return;
      getCall()?.talk(false);
    };
    const blur = () => getCall()?.talk(false);
    window.addEventListener("keydown", down);
    window.addEventListener("keyup", up);
    window.addEventListener("blur", blur);
    return () => {
      window.removeEventListener("keydown", down);
      window.removeEventListener("keyup", up);
      window.removeEventListener("blur", blur);
    };
  }, [view.ptt]);

  let note = "";
  if (!view.canSpeak) note = "You are muted in this domain. You can listen.";
  else if (!view.micOk && view.phase === "live") note = "No microphone. You can listen.";
  else if (view.ptt) note = "Hold the space bar or the button to talk.";

  const speakOk = view.canSpeak && view.micOk;
  return (
    <div class="call-controls">
      {note && <div class="compose-note">{note}</div>}
      {err && (
        <div class="compose-note error" role="alert">
          {err}
        </div>
      )}
      <div class="call-buttons">
        {view.ptt ? (
          <button
            type="button"
            class={view.talking ? "btn-primary" : "btn-outline"}
            disabled={!speakOk}
            aria-pressed={view.talking}
            onPointerDown={(e) => {
              (e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId);
              s?.talk(true);
            }}
            onPointerUp={() => s?.talk(false)}
            onPointerCancel={() => s?.talk(false)}
          >
            Hold to talk
          </button>
        ) : (
          <button type="button" class="btn-small" disabled={!speakOk} aria-pressed={view.muted} onClick={() => s?.setMuted(!view.muted)}>
            {view.muted ? "Unmute" : "Mute"}
          </button>
        )}
        <button type="button" class="btn-small" disabled={!speakOk} aria-pressed={view.ptt} onClick={() => s?.setPtt(!view.ptt)}>
          Push to talk
        </button>
        {view.videoAllowed && (
          <button type="button" class="btn-small" aria-pressed={view.camera} onClick={() => run(() => s?.setCamera(!view.camera))}>
            Camera
          </button>
        )}
        {view.videoAllowed && canShareScreen && (
          <button type="button" class="btn-small" aria-pressed={view.screen} onClick={() => run(() => s?.setScreen(!view.screen))}>
            Share screen
          </button>
        )}
        <div class="grow" />
        <button type="button" class="btn-outline call-leave" onClick={() => leaveCall()}>
          {view.relay ? "Leave" : "Hang up"}
        </button>
      </div>
    </div>
  );
}

/** "Incoming call" with Answer and Decline. */
export function IncomingCall({ app }: { app: AppState }) {
  const inc = app.incoming;
  if (!inc) return null;
  return (
    <div class="banner incoming-call" role="alert">
      <span>
        {inc.video ? "Incoming video call" : "Incoming call"} from {chatLabel(inc.gid)}
      </span>
      <button type="button" class="btn-small" onClick={() => void answerCall()}>
        Answer
      </button>
      <button type="button" class="btn-small" onClick={() => void declineCall()}>
        Decline
      </button>
    </div>
  );
}

/** Keeps the call in reach while another chat is open. */
export function CallBar({ app }: { app: AppState }) {
  const v = app.call;
  if (!v || app.selGroup === v.gid) return null;
  const s = getCall();
  return (
    <div class="banner call-bar">
      <button type="button" class="btn-link" onClick={() => void openGroup(v.gid)}>
        In call: {chatLabel(v.gid)}
      </button>
      {v.canSpeak && v.micOk && !v.ptt && (
        <button type="button" class="btn-small" aria-pressed={v.muted} onClick={() => s?.setMuted(!v.muted)}>
          {v.muted ? "Unmute" : "Mute"}
        </button>
      )}
      <button type="button" class="btn-small" onClick={() => leaveCall()}>
        {v.relay ? "Leave" : "Hang up"}
      </button>
    </div>
  );
}

/** Who is in a Voice Relay, under its name in the sidebar. */
export function RelayRoster({ app, gid }: { app: AppState; gid: string }) {
  const inCall = app.call?.gid === gid ? app.call : undefined;
  const parts = inCall ? inCall.participants : (app.rooms.find((r) => r.id === gid)?.participants ?? []);
  if (parts.length === 0) return null;
  return (
    <ul class="relay-roster">
      {parts.map((p) => {
        const speaking = !!inCall?.speaking[p.id];
        return (
          <li class={speaking ? "speaking" : undefined}>
            {callsignOf(p.user_id)}
            {(p.muted || !p.can_speak) && <span class="tag">MUTED</span>}
          </li>
        );
      })}
    </ul>
  );
}
