// The connect page's title bar. The window has no system frame, so the page
// draws the bar: the app menu, full screen, and on Windows and Linux
// minimize, maximize/restore and close. The node's client draws the same bar
// (client/src/ui/TitleBar.tsx) once it has loaded; main.js adds this one to
// a node whose client is too old to draw it.
"use strict";

(() => {
  const d = window.sdDesktop;
  if (!d) return;
  const mac = d.platform === "darwin";

  const svg = (body) =>
    `<svg width="12" height="12" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="square" aria-hidden="true">${body}</svg>`;
  const ICONS = {
    menu: svg('<path d="M1 2.5h10M1 6h10M1 9.5h10"/>'),
    fullscreen: svg('<path d="M1 4V1h3M8 1h3v3M11 8v3H8M4 11H1V8"/>'),
    minimize: svg('<path d="M1.5 6.5h9"/>'),
    maximize: svg('<rect x="1.5" y="1.5" width="9" height="9"/>'),
    restore: svg('<rect x="1.5" y="3.5" width="7" height="7"/><path d="M3.5 3.5v-2h7v7h-2"/>'),
    close: svg('<path d="M1.5 1.5l9 9M10.5 1.5l-9 9"/>'),
  };

  const bar = document.createElement("div");
  bar.className = "titlebar";
  bar.dataset.platform = d.platform;

  function button(name, label, onClick, extra) {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "tb-btn" + (extra ? " " + extra : "");
    b.setAttribute("aria-label", label);
    b.title = label;
    b.innerHTML = ICONS[name];
    b.addEventListener("click", onClick);
    return b;
  }

  if (!mac) {
    const menu = button("menu", "Menu", () => {
      // Just under the bar, at the button's left edge.
      d.showMenu(menu.getBoundingClientRect().left, bar.getBoundingClientRect().bottom);
    });
    bar.append(menu);
  }
  const fill = document.createElement("div");
  fill.className = "tb-fill";
  bar.append(fill);
  bar.append(button("fullscreen", "Full screen (F11)", () => d.toggleFullscreen()));
  let max = null;
  if (!mac) {
    max = button("maximize", "Maximize", () => d.maximize());
    bar.append(button("minimize", "Minimize", () => d.minimize()), max, button("close", "Close", () => d.close(), "tb-close"));
  }
  document.body.prepend(bar);

  function apply(s) {
    if (!s) return;
    document.documentElement.classList.toggle("has-titlebar", !s.fullscreen);
    bar.hidden = !!s.fullscreen;
    bar.classList.toggle("blurred", !s.focused);
    if (max) {
      const label = s.maximized ? "Restore" : "Maximize";
      max.setAttribute("aria-label", label);
      max.title = label;
      max.innerHTML = s.maximized ? ICONS.restore : ICONS.maximize;
    }
  }
  document.documentElement.classList.add("has-titlebar");
  d.onState(apply);
  void d.getState().then(apply);
})();
