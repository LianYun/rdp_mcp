import { create } from "zustand";
import type { ApiClient } from "@/api/client";
import { openEventStream } from "@/api/events";
import type { Discovery, ServerEvent } from "@/api/types";
import { clientFrom, discover } from "@/lib/service";
import { initialLive, reduceEvent, type LiveState } from "./reduce";

export type ServiceStatus = "checking" | "running" | "stopped" | "error";

interface Store extends LiveState {
  discovery: Discovery | null;
  client: ApiClient | null;
  status: ServiceStatus;
  /** SSE 是否已连接。 */
  connected: boolean;
  busy: boolean;
  lastError?: string;
  refresh: () => Promise<void>;
  setBusy: (b: boolean, err?: string) => void;
  dispatch: (ev: ServerEvent) => void;
}

let closeStream: (() => void) | null = null;
let streamKey = "";

export const useLive = create<Store>((set, get) => ({
  ...initialLive(),
  discovery: null,
  client: null,
  status: "checking",
  connected: false,
  busy: false,

  setBusy: (busy, err) => set({ busy, lastError: err }),

  dispatch: (ev) => set((s) => reduceEvent(s, ev)),

  refresh: async () => {
    let d: Discovery;
    try {
      d = await discover();
    } catch (e) {
      set({ status: "error", lastError: e instanceof Error ? e.message : String(e) });
      return;
    }
    const prev = get().discovery;
    const client = clientFrom(d);
    const key = client ? `${client.listen}|${client.token}|${d.pid}` : "";
    const status: ServiceStatus = d.running ? "running" : d.error ? "error" : "stopped";
    const patch: Partial<Store> = { discovery: d, status, lastError: d.error ?? get().lastError };
    if (key !== streamKey) {
      closeStream?.();
      closeStream = null;
      streamKey = key;
      if (client) {
        patch.client = client;
        closeStream = openEventStream(client, {
          onEvent: (ev) => get().dispatch(ev),
          onOpen: () => set({ connected: true }),
          onError: () => set({ connected: false }),
        });
      } else {
        Object.assign(patch, { client: null, connected: false, ...initialLive() });
      }
    }
    if (!d.running && prev?.running) Object.assign(patch, { connected: false });
    set(patch);
  },
}));

let timer: ReturnType<typeof setInterval> | null = null;

/** 开始轮询服务发现（3 秒）；服务启停、重启后自动重新附着。 */
export function startDiscoveryPolling(): () => void {
  void useLive.getState().refresh();
  timer = setInterval(() => void useLive.getState().refresh(), 3000);
  return () => {
    if (timer) clearInterval(timer);
    closeStream?.();
    closeStream = null;
    streamKey = "";
  };
}
