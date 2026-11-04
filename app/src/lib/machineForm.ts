// 机器编辑表单 <-> 管理 API 的映射（PUT 整体替换语义见 docs/admin-api.md）。

import type { CertPolicy, MachineConfig, MachineDef, Security } from "@/api/types";

export interface MachineForm {
  name: string;
  description: string;
  tags: string; // 逗号分隔
  enabled: boolean;
  host: string;
  port: string;
  username: string;
  domain: string;
  /** 新密码（明文）。编辑时留空表示保留原设置。 */
  password: string;
  clearPassword: boolean;
  width: string;
  height: string;
  security: "" | Security;
  cert: "" | CertPolicy;
  cert_fingerprint: string;
  auto_connect: "" | "true" | "false";
  idle_disconnect: string;
  reconnect_attempts: string;
  enable_drive: "" | "true" | "false";
  enable_clipboard: "" | "true" | "false";
  disable_wallpaper: "" | "true" | "false";
}

export const emptyForm = (): MachineForm => ({
  name: "",
  description: "",
  tags: "",
  enabled: true,
  host: "",
  port: "",
  username: "",
  domain: "",
  password: "",
  clearPassword: false,
  width: "",
  height: "",
  security: "",
  cert: "",
  cert_fingerprint: "",
  auto_connect: "",
  idle_disconnect: "",
  reconnect_attempts: "",
  enable_drive: "",
  enable_clipboard: "",
  disable_wallpaper: "",
});

const tri = (v: boolean | undefined): "" | "true" | "false" => (v === undefined ? "" : v ? "true" : "false");
const str = (v: number | undefined) => (v === undefined ? "" : String(v));

/** 由 GET /config 的机器配置生成表单（密码字段不回显）。 */
export function formFromConfig(m: MachineConfig): MachineForm {
  return {
    ...emptyForm(),
    name: m.name,
    description: m.description ?? "",
    tags: (m.tags ?? []).join(", "),
    enabled: m.enabled !== false,
    host: m.host,
    port: str(m.port),
    username: m.username ?? "",
    domain: m.domain ?? "",
    width: str(m.width),
    height: str(m.height),
    security: m.security ?? "",
    cert: m.cert ?? "",
    cert_fingerprint: m.cert_fingerprint ?? "",
    auto_connect: tri(m.auto_connect),
    idle_disconnect: m.idle_disconnect ?? "",
    reconnect_attempts: str(m.reconnect_attempts),
    enable_drive: tri(m.enable_drive),
    enable_clipboard: tri(m.enable_clipboard),
    disable_wallpaper: tri(m.disable_wallpaper),
  };
}

export function parseTags(s: string): string[] {
  return s
    .split(/[,，\s]+/)
    .map((x) => x.trim())
    .filter(Boolean);
}

export const NAME_RE = /^[A-Za-z0-9_-]+$/;

/** 校验表单，返回 {字段: 错误键}（错误键为 i18n key）。 */
export function validateForm(f: MachineForm): Record<string, string> {
  const e: Record<string, string> = {};
  if (!f.name) e.name = "form.err.name_required";
  else if (!NAME_RE.test(f.name)) e.name = "form.err.name_chars";
  if (!f.host.trim()) e.host = "form.err.host_required";
  if (!f.username.trim()) e.username = "form.err.user_required";
  const intIn = (v: string, lo: number, hi: number) => v === "" || (/^\d+$/.test(v) && Number(v) >= lo && Number(v) <= hi);
  if (!intIn(f.port, 1, 65535)) e.port = "form.err.port";
  if (!intIn(f.width, 200, 8192)) e.width = "form.err.size";
  if (!intIn(f.height, 200, 8192)) e.height = "form.err.size";
  if (!intIn(f.reconnect_attempts, 0, 1000)) e.reconnect_attempts = "form.err.number";
  if (f.idle_disconnect !== "" && f.idle_disconnect !== "0" && !/^(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$/.test(f.idle_disconnect))
    e.idle_disconnect = "form.err.duration";
  if (f.cert === "fingerprint" && !f.cert_fingerprint.trim()) e.cert_fingerprint = "form.err.fingerprint";
  return e;
}

/**
 * 表单 -> POST/PUT body。PUT 是整体替换：空字段不发送（回退 defaults）。
 * 密码：留空且未勾选清除时省略（保留原有设置）；勾选清除发送 `password: ""`。
 */
export function toMachineDef(f: MachineForm, opts: { editing?: string } = {}): MachineDef {
  const d: MachineDef = { name: opts.editing ?? f.name, host: f.host.trim(), username: f.username.trim(), enabled: f.enabled };
  if (opts.editing && f.name !== opts.editing) d.new_name = f.name;
  if (f.description.trim()) d.description = f.description.trim();
  const tags = parseTags(f.tags);
  if (tags.length) d.tags = tags;
  if (f.domain.trim()) d.domain = f.domain.trim();
  if (f.port) d.port = Number(f.port);
  if (f.width) d.width = Number(f.width);
  if (f.height) d.height = Number(f.height);
  if (f.security) d.security = f.security;
  if (f.cert) d.cert = f.cert;
  if (f.cert === "fingerprint" && f.cert_fingerprint.trim()) d.cert_fingerprint = f.cert_fingerprint.trim();
  if (f.auto_connect) d.auto_connect = f.auto_connect === "true";
  if (f.idle_disconnect) d.idle_disconnect = f.idle_disconnect;
  if (f.reconnect_attempts) d.reconnect_attempts = Number(f.reconnect_attempts);
  if (f.enable_drive) d.enable_drive = f.enable_drive === "true";
  if (f.enable_clipboard) d.enable_clipboard = f.enable_clipboard === "true";
  if (f.disable_wallpaper) d.disable_wallpaper = f.disable_wallpaper === "true";
  if (f.clearPassword) {
    d.password = "";
    d.password_cmd = "";
  } else if (f.password) {
    d.password = f.password;
  }
  return d;
}
