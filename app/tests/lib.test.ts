import { describe, expect, it } from "vitest";
import { buildSnippet } from "@/lib/snippets";
import { machineNameFromFile, parseRdpFile } from "@/lib/rdpfile";
import { emptyForm, formFromConfig, parseTags, toMachineDef, validateForm } from "@/lib/machineForm";
import { buildPatch, DEFAULT_FIELDS, readAll, SERVER_FIELDS, validateField } from "@/lib/settingsPatch";
import { cursorGeometry, fitSize, FpsMeter, mouseButton, toFrameCoords, wheelNotches } from "@/lib/input";
import { parseLogLine } from "@/lib/logs";
import { filterRecords, toJsonl } from "@/pages/Activity";
import { en } from "@/i18n/en";
import { translate } from "@/i18n";
import { zh } from "@/i18n/zh";

describe("snippets", () => {
  const url = "http://127.0.0.1:8765/mcp";
  it("builds the Claude Code command", () => {
    expect(buildSnippet("claude-code", url, "abc")).toBe(`claude mcp add --transport http remoter ${url} --header "Authorization: Bearer abc"`);
  });
  it("omits the header when no token is set", () => {
    expect(buildSnippet("claude-code", url, "")).toBe(`claude mcp add --transport http remoter ${url}`);
    expect(JSON.parse(buildSnippet("json", url, "")).mcpServers.remoter.headers).toBeUndefined();
  });
  it("masks the token for display", () => {
    expect(buildSnippet("claude-code", url, "secret", true)).not.toContain("secret");
    expect(buildSnippet("json", url, "secret", true)).toContain("<token>");
  });
  it("builds the Codex CLI command", () => {
    expect(buildSnippet("codex-cli", url, "")).toBe(`codex mcp add remoter --url ${url}`);
    expect(buildSnippet("codex-cli", url, "abc")).toBe(
      `export REMOTER_MCP_TOKEN=abc\ncodex mcp add remoter --url ${url} --bearer-token-env-var REMOTER_MCP_TOKEN`,
    );
    expect(buildSnippet("codex-cli", url, "secret", true)).not.toContain("secret");
  });
  it("builds the Codex config.toml block", () => {
    expect(buildSnippet("codex-app", url, "abc")).toBe(
      [
        "[mcp_servers.remoter]",
        `url = "${url}"`,
        'http_headers = { "Authorization" = "Bearer abc" }',
        "startup_timeout_sec = 30",
        "tool_timeout_sec = 180",
      ].join("\n"),
    );
    expect(buildSnippet("codex-app", url, "")).not.toContain("http_headers");
    expect(buildSnippet("codex-app", url, "secret", true)).toContain("<token>");
  });
  it("builds valid JSON snippets", () => {
    const j = JSON.parse(buildSnippet("json", url, "abc"));
    expect(j.mcpServers.remoter).toEqual({ type: "http", url, headers: { Authorization: "Bearer abc" } });
    const d = JSON.parse(buildSnippet("claude-desktop", url, "abc"));
    expect(d.mcpServers.remoter.args).toContain("mcp-remote");
    expect(d.mcpServers.remoter.args).toContain("--allow-http");
  });
});

describe(".rdp import", () => {
  it("parses address, port, user and size", () => {
    const r = parseRdpFile("screen mode id:i:2\r\nfull address:s:10.0.0.5:3390\r\nusername:s:CORP\\alice\r\ndesktopwidth:i:1600\r\ndesktopheight:i:900\r\n");
    expect(r).toMatchObject({ host: "10.0.0.5", port: 3390, username: "alice", domain: "CORP", width: 1600, height: 900 });
  });
  it("handles a bare host, a BOM and ipv6", () => {
    expect(parseRdpFile("﻿full address:s:win-box").host).toBe("win-box");
    expect(parseRdpFile("full address:s:[fe80::1]:3390")).toMatchObject({ host: "fe80::1", port: 3390 });
  });
  it("maps enablecredsspsupport to security", () => {
    expect(parseRdpFile("full address:s:h\nenablecredsspsupport:i:0").security).toBe("tls");
    expect(parseRdpFile("full address:s:h\nenablecredsspsupport:i:1").security).toBe("nla");
  });
  it("returns no host for junk", () => {
    expect(parseRdpFile("hello").host).toBeUndefined();
  });
  it("derives machine names", () => {
    expect(machineNameFromFile("My PC (work).rdp")).toBe("My-PC-work");
    expect(machineNameFromFile("", "10.0.0.5")).toBe("10-0-0-5");
  });
});

describe("machine form", () => {
  it("validates required fields and ranges", () => {
    const e = validateForm({ ...emptyForm(), name: "bad name", port: "70000", width: "10", idle_disconnect: "soon" });
    expect(e.name).toBe("form.err.name_chars");
    expect(e.host).toBe("form.err.host_required");
    expect(e.username).toBe("form.err.user_required");
    expect(e.port).toBe("form.err.port");
    expect(e.width).toBe("form.err.size");
    expect(e.idle_disconnect).toBe("form.err.duration");
  });
  it("accepts a valid form", () => {
    expect(validateForm({ ...emptyForm(), name: "win-1", host: "h", username: "u", idle_disconnect: "1h30m" })).toEqual({});
    expect(validateForm({ ...emptyForm(), name: "w", host: "h", username: "u", idle_disconnect: "0" })).toEqual({});
  });
  it("requires a fingerprint for the fingerprint policy", () => {
    expect(validateForm({ ...emptyForm(), name: "w", host: "h", username: "u", cert: "fingerprint" }).cert_fingerprint).toBeDefined();
  });
  it("omits empty optional fields (PUT replaces the definition)", () => {
    const d = toMachineDef({ ...emptyForm(), name: "w", host: " h ", username: "u", tags: "a, b" });
    expect(d).toEqual({ name: "w", host: "h", username: "u", enabled: true, tags: ["a", "b"] });
  });
  it("only sends the password when entered or cleared", () => {
    const base = { ...emptyForm(), name: "w", host: "h", username: "u" };
    expect(toMachineDef(base, { editing: "w" }).password).toBeUndefined();
    expect(toMachineDef({ ...base, password: "pw" }, { editing: "w" }).password).toBe("pw");
    expect(toMachineDef({ ...base, clearPassword: true }, { editing: "w" })).toMatchObject({ password: "", password_cmd: "" });
  });
  it("uses new_name when renaming", () => {
    const d = toMachineDef({ ...emptyForm(), name: "new", host: "h", username: "u" }, { editing: "old" });
    expect(d.name).toBe("old");
    expect(d.new_name).toBe("new");
  });
  it("round-trips a config entry", () => {
    const f = formFromConfig({ name: "a", host: "h", username: "u", port: 3390, tags: ["x"], enable_drive: false, enabled: false, security: "tls", password: "keychain:a" });
    expect(f.password).toBe(""); // never echo the password
    const d = toMachineDef(f, { editing: "a" });
    expect(d).toMatchObject({ name: "a", host: "h", port: 3390, enable_drive: false, enabled: false, security: "tls", tags: ["x"] });
    expect(d.password).toBeUndefined();
  });
  it("splits tags on commas and whitespace", () => {
    expect(parseTags("a, b，c  d")).toEqual(["a", "b", "c", "d"]);
  });
});

describe("settings patch", () => {
  it("reads nested values", () => {
    const r = readAll(SERVER_FIELDS, { listen: "x:1", audit: { retention_days: 3, enabled: false } });
    expect(r.listen).toBe("x:1");
    expect(r["audit.retention_days"]).toBe("3");
    expect(r["audit.enabled"]).toBe("false");
    expect(r.log_level).toBe("");
  });
  it("only includes changed keys, with types", () => {
    const before = readAll(SERVER_FIELDS, { screenshot_max_width: 1280, log_level: "info" });
    const after = { ...before, screenshot_max_width: "1600", draw_cursor: "false", log_level: "" };
    expect(buildPatch(SERVER_FIELDS, before, after)).toEqual({ screenshot_max_width: 1600, draw_cursor: false, log_level: null });
  });
  it("validates ints", () => {
    const port = DEFAULT_FIELDS.find((f) => f.key === "port")!;
    expect(validateField(port, "")).toBe(true);
    expect(validateField(port, "3389")).toBe(true);
    expect(validateField(port, "abc")).toBe(false);
  });
});

describe("live view input helpers", () => {
  it("maps mouse buttons", () => {
    expect([0, 1, 2, 3, 4, 9].map(mouseButton)).toEqual(["left", "middle", "right", "x1", "x2", null]);
  });
  it("converts wheel deltas to notches in browser direction", () => {
    expect(wheelNotches(100, 0)).toBe(1);
    expect(wheelNotches(-100, 0)).toBe(-1);
    expect(wheelNotches(3, 0)).toBe(1); // 小滚动至少一格
    expect(wheelNotches(300, 0)).toBe(3);
    expect(wheelNotches(3, 1)).toBe(1); // 行模式
    expect(wheelNotches(0, 0)).toBe(0);
    expect(wheelNotches(100000, 0)).toBe(20);
  });
  it("converts element coordinates to frame coordinates", () => {
    expect(toFrameCoords(400, 225, { w: 800, h: 450 }, { w: 1600, h: 900 })).toEqual({ x: 800, y: 450 });
    expect(toFrameCoords(-5, 9999, { w: 800, h: 450 }, { w: 1600, h: 900 })).toEqual({ x: 0, y: 899 });
    expect(toFrameCoords(1, 1, { w: 0, h: 0 }, { w: 10, h: 10 })).toBeNull();
  });
  it("places the cursor overlay using hotspot and scale", () => {
    // 帧是桌面的 0.5 倍，显示又缩小到 0.5：光标图（32x32 桌面像素，热点 4,4）
    const g = cursorGeometry({ x: 400, y: 200, hot: [4, 4], size: [32, 32] }, 0.5, 0.5);
    expect(g).toEqual({ left: 200 - 1, top: 100 - 1, width: 8, height: 8 });
  });
  it("fits frames into a box", () => {
    expect(fitSize({ w: 1920, h: 1080 }, { w: 960, h: 960 })).toEqual({ w: 960, h: 540 });
    expect(fitSize({ w: 0, h: 0 }, { w: 960, h: 960 })).toEqual({ w: 0, h: 0 });
  });
  it("computes fps over a sliding window", () => {
    const f = new FpsMeter(1000);
    for (let i = 0; i <= 10; i++) f.tick(i * 100); // 10 fps
    expect(f.value(1000)).toBe(10);
    expect(f.value(5000)).toBe(0);
  });
});

describe("log parsing", () => {
  it("parses slog text lines", () => {
    const l = parseLogLine('time=2026-11-04T10:00:00.000+08:00 level=WARN msg="config: bad thing" machine=a');
    expect(l).toMatchObject({ level: "WARN", msg: "config: bad thing", time: "2026-11-04T10:00:00.000+08:00" });
  });
  it("tolerates unstructured lines", () => {
    expect(parseLogLine("hello")).toMatchObject({ level: "", msg: "hello" });
  });
});

describe("activity helpers", () => {
  const recs = [
    { id: "1", time: "t", phase: "end" as const, tool: "mouse", machine: "a", ok: true },
    { id: "2", time: "t", phase: "end" as const, tool: "keyboard", machine: "b", ok: false },
    { id: "3", time: "t", phase: "start" as const, tool: "screenshot", machine: "a" },
  ];
  it("filters by machine, tool and result", () => {
    expect(filterRecords(recs, { machine: "a", tool: "", result: "" }).map((r) => r.id)).toEqual(["1", "3"]);
    expect(filterRecords(recs, { machine: "", tool: "keyboard", result: "" }).map((r) => r.id)).toEqual(["2"]);
    expect(filterRecords(recs, { machine: "", tool: "", result: "fail" }).map((r) => r.id)).toEqual(["2"]);
    expect(filterRecords(recs, { machine: "", tool: "", result: "running" }).map((r) => r.id)).toEqual(["3"]);
    expect(filterRecords(recs, { machine: "", tool: "", result: "ok" }).map((r) => r.id)).toEqual(["1"]);
  });
  it("exports one JSON object per line", () => {
    const out = toJsonl(recs);
    expect(out.trim().split("\n")).toHaveLength(3);
    expect(JSON.parse(out.split("\n")[0]).id).toBe("1");
    expect(toJsonl([])).toBe("");
  });
});

describe("i18n", () => {
  it("has English for every Chinese key and vice versa", () => {
    expect(Object.keys(en).sort()).toEqual(Object.keys(zh).sort());
  });
  it("has no empty strings", () => {
    for (const [k, v] of Object.entries({ ...zh, ...en })) expect(v, k).not.toBe("");
  });
  it("interpolates and falls back", () => {
    expect(translate("zh", "machines.created", { name: "w" })).toBe("已添加 w");
    expect(translate("en", "machines.created", { name: "w" })).toBe("Added w");
    expect(translate("en", "no.such.key")).toBe("no.such.key");
  });
  it("uses the same placeholders in both languages", () => {
    const ph = (s: string) => (s.match(/\{\w+\}/g) ?? []).sort().join(",");
    for (const k of Object.keys(zh)) expect(ph(en[k]), k).toBe(ph(zh[k]));
  });
});
