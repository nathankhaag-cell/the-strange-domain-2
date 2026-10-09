// Settings: profile picture, notifications, and (for phones, where the top
// bar is hidden) effects and sign out.

import { useEffect, useRef, useState } from "preact/hooks";
import {
  enableNotifications,
  removeAvatar,
  setAvatar,
  setEffects,
  setNotifyPrefs,
  signOut,
  useApp,
} from "../app";
import { renderAvatar } from "../files";
import { permission, type Permission } from "../notify";
import { Modal, errText } from "./common";
import { Avatar } from "./Media";
import { Lock } from "./icons";

export function SettingsModal({ onClose }: { onClose: () => void }) {
  const app = useApp();
  const prefs = app.notifyPrefs;
  const [perm, setPerm] = useState<Permission>("default");
  const [err, setErr] = useState("");
  useEffect(() => {
    void permission().then(setPerm);
  }, [prefs.notify]);

  const toggleNotify = async (on: boolean) => {
    setErr("");
    if (!on) {
      setNotifyPrefs({ ...prefs, notify: false });
      return;
    }
    const ok = await enableNotifications();
    setPerm(await permission());
    if (!ok) setErr("Notifications are blocked. Allow them for this app in your browser or system settings.");
  };

  return (
    <Modal title="Settings" onClose={onClose}>
      <div class="modal-form">
        <h3>Profile picture</h3>
        <AvatarPicker />
      </div>

      <div class="modal-form">
        <h3>Notifications</h3>
        <label class="check">
          <input
            type="checkbox"
            checked={prefs.sound}
            onChange={(e) => setNotifyPrefs({ ...prefs, sound: (e.target as HTMLInputElement).checked })}
          />
          Play a sound when a message arrives
        </label>
        <label class="check">
          <input
            type="checkbox"
            checked={prefs.notify && perm === "granted"}
            disabled={perm === "unsupported"}
            onChange={(e) => void toggleNotify((e.target as HTMLInputElement).checked)}
          />
          Show a notification when a message arrives
        </label>
        <label class="check">
          <input
            type="checkbox"
            checked={prefs.preview}
            disabled={!prefs.notify}
            onChange={(e) => setNotifyPrefs({ ...prefs, preview: (e.target as HTMLInputElement).checked })}
          />
          Include the sender and message text in notifications
        </label>
        <p class="muted small">
          Without this, a notification only says which chat has a new message. To silence one chat, use Mute at the top of that chat.
        </p>
        {perm === "unsupported" && <p class="muted small">This browser cannot show notifications.</p>}
        {perm === "denied" && !err && (
          <p class="muted small">Notifications are blocked. Allow them for this app in your browser or system settings.</p>
        )}
        {err && <div class="error">{err}</div>}
      </div>

      <div class="modal-form settings-device">
        <h3>This device</h3>
        <div class="row-wrap">
          <button type="button" class="btn-small" aria-pressed={!app.effects} onClick={() => setEffects(!app.effects)}>
            {app.effects ? "Reduce effects" : "Effects off"}
          </button>
          <button type="button" class="btn-small" onClick={() => void signOut()}>
            Sign out
          </button>
        </div>
      </div>
    </Modal>
  );
}

const PREVIEW = 240;

function AvatarPicker() {
  const app = useApp();
  const me = app.me;
  const fileRef = useRef<HTMLInputElement>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const [bmp, setBmp] = useState<ImageBitmap | null>(null);
  const [view, setView] = useState({ zoom: 1, x: 0, y: 0 });
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const drag = useRef<{ px: number; py: number; x: number; y: number } | null>(null);

  // Keeps the crop square inside the picture.
  const clamp = (v: { zoom: number; x: number; y: number }, b: ImageBitmap) => {
    const side = Math.min(b.width, b.height) / v.zoom;
    const mx = (b.width - side) / 2 / side;
    const my = (b.height - side) / 2 / side;
    return { zoom: v.zoom, x: Math.max(-mx, Math.min(mx, v.x)), y: Math.max(-my, Math.min(my, v.y)) };
  };

  useEffect(() => {
    const c = canvasRef.current;
    if (!c || !bmp) return;
    const ctx = c.getContext("2d");
    if (!ctx) return;
    const side = Math.min(bmp.width, bmp.height) / view.zoom;
    const sx = (bmp.width - side) / 2 - view.x * side;
    const sy = (bmp.height - side) / 2 - view.y * side;
    ctx.clearRect(0, 0, PREVIEW, PREVIEW);
    ctx.drawImage(bmp, sx, sy, side, side, 0, 0, PREVIEW, PREVIEW);
  }, [bmp, view]);

  const pick = async (file: File | undefined) => {
    setErr("");
    if (!file) return;
    if (!/^image\/(png|jpeg|gif|webp)$/.test(file.type)) {
      setErr("Choose a PNG, JPEG, GIF or WebP picture.");
      return;
    }
    try {
      const b = await createImageBitmap(file);
      bmp?.close();
      setBmp(b);
      setView({ zoom: 1, x: 0, y: 0 });
    } catch {
      setErr("That picture could not be opened.");
    }
  };

  const save = async () => {
    if (!bmp) return;
    setBusy(true);
    setErr("");
    try {
      await setAvatar(await renderAvatar(bmp, view));
      bmp.close();
      setBmp(null);
    } catch (e) {
      setErr(errText(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div class="avatar-picker">
      {bmp ? (
        <>
          <p class="muted small">Drag to move the picture. Use the slider to zoom.</p>
          <canvas
            ref={canvasRef}
            class="crop"
            width={PREVIEW}
            height={PREVIEW}
            onPointerDown={(e) => {
              (e.target as HTMLElement).setPointerCapture(e.pointerId);
              drag.current = { px: e.clientX, py: e.clientY, x: view.x, y: view.y };
            }}
            onPointerMove={(e) => {
              const d = drag.current;
              if (!d) return;
              const r = (e.target as HTMLElement).getBoundingClientRect();
              setView(clamp({ zoom: view.zoom, x: d.x + (e.clientX - d.px) / r.width, y: d.y + (e.clientY - d.py) / r.height }, bmp));
            }}
            onPointerUp={() => (drag.current = null)}
            onPointerCancel={() => (drag.current = null)}
          />
          <label class="field">
            <span>Zoom</span>
            <input
              type="range"
              min="1"
              max="4"
              step="0.05"
              value={view.zoom}
              onInput={(e) => setView(clamp({ ...view, zoom: Number((e.target as HTMLInputElement).value) }, bmp))}
            />
          </label>
          <div class="row-wrap">
            <button type="button" class="btn-primary" disabled={busy} onClick={() => void save()}>
              Save picture
            </button>
            <button
              type="button"
              class="btn-small"
              disabled={busy}
              onClick={() => {
                bmp.close();
                setBmp(null);
              }}
            >
              Cancel
            </button>
          </div>
        </>
      ) : (
        <div class="row-wrap">
          {me && (
            <Avatar uid={me.user_id} version={me.avatar} class="avatar avatar-lg">
              {me.callsign.slice(0, 1).toUpperCase()}
            </Avatar>
          )}
          <button type="button" class="btn-outline" onClick={() => fileRef.current?.click()}>
            Choose picture
          </button>
          {me?.avatar ? (
            <button
              type="button"
              class="btn-small"
              disabled={busy}
              onClick={async () => {
                setBusy(true);
                try {
                  await removeAvatar();
                } catch (e) {
                  setErr(errText(e));
                } finally {
                  setBusy(false);
                }
              }}
            >
              Remove picture
            </button>
          ) : null}
        </div>
      )}
      <input
        ref={fileRef}
        type="file"
        accept="image/png,image/jpeg,image/gif,image/webp"
        class="sr-only"
        tabIndex={-1}
        onChange={(e) => {
          const input = e.target as HTMLInputElement;
          void pick(input.files?.[0]);
          input.value = "";
        }}
      />
      <p class="muted small key-line">
        <Lock size={14} /> Your profile picture is stored on the node and is not end-to-end encrypted. Messages and attachments are.
      </p>
      {err && <div class="error">{err}</div>}
    </div>
  );
}
