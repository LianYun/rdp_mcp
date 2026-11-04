// SSE 事件 -> 前端状态的纯函数归约（便于单测）。

import type { AuditRecord, CertVerifyEvent, MachineInfo, ServerEvent } from "@/api/types";

export const MAX_ACTIVITY = 500;

export interface LiveState {
  machines: MachineInfo[];
  /** 实时工具调用（phase=end 的记录会替换同 id 的 start）。最新在前。 */
  activity: AuditRecord[];
  /** 待确认证书，按机器名。 */
  certs: Record<string, CertVerifyEvent>;
  /** 服务最近一次 config_error。 */
  configError?: string;
  /** 收到 service stopping。 */
  stopping: boolean;
}

export const initialLive = (): LiveState => ({ machines: [], activity: [], certs: {}, stopping: false });

function patchMachine(ms: MachineInfo[], name: string, p: Partial<MachineInfo>): MachineInfo[] {
  return ms.map((m) => (m.name === name ? { ...m, ...p } : m));
}

/** 服务端的 machine_state.resolution 实际是 [w, h] 数组（文档未写明类型）；也兼容 "WxH" 字符串。 */
export function parseResolution(r?: [number, number] | string): { width?: number; height?: number } {
  if (Array.isArray(r) && r.length === 2 && r.every((n) => Number.isFinite(n))) return { width: r[0], height: r[1] };
  if (typeof r === "string") {
    const m = r.match(/^(\d+)x(\d+)$/);
    if (m) return { width: Number(m[1]), height: Number(m[2]) };
  }
  return {};
}

export function mergeActivity(list: AuditRecord[], rec: AuditRecord): AuditRecord[] {
  const idx = list.findIndex((r) => r.id === rec.id);
  if (idx >= 0) {
    // end 覆盖 start；重复的 start 忽略
    if (rec.phase === "end" || list[idx].phase === "start") {
      const next = list.slice();
      next[idx] = { ...list[idx], ...rec };
      return next;
    }
    return list;
  }
  return [rec, ...list].slice(0, MAX_ACTIVITY);
}

export function reduceEvent(s: LiveState, ev: ServerEvent): LiveState {
  switch (ev.type) {
    case "snapshot": {
      const certs: Record<string, CertVerifyEvent> = {};
      for (const m of ev.data.machines) {
        if (m.pending_cert) certs[m.name] = { machine: m.name, pending: true, ...m.pending_cert };
      }
      return { ...s, machines: ev.data.machines, certs, stopping: false };
    }
    case "machine_state": {
      const { machine, state, error, resolution } = ev.data;
      const certs = { ...s.certs };
      if (state === "connected" || state === "failed") delete certs[machine];
      return {
        ...s,
        certs,
        machines: patchMachine(s.machines, machine, {
          state,
          last_error: error || undefined,
          ...(state === "connected" ? parseResolution(resolution) : {}),
        }),
      };
    }
    case "control_mode":
      return { ...s, machines: patchMachine(s.machines, ev.data.machine, { control_mode: ev.data.mode }) };
    case "tool_call": {
      const rec = ev.data;
      const machines = rec.machine ? patchMachine(s.machines, rec.machine, { last_activity: rec.time }) : s.machines;
      return { ...s, machines, activity: mergeActivity(s.activity, rec) };
    }
    case "cert_verify":
      return { ...s, certs: { ...s.certs, [ev.data.machine]: ev.data } };
    case "config_error":
      return { ...s, configError: ev.data.error };
    case "config_changed":
      return { ...s, configError: undefined };
    case "service":
      return { ...s, stopping: ev.data.status === "stopping" };
    default:
      return s;
  }
}
