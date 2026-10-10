// A large view of someone's shared screen or camera in a call, with zoom
// and pan: mouse wheel, pinch, the + and - buttons or keys, double-click
// (or double-tap) to switch between 100% and fit, drag to move around.

import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "preact/hooks";

interface View {
  /** Video pixels to CSS pixels. */
  scale: number;
  /** Offset of the video's centre from the viewport's centre, in CSS px. */
  x: number;
  y: number;
  /** Fit to the viewport; follows the window and video size. */
  fit: boolean;
}

const STEP = 1.25;

export function ZoomView(props: { source: MediaStream | MediaStreamTrack; title: string; onClose: () => void }) {
  const { source, title, onClose } = props;
  const root = useRef<HTMLDivElement>(null);
  const port = useRef<HTMLDivElement>(null);
  const vid = useRef<HTMLVideoElement>(null);
  const stream = useMemo(() => (source instanceof MediaStream ? source : new MediaStream([source])), [source]);
  const [nat, setNat] = useState({ w: 0, h: 0 });
  const [box, setBox] = useState({ w: 0, h: 0 });
  const [view, setView] = useState<View>({ scale: 1, x: 0, y: 0, fit: true });
  const [full, setFull] = useState(false);
  const pointers = useRef(new Map<number, { x: number; y: number }>());
  const gesture = useRef<{ dist: number; mx: number; my: number } | null>(null);
  const lastTap = useRef(0);
  const tapToggled = useRef(0);

  useEffect(() => {
    const el = vid.current;
    if (!el) return;
    el.srcObject = stream;
    void el.play().catch(() => undefined);
    const size = () => setNat({ w: el.videoWidth, h: el.videoHeight });
    el.addEventListener("loadedmetadata", size);
    el.addEventListener("resize", size);
    size();
    return () => {
      el.removeEventListener("loadedmetadata", size);
      el.removeEventListener("resize", size);
    };
  }, [stream]);

  useLayoutEffect(() => {
    const el = port.current;
    if (!el) return;
    const measure = () => setBox({ w: el.clientWidth, h: el.clientHeight });
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const ready = nat.w > 0 && nat.h > 0 && box.w > 0 && box.h > 0;
  const fitScale = ready ? Math.min(box.w / nat.w, box.h / nat.h) : 1;
  const minScale = Math.min(fitScale, 1);
  const maxScale = Math.max(fitScale, 1) * 8;

  /** Keeps the picture from being dragged out of view. */
  const clamp = (v: View): View => {
    if (!ready) return v;
    const scale = Math.max(minScale, Math.min(maxScale, v.scale));
    const mx = Math.max(0, (nat.w * scale - box.w) / 2);
    const my = Math.max(0, (nat.h * scale - box.h) / 2);
    return { scale, x: Math.max(-mx, Math.min(mx, v.x)), y: Math.max(-my, Math.min(my, v.y)), fit: v.fit };
  };
  const shown = view.fit ? { scale: fitScale, x: 0, y: 0, fit: true } : clamp(view);

  /** Zoom to `scale`, keeping the point (px, py) from the centre still. */
  const zoomAt = (scale: number, px = 0, py = 0) => {
    const cur = shown;
    const s = Math.max(minScale, Math.min(maxScale, scale));
    const k = s / cur.scale;
    setView(clamp({ scale: s, x: px - (px - cur.x) * k, y: py - (py - cur.y) * k, fit: false }));
  };
  const fromCentre = (clientX: number, clientY: number) => {
    const r = port.current!.getBoundingClientRect();
    return { px: clientX - r.left - r.width / 2, py: clientY - r.top - r.height / 2 };
  };
  const toggle100 = (clientX?: number, clientY?: number) => {
    if (!view.fit && Math.abs(shown.scale - 1) < 0.01) {
      setView({ scale: fitScale, x: 0, y: 0, fit: true });
      return;
    }
    const p = clientX === undefined || clientY === undefined ? { px: 0, py: 0 } : fromCentre(clientX, clientY);
    zoomAt(1, p.px, p.py);
  };
  const reset = () => setView({ scale: fitScale, x: 0, y: 0, fit: true });

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !document.fullscreenElement) onClose();
      else if (e.key === "+" || e.key === "=") zoomAt(shown.scale * STEP);
      else if (e.key === "-") zoomAt(shown.scale / STEP);
      else if (e.key === "0") reset();
      else return;
      e.preventDefault();
      e.stopPropagation();
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  });

  useEffect(() => {
    const onFs = () => setFull(document.fullscreenElement === root.current);
    document.addEventListener("fullscreenchange", onFs);
    return () => {
      document.removeEventListener("fullscreenchange", onFs);
      if (document.fullscreenElement === root.current) void document.exitFullscreen().catch(() => undefined);
    };
  }, []);

  // The wheel needs a non-passive listener to stop the page scrolling.
  useEffect(() => {
    const el = port.current;
    if (!el) return;
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      const dy = e.deltaMode === 1 ? e.deltaY * 16 : e.deltaY;
      const p = fromCentre(e.clientX, e.clientY);
      zoomAt(shown.scale * Math.exp(-dy * 0.0015), p.px, p.py);
    };
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => el.removeEventListener("wheel", onWheel);
  });

  const pinchInfo = () => {
    const [a, b] = [...pointers.current.values()];
    return { dist: Math.hypot(a.x - b.x, a.y - b.y), mx: (a.x + b.x) / 2, my: (a.y + b.y) / 2 };
  };

  const onPointerDown = (e: PointerEvent) => {
    (e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId);
    pointers.current.set(e.pointerId, { x: e.clientX, y: e.clientY });
    if (pointers.current.size === 2) gesture.current = pinchInfo();
    // Double-tap on touch screens (dblclick does not fire for touch everywhere).
    if (e.pointerType === "touch" && pointers.current.size === 1) {
      const now = Date.now();
      if (now - lastTap.current < 300) {
        toggle100(e.clientX, e.clientY);
        lastTap.current = 0;
        tapToggled.current = now;
      } else lastTap.current = now;
    }
  };
  const onPointerMove = (e: PointerEvent) => {
    const prev = pointers.current.get(e.pointerId);
    if (!prev) return;
    const cur = { x: e.clientX, y: e.clientY };
    pointers.current.set(e.pointerId, cur);
    if (pointers.current.size === 1) {
      if (shown.fit && shown.scale <= fitScale) return;
      setView(clamp({ ...shown, x: shown.x + cur.x - prev.x, y: shown.y + cur.y - prev.y, fit: false }));
    } else if (pointers.current.size === 2 && gesture.current) {
      const g = pinchInfo();
      const p = fromCentre(g.mx, g.my);
      const k = g.dist / (gesture.current.dist || 1);
      const s = Math.max(minScale, Math.min(maxScale, shown.scale * k));
      const kk = s / shown.scale;
      // Zoom about the midpoint, and follow the midpoint as it moves.
      setView(
        clamp({
          scale: s,
          x: p.px - (p.px - shown.x) * kk + (g.mx - gesture.current.mx),
          y: p.py - (p.py - shown.y) * kk + (g.my - gesture.current.my),
          fit: false,
        }),
      );
      gesture.current = g;
    }
  };
  const onPointerUp = (e: PointerEvent) => {
    pointers.current.delete(e.pointerId);
    if (pointers.current.size < 2) gesture.current = null;
  };

  const canFull = typeof document !== "undefined" && document.fullscreenEnabled;
  const toggleFull = () => {
    if (document.fullscreenElement) void document.exitFullscreen().catch(() => undefined);
    else void root.current?.requestFullscreen().catch(() => undefined);
  };

  const pct = Math.round(shown.scale * 100);
  const zoomed = shown.scale > fitScale + 0.001;
  return (
    <div class="zoom-view" ref={root} role="dialog" aria-modal="true" aria-label={title}>
      <div class="zoom-bar">
        <span class="zoom-title">{title}</span>
        <div class="grow" />
        <button type="button" class="btn-small" aria-label="Zoom out" title="Zoom out" onClick={() => zoomAt(shown.scale / STEP)}>
          −
        </button>
        <span class="zoom-pct" aria-live="polite">
          {pct}%
        </span>
        <button type="button" class="btn-small" aria-label="Zoom in" title="Zoom in" onClick={() => zoomAt(shown.scale * STEP)}>
          +
        </button>
        <button type="button" class="btn-small" aria-pressed={!view.fit && Math.abs(shown.scale - 1) < 0.01} onClick={() => zoomAt(1)}>
          100%
        </button>
        <button type="button" class="btn-small" onClick={reset}>
          Reset
        </button>
        {canFull && (
          <button type="button" class="btn-small" aria-pressed={full} onClick={toggleFull}>
            {full ? "Exit full screen" : "Full screen"}
          </button>
        )}
        <button type="button" class="btn-small" onClick={onClose}>
          Close
        </button>
      </div>
      <div
        class={zoomed ? "zoom-port zoomed" : "zoom-port"}
        ref={port}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onPointerCancel={onPointerUp}
        onDblClick={(e) => {
          // A double-tap was handled above; some browsers also send dblclick.
          if (Date.now() - tapToggled.current > 500) toggle100(e.clientX, e.clientY);
        }}
      >
        <video
          ref={vid}
          class="zoom-video"
          autoplay
          playsInline
          muted
          style={{
            width: `${nat.w || box.w}px`,
            height: `${nat.h || box.h}px`,
            transform: `translate(${shown.x - (nat.w || box.w) / 2}px, ${shown.y - (nat.h || box.h) / 2}px) scale(${ready ? shown.scale : 1})`,
          }}
        />
      </div>
    </div>
  );
}
