// Profile pictures and attachments in the message list.

import type { ComponentChildren } from "preact";
import { useEffect, useState } from "preact/hooks";
import { avatarLoaded, avatarUrl, fmtSize, isInlineImage, openFile, saveFile, type FileRef } from "../files";
import { FileIcon } from "./icons";
import { errText } from "./common";

/** A user's profile picture, or the fallback (the existing letter tile or role icon). */
export function Avatar(props: { uid: string; version?: number; class: string; children: ComponentChildren }) {
  const k = `${props.uid}:${props.version}`;
  const [url, setUrl] = useState<string | null>(props.version ? (avatarLoaded.get(k) ?? null) : null);
  useEffect(() => {
    if (!props.version) {
      setUrl(null);
      return;
    }
    let live = true;
    setUrl(avatarLoaded.get(k) ?? null);
    void avatarUrl(props.uid, props.version).then((u) => live && setUrl(u));
    return () => {
      live = false;
    };
  }, [k]);
  if (url) {
    return (
      <div class={props.class + " has-pic"}>
        <img src={url} alt="" draggable={false} />
      </div>
    );
  }
  return <div class={props.class}>{props.children}</div>;
}

export function Attachments({ files }: { files: FileRef[] }) {
  const [big, setBig] = useState<{ f: FileRef; url: string } | null>(null);
  return (
    <div class="attachments">
      {files.map((f) =>
        isInlineImage(f) ? <InlineImage f={f} onOpen={(url) => setBig({ f, url })} /> : <FileChip f={f} />,
      )}
      {big && <Lightbox f={big.f} url={big.url} onClose={() => setBig(null)} />}
    </div>
  );
}

function InlineImage({ f, onOpen }: { f: FileRef; onOpen: (url: string) => void }) {
  const [state, setState] = useState<{ url?: string; inline?: boolean; err?: string }>({});
  useEffect(() => {
    let live = true;
    openFile(f).then(
      (d) => live && setState({ url: d.url, inline: d.inline }),
      (e) => live && setState({ err: errText(e) }),
    );
    return () => {
      live = false;
    };
  }, [f.id]);
  // Not really an image after all: offer it as a download.
  if (state.url && !state.inline) return <FileChip f={f} />;
  if (state.err) return <FileChip f={f} err={state.err} />;
  const ratio = f.w && f.h ? `${f.w} / ${f.h}` : undefined;
  return (
    <button
      type="button"
      class="att-image"
      aria-label={`Open image ${f.name}`}
      style={ratio ? { aspectRatio: ratio, width: `min(${f.w}px, 100%)` } : undefined}
      onClick={() => state.url && onOpen(state.url)}
    >
      {state.url ? <img src={state.url} alt={f.name} draggable={false} /> : <span class="muted small">Decrypting…</span>}
    </button>
  );
}

function FileChip({ f, err }: { f: FileRef; err?: string }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(err ?? "");
  return (
    <div class="att-file">
      <FileIcon />
      <div class="att-file-text">
        <div class="att-name">{f.name}</div>
        <div class="muted small">
          {fmtSize(f.size)}
          {error && <span class="error"> • {error}</span>}
        </div>
      </div>
      <button
        type="button"
        class="btn-small"
        disabled={busy}
        onClick={async () => {
          setBusy(true);
          setError("");
          try {
            await saveFile(f);
          } catch (e) {
            setError(errText(e));
          } finally {
            setBusy(false);
          }
        }}
      >
        {busy ? "Decrypting…" : "Download"}
      </button>
    </div>
  );
}

function Lightbox({ f, url, onClose }: { f: FileRef; url: string; onClose: () => void }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
  return (
    <div class="lightbox" role="dialog" aria-modal="true" aria-label={f.name} onClick={(e) => e.target === e.currentTarget && onClose()}>
      <div class="lightbox-bar">
        <span class="att-name">{f.name}</span>
        <div class="grow" />
        <button type="button" class="btn-small" onClick={() => void saveFile(f)}>
          Download
        </button>
        <button type="button" class="btn-icon" aria-label="Close" onClick={onClose}>
          ×
        </button>
      </div>
      <img src={url} alt={f.name} onClick={onClose} />
    </div>
  );
}
