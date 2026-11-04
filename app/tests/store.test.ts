import { describe, expect, it } from "vitest";
import { initialLive, mergeActivity, reduceEvent, MAX_ACTIVITY } from "@/stores/reduce";
import type { AuditRecord, MachineInfo } from "@/api/types";

const m = (name: string, p: Partial<MachineInfo> = {}): MachineInfo => ({
  name,
  host: "h",
  port: 3389,
  username: "u",
  enabled: true,
  state: "idle",
  control_mode: "agent",
  viewers: 0,
  ...p,
});

describe("reduceEvent", () => {
  it("replaces machines on snapshot and collects pending certs", () => {
    const s = reduceEvent(initialLive(), {
      type: "snapshot",
      data: { machines: [m("a"), m("b", { pending_cert: { host: "h", port: 1, fingerprint: "ab", subject: "", issuer: "", reason: "new" } })] },
    });
    expect(s.machines.map((x) => x.name)).toEqual(["a", "b"]);
    expect(s.certs.b.fingerprint).toBe("ab");
    expect(s.certs.a).toBeUndefined();
  });

  it("updates state and parses the resolution when connected", () => {
    let s = reduceEvent(initialLive(), { type: "snapshot", data: { machines: [m("a")] } });
    s = reduceEvent(s, { type: "machine_state", data: { machine: "a", state: "connected", resolution: "1920x1080" } });
    expect(s.machines[0]).toMatchObject({ state: "connected", width: 1920, height: 1080 });
  });

  it("clears a pending cert once the machine connects or fails", () => {
    let s = reduceEvent(initialLive(), { type: "snapshot", data: { machines: [m("a")] } });
    s = reduceEvent(s, { type: "cert_verify", data: { machine: "a", host: "h", port: 1, fingerprint: "f", subject: "", issuer: "", reason: "", pending: true } });
    expect(s.certs.a).toBeDefined();
    s = reduceEvent(s, { type: "machine_state", data: { machine: "a", state: "connecting" } });
    expect(s.certs.a).toBeDefined();
    s = reduceEvent(s, { type: "machine_state", data: { machine: "a", state: "failed", error: "rejected" } });
    expect(s.certs.a).toBeUndefined();
    expect(s.machines[0].last_error).toBe("rejected");
  });

  it("updates the control mode", () => {
    let s = reduceEvent(initialLive(), { type: "snapshot", data: { machines: [m("a")] } });
    s = reduceEvent(s, { type: "control_mode", data: { machine: "a", mode: "human" } });
    expect(s.machines[0].control_mode).toBe("human");
  });

  it("ignores state events for unknown machines", () => {
    const s = reduceEvent(initialLive(), { type: "machine_state", data: { machine: "zz", state: "connected" } });
    expect(s.machines).toEqual([]);
  });

  it("tracks tool calls and the machine's last activity", () => {
    let s = reduceEvent(initialLive(), { type: "snapshot", data: { machines: [m("a")] } });
    const start: AuditRecord = { id: "1", time: "2026-01-01T00:00:00Z", phase: "start", tool: "mouse", machine: "a" };
    s = reduceEvent(s, { type: "tool_call", data: start });
    expect(s.activity).toHaveLength(1);
    expect(s.machines[0].last_activity).toBe(start.time);
    s = reduceEvent(s, { type: "tool_call", data: { ...start, phase: "end", ok: true, duration_ms: 12 } });
    expect(s.activity).toHaveLength(1);
    expect(s.activity[0]).toMatchObject({ phase: "end", ok: true, duration_ms: 12 });
  });

  it("records config errors and clears them on the next change", () => {
    let s = reduceEvent(initialLive(), { type: "config_error", data: { error: "bad yaml" } });
    expect(s.configError).toBe("bad yaml");
    s = reduceEvent(s, { type: "config_changed", data: { machines: 1 } });
    expect(s.configError).toBeUndefined();
  });

  it("flags service stopping and resets on snapshot", () => {
    let s = reduceEvent(initialLive(), { type: "service", data: { status: "stopping" } });
    expect(s.stopping).toBe(true);
    s = reduceEvent(s, { type: "snapshot", data: { machines: [] } });
    expect(s.stopping).toBe(false);
  });
});

describe("mergeActivity", () => {
  const rec = (id: string, phase: "start" | "end"): AuditRecord => ({ id, time: "t", phase, tool: "x" });
  it("prepends new records and caps the list", () => {
    let l: AuditRecord[] = [];
    for (let i = 0; i < MAX_ACTIVITY + 10; i++) l = mergeActivity(l, rec(String(i), "end"));
    expect(l).toHaveLength(MAX_ACTIVITY);
    expect(l[0].id).toBe(String(MAX_ACTIVITY + 9));
  });
  it("does not let a late start overwrite an end", () => {
    let l = mergeActivity([], rec("1", "end"));
    l = mergeActivity(l, rec("1", "start"));
    expect(l[0].phase).toBe("end");
  });
});
