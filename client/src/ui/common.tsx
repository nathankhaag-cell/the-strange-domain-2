import type { ComponentChildren } from "preact";
import { useEffect, useRef } from "preact/hooks";
import { ApiError } from "../api";

// Approved wording.
export const TAGLINE = "VIGIL KEPT SINCE REDACTED • SIGNAL RECOVERED FROM THE DEEP";
export const LOADING = "ESTABLISHING SECURE UPLINK...";
export const REDACTED = "[ REDACTED ]";

export function Badge({ size }: { size?: "lg" | "sm" }) {
  return (
    <div class={size === "lg" ? "badge74 badge74-lg" : "badge74"} aria-hidden="true">
      74
    </div>
  );
}

export function errText(e: unknown): string {
  if (e instanceof ApiError) {
    const m = e.message;
    return m.charAt(0).toUpperCase() + m.slice(1) + ".";
  }
  if (e instanceof Error) return e.message;
  return String(e);
}

export function Field(props: {
  id: string;
  label: string;
  value: string;
  onInput: (v: string) => void;
  required?: boolean;
  maxLength?: number;
  placeholder?: string;
  type?: string;
}) {
  return (
    <div class="field">
      <label for={props.id}>{props.label}</label>
      <input
        id={props.id}
        type={props.type ?? "text"}
        value={props.value}
        required={props.required}
        maxLength={props.maxLength}
        placeholder={props.placeholder ?? ">_"}
        autocomplete="off"
        spellcheck={false}
        onInput={(e) => props.onInput((e.target as HTMLInputElement).value)}
      />
    </div>
  );
}

export function Modal(props: { title: string; onClose: () => void; children: ComponentChildren }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && props.onClose();
    window.addEventListener("keydown", onKey);
    ref.current?.querySelector<HTMLElement>("input, select, button")?.focus();
    return () => window.removeEventListener("keydown", onKey);
  }, []);
  return (
    <div class="modal-back" onClick={(e) => e.target === e.currentTarget && props.onClose()}>
      <div class="modal" role="dialog" aria-modal="true" aria-label={props.title} ref={ref}>
        <div class="modal-head">
          <h2>{props.title}</h2>
          <button type="button" class="btn-icon" aria-label="Close" onClick={props.onClose}>
            ×
          </button>
        </div>
        {props.children}
      </div>
    </div>
  );
}

export function Effects() {
  return (
    <>
      <div class="fx-scan" aria-hidden="true" />
      <div class="fx-vignette" aria-hidden="true" />
    </>
  );
}

export function initials(name: string): string {
  const words = name.trim().split(/\s+/).filter(Boolean);
  if (words.length >= 2) return (words[0][0] + words[1][0]).toUpperCase();
  return name.trim().slice(0, 2).toUpperCase();
}

export function fmtTime(unix: number): string {
  const d = new Date(unix * 1000);
  const now = new Date();
  const hm = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  if (d.toDateString() === now.toDateString()) return `Today ${hm}`;
  return `${d.toLocaleDateString()} ${hm}`;
}
