// The desktop app's window API (desktop/src/preload.js). Present only when
// the client runs inside the desktop app; browsers and the phone app have
// none, and everything that uses it is skipped there.

export interface WindowState {
  maximized: boolean;
  fullscreen: boolean;
  focused: boolean;
}

export interface DesktopApi {
  platform: string;
  minimize(): void;
  maximize(): void;
  close(): void;
  toggleFullscreen(): void;
  getState(): Promise<WindowState | null>;
  isFullscreen(): Promise<boolean>;
  onState(cb: (s: WindowState) => void): () => void;
  showMenu(x: number, y: number): void;
  setGlobalMute(accelerator: string | null): Promise<boolean>;
  onGlobalMute(cb: () => void): () => void;
}

export function desktop(): DesktopApi | undefined {
  const d = (globalThis as { sdDesktop?: DesktopApi }).sdDesktop;
  return d && typeof d.minimize === "function" ? d : undefined;
}
