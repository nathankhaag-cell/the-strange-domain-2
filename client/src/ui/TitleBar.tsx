// The desktop app's title bar. The window has no system frame, so the
// client draws one: the app menu (File, Edit, View, Window), full screen,
// and on Windows and Linux minimize, maximize/restore and close.
// macOS keeps its own traffic lights at the left. In full screen the bar is
// hidden; F11 brings the window back. Rendered only in the desktop app.

import { useEffect, useLayoutEffect, useState } from "preact/hooks";
import { desktop, type WindowState } from "../desktop";

const Glyph = ({ d }: { d: string }) => (
  <svg width="12" height="12" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="square" aria-hidden="true">
    <path d={d} />
  </svg>
);
const MENU = "M1 2.5h10M1 6h10M1 9.5h10";
const FULLSCREEN = "M1 4V1h3M8 1h3v3M11 8v3H8M4 11H1V8";
const MINIMIZE = "M1.5 6.5h9";
const MAXIMIZE = "M1.5 1.5h9v9h-9z";
const RESTORE = "M1.5 3.5h7v7h-7zM3.5 3.5v-2h7v7h-2";
const CLOSE = "M1.5 1.5l9 9M10.5 1.5l-9 9";

export function TitleBar() {
  const d = desktop();
  const [st, setSt] = useState<WindowState>({ maximized: false, fullscreen: false, focused: true });
  useEffect(() => {
    if (!d) return;
    void d.getState().then((s) => s && setSt(s));
    return d.onState(setSt);
  }, []);
  useLayoutEffect(() => {
    document.documentElement.classList.toggle("has-titlebar", !!d && !st.fullscreen);
  }, [st.fullscreen]);
  if (!d || st.fullscreen) return null;
  const mac = d.platform === "darwin";
  const btn = (label: string, glyph: string, onClick: (e: MouseEvent) => void, extra = "") => (
    <button type="button" class={"tb-btn" + extra} aria-label={label} title={label} onClick={onClick}>
      <Glyph d={glyph} />
    </button>
  );
  return (
    <div class={st.focused ? "titlebar" : "titlebar blurred"} data-platform={d.platform}>
      {!mac &&
        btn("Menu", MENU, (e) => {
          // Just under the bar, at the button's left edge.
          const b = e.currentTarget as HTMLElement;
          d.showMenu(b.getBoundingClientRect().left, b.parentElement!.getBoundingClientRect().bottom);
        })}
      <div class="tb-fill" />
      {btn("Full screen (F11)", FULLSCREEN, () => d.toggleFullscreen())}
      {!mac && btn("Minimize", MINIMIZE, () => d.minimize())}
      {!mac && btn(st.maximized ? "Restore" : "Maximize", st.maximized ? RESTORE : MAXIMIZE, () => d.maximize())}
      {!mac && btn("Close", CLOSE, () => d.close(), " tb-close")}
    </div>
  );
}
