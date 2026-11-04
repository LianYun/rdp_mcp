// 设置页：配置视图 <-> PUT /config/server|defaults 的 patch 转换（纯函数，便于单测）。

export type FieldKind = "text" | "int" | "bool" | "select";

export interface FieldSpec {
  /** 点号路径，如 `audit.retention_days`；同时是 PUT 的键。 */
  key: string;
  kind: FieldKind;
  label: string;
  options?: string[];
  placeholder?: string;
  restart?: boolean;
}

export const SERVER_FIELDS: FieldSpec[] = [
  { key: "listen", kind: "text", label: "settings.server.listen", restart: true, placeholder: "127.0.0.1:8765" },
  { key: "screenshot_max_width", kind: "int", label: "settings.server.screenshot_max_width", placeholder: "1280" },
  { key: "draw_cursor", kind: "bool", label: "settings.server.draw_cursor" },
  { key: "log_level", kind: "select", label: "settings.server.log_level", options: ["debug", "info", "warn", "error"] },
  { key: "audit.enabled", kind: "bool", label: "settings.server.audit_enabled" },
  { key: "audit.screenshots", kind: "bool", label: "settings.server.audit_screenshots" },
  { key: "audit.retention_days", kind: "int", label: "settings.server.audit_retention", placeholder: "7" },
  { key: "audit.redact_typed_text", kind: "bool", label: "settings.server.audit_redact" },
];

export const DEFAULT_FIELDS: FieldSpec[] = [
  { key: "port", kind: "int", label: "form.port", placeholder: "3389" },
  { key: "width", kind: "int", label: "form.width", placeholder: "1920" },
  { key: "height", kind: "int", label: "form.height", placeholder: "1080" },
  { key: "security", kind: "select", label: "form.security", options: ["auto", "nla", "tls", "rdp"] },
  { key: "cert", kind: "select", label: "form.cert", options: ["tofu", "ignore", "fingerprint"] },
  { key: "auto_connect", kind: "bool", label: "form.auto_connect" },
  { key: "idle_disconnect", kind: "text", label: "form.idle_disconnect", placeholder: "30m" },
  { key: "reconnect_attempts", kind: "int", label: "form.reconnect_attempts", placeholder: "5" },
  { key: "enable_drive", kind: "bool", label: "form.enable_drive" },
  { key: "enable_clipboard", kind: "bool", label: "form.enable_clipboard" },
  { key: "disable_wallpaper", kind: "bool", label: "form.disable_wallpaper" },
];

/** 按点号路径读取值并转成表单字符串（未设置 -> ""）。 */
export function readValue(obj: unknown, key: string): string {
  let cur: unknown = obj;
  for (const part of key.split(".")) {
    if (cur === null || typeof cur !== "object") return "";
    cur = (cur as Record<string, unknown>)[part];
  }
  if (cur === undefined || cur === null) return "";
  return String(cur);
}

export function readAll(specs: FieldSpec[], obj: unknown): Record<string, string> {
  return Object.fromEntries(specs.map((s) => [s.key, readValue(obj, s.key)]));
}

export function validateField(spec: FieldSpec, v: string): boolean {
  if (v === "") return true;
  if (spec.kind === "int") return /^\d+$/.test(v);
  return true;
}

function convert(spec: FieldSpec, v: string): unknown {
  if (v === "") return null; // null 删除该键，回退默认
  if (spec.kind === "int") return Number(v);
  if (spec.kind === "bool") return v === "true";
  return v;
}

/** 只包含有变化的键；空值表示删除。 */
export function buildPatch(specs: FieldSpec[], before: Record<string, string>, after: Record<string, string>): Record<string, unknown> {
  const patch: Record<string, unknown> = {};
  for (const s of specs) {
    if ((before[s.key] ?? "") !== (after[s.key] ?? "")) patch[s.key] = convert(s, after[s.key] ?? "");
  }
  return patch;
}
