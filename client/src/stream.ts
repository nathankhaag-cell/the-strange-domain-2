// The node's live event stream (/api/v1/stream). The first frame carries the
// session token; browsers can't set headers on WebSockets and tokens don't
// belong in URLs. Events only say what changed; we fetch the content.

import { getToken } from "./api";

export interface StreamEvent {
  type: string;
  group_id?: string;
  seq?: number;
}

export class Stream {
  private ws: WebSocket | null = null;
  private closed = false;
  private backoff = 1000;
  private timer: number | undefined;

  constructor(
    private onEvent: (ev: StreamEvent) => void,
    private onOnline: (online: boolean) => void,
  ) {}

  connect() {
    if (this.closed) return;
    const url = `${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}/api/v1/stream`;
    const ws = new WebSocket(url);
    this.ws = ws;
    ws.onopen = () => ws.send(JSON.stringify({ token: getToken() }));
    ws.onmessage = (m) => {
      let ev: StreamEvent;
      try {
        ev = JSON.parse(String(m.data));
      } catch {
        return;
      }
      if (ev.type === "ready") {
        this.backoff = 1000;
        this.onOnline(true);
        return;
      }
      this.onEvent(ev);
    };
    ws.onclose = () => {
      if (this.ws !== ws) return;
      this.ws = null;
      this.onOnline(false);
      if (this.closed) return;
      this.timer = window.setTimeout(() => this.connect(), this.backoff);
      this.backoff = Math.min(this.backoff * 2, 30000);
    };
  }

  close() {
    this.closed = true;
    window.clearTimeout(this.timer);
    this.ws?.close();
    this.ws = null;
  }
}
