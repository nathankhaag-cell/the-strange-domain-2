// Runs in the window's pages, isolated from them (contextIsolation).
//
// - nodePicker: the node picker, for the app's own connect page only.
// - sdDesktop: the window controls behind the page-drawn title bar, and the
//   one background shortcut (Toggle mute). Both the connect page and the
//   node's client use it. The main process checks that each request comes
//   from one of those two pages (main.js, fromWindowPage).
"use strict";

const { contextBridge, ipcRenderer } = require("electron");

if (location.protocol === "file:") {
  contextBridge.exposeInMainWorld("nodePicker", {
    current: () => ipcRenderer.invoke("node:current"),
    probe: (address) => ipcRenderer.invoke("node:probe", String(address)),
    connect: (url) => ipcRenderer.invoke("node:connect", String(url)),
  });
}

/** Subscribes to a main-process message; returns the unsubscribe function. */
function listen(channel, cb) {
  if (typeof cb !== "function") return () => undefined;
  const handler = (_e, value) => cb(value);
  ipcRenderer.on(channel, handler);
  return () => ipcRenderer.removeListener(channel, handler);
}

contextBridge.exposeInMainWorld("sdDesktop", {
  platform: process.platform,
  minimize: () => ipcRenderer.send("window:minimize"),
  /** Maximizes, or restores when already maximized. */
  maximize: () => ipcRenderer.send("window:maximize"),
  close: () => ipcRenderer.send("window:close"),
  toggleFullscreen: () => ipcRenderer.send("window:fullscreen"),
  /** @returns {Promise<{maximized: boolean, fullscreen: boolean, focused: boolean} | null>} */
  getState: () => ipcRenderer.invoke("window:state"),
  isFullscreen: () => ipcRenderer.invoke("window:state").then((s) => !!s?.fullscreen),
  onState: (cb) => listen("window:state", cb),
  /** Opens the app menu (File, Edit, View, Window) at x, y in the page. */
  showMenu: (x, y) => ipcRenderer.send("window:menu", Number(x), Number(y)),
  /** Registers the background Toggle mute shortcut (an Electron accelerator), or clears it with null. */
  setGlobalMute: (accelerator) => ipcRenderer.invoke("keys:global-mute", accelerator == null ? null : String(accelerator)),
  onGlobalMute: (cb) => listen("keys:global-mute", () => cb()),
});
