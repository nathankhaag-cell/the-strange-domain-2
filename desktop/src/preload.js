// Exposes the node picker to the app's own connect page only. Pages served
// by the node get nothing from here.
"use strict";

const { contextBridge, ipcRenderer } = require("electron");

if (location.protocol === "file:") {
  contextBridge.exposeInMainWorld("nodePicker", {
    current: () => ipcRenderer.invoke("node:current"),
    probe: (address) => ipcRenderer.invoke("node:probe", String(address)),
    connect: (url) => ipcRenderer.invoke("node:connect", String(url)),
  });
}
