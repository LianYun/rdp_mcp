import type { ApiClient } from "./client";
import type { ServerEvent } from "./types";

const EVENT_TYPES = [
  "snapshot",
  "machine_state",
  "control_mode",
  "tool_call",
  "cert_verify",
  "config_changed",
  "config_error",
  "service",
  "log",
] as const;

export interface EventStreamHandlers {
  onEvent: (ev: ServerEvent) => void;
  onOpen?: () => void;
  onError?: () => void;
}

/** 管理 API 的 SSE 事件流（GET /events）。EventSource 自带断线重连；每次重连服务端会重新推 snapshot。 */
export function openEventStream(client: ApiClient, h: EventStreamHandlers): () => void {
  const es = new EventSource(client.withToken(client.base + "/events"));
  es.onopen = () => h.onOpen?.();
  es.onerror = () => h.onError?.();
  for (const type of EVENT_TYPES) {
    es.addEventListener(type, (e) => {
      try {
        h.onEvent({ type, data: JSON.parse((e as MessageEvent).data) } as ServerEvent);
      } catch {
        /* 忽略坏事件 */
      }
    });
  }
  return () => es.close();
}
