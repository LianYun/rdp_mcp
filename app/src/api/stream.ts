import type { ApiClient } from "./client";
import type { StreamInput, StreamMessage } from "./types";

export interface StreamOptions {
  fps?: number;
  maxWidth?: number;
  quality?: number;
  /** 未连接时是否由服务发起连接（默认 true）。 */
  connect?: boolean;
}

export interface StreamHandlers {
  onFrame: (jpeg: Blob) => void;
  onMessage: (msg: StreamMessage) => void;
  onOpen?: () => void;
  onClose?: () => void;
}

/** 实时画面 WebSocket 封装（docs/admin-api.md「实时画面」）。 */
export class LiveStream {
  private ws: WebSocket | null = null;
  private closed = false;

  constructor(
    private client: ApiClient,
    private machine: string,
    private opts: StreamOptions,
    private h: StreamHandlers,
  ) {}

  static url(client: ApiClient, machine: string, o: StreamOptions): string {
    const base = `${client.origin.replace(/^http/, "ws")}/api/v1/machines/${encodeURIComponent(machine)}/stream`;
    return client.withToken(base, { fps: o.fps, max_width: o.maxWidth, quality: o.quality, connect: o.connect });
  }

  open(): void {
    this.closed = false;
    const ws = new WebSocket(LiveStream.url(this.client, this.machine, this.opts));
    this.ws = ws;
    ws.onopen = () => this.h.onOpen?.();
    ws.onclose = () => {
      if (this.ws === ws) this.ws = null;
      this.h.onClose?.();
    };
    ws.onmessage = (ev) => {
      if (typeof ev.data === "string") {
        try {
          this.h.onMessage(JSON.parse(ev.data) as StreamMessage);
        } catch {
          /* 忽略坏消息 */
        }
      } else if (ev.data instanceof Blob) {
        this.h.onFrame(ev.data);
      }
    };
  }

  get isOpen(): boolean {
    return this.ws?.readyState === WebSocket.OPEN;
  }

  send(m: StreamInput): void {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(m));
  }

  close(): void {
    this.closed = true;
    this.ws?.close();
    this.ws = null;
  }

  get wasClosedByUs(): boolean {
    return this.closed;
  }
}
