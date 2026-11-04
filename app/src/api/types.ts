// 与 docs/admin-api.md 对齐的类型定义。

export type MachineState = "idle" | "connecting" | "connected" | "reconnecting" | "failed";
export type ControlMode = "agent" | "human" | "paused";
export type ManagedBy = "cli" | "ui-sidecar" | "launchd" | "systemd" | string;

export interface PendingCert {
  host: string;
  port: number;
  fingerprint: string;
  subject: string;
  issuer: string;
  reason: string;
}

export interface MachineInfo {
  name: string;
  description?: string;
  tags?: string[];
  host: string;
  port: number;
  username: string;
  enabled: boolean;
  state: MachineState;
  control_mode: ControlMode;
  width?: number;
  height?: number;
  last_error?: string;
  last_activity?: string;
  connected_at?: string;
  viewers: number;
  pending_cert?: PendingCert;
}

export interface ServiceInfo {
  version: string;
  pid: number;
  listen: string;
  mcp_url: string;
  config_path: string;
  data_dir: string;
  managed_by: ManagedBy;
  started_at: string;
  worker_path: string;
}

export interface Health {
  status: string;
  version: string;
  uptime_s: number;
}

export type Security = "auto" | "nla" | "tls" | "rdp";
export type CertPolicy = "tofu" | "ignore" | "fingerprint";

/** POST / PUT /machines 的 body（PUT 为整体替换）。 */
export interface MachineDef {
  name: string;
  new_name?: string;
  description?: string;
  tags?: string[];
  enabled?: boolean;
  host: string;
  port?: number;
  username: string;
  domain?: string;
  password?: string;
  password_cmd?: string;
  width?: number;
  height?: number;
  security?: Security;
  cert?: CertPolicy;
  cert_fingerprint?: string;
  auto_connect?: boolean;
  idle_disconnect?: string;
  reconnect_attempts?: number;
  enable_drive?: boolean;
  enable_clipboard?: boolean;
  disable_wallpaper?: boolean;
}

/** GET /config 的 machines[]（密码字段为 *** 或引用）。 */
export interface MachineConfig {
  name: string;
  description?: string;
  tags?: string[];
  enabled?: boolean;
  host: string;
  username?: string;
  domain?: string;
  password?: string;
  password_cmd?: string;
  cert_fingerprint?: string;
  port?: number;
  width?: number;
  height?: number;
  security?: Security;
  cert?: CertPolicy;
  auto_connect?: boolean;
  idle_disconnect?: string;
  reconnect_attempts?: number;
  enable_drive?: boolean;
  enable_clipboard?: boolean;
  disable_wallpaper?: boolean;
}

export interface ServerConfig {
  listen: string;
  mcp_token_set: boolean;
  admin_token_set: boolean;
  cors_origins: string[] | null;
  screenshot_max_width: number;
  draw_cursor?: boolean;
  data_dir: string;
  worker_path: string;
  log_level: string;
  audit: { enabled?: boolean; screenshots?: boolean; retention_days?: number; redact_typed_text?: boolean };
  ocr: { enabled?: boolean; tesseract?: string; languages?: string };
}

export type DefaultsConfig = Omit<MachineConfig, "name" | "host" | "username" | "description" | "tags" | "enabled" | "domain" | "password" | "password_cmd" | "cert_fingerprint">;

export interface ConfigView {
  path: string;
  server: ServerConfig;
  defaults: DefaultsConfig;
  machines: MachineConfig[] | null;
}

export interface ConfigWriteResult {
  status: string;
  restart_required: boolean;
}

export interface TestRequest {
  host?: string;
  port?: number;
  username?: string;
  password?: string;
  security?: Security;
  width?: number;
  height?: number;
}

export interface TestResult {
  ok: boolean;
  error?: string;
  width?: number;
  height?: number;
  freerdp?: string;
  screenshot?: string;
}

export interface AuditRecord {
  id: string;
  time: string;
  phase: "start" | "end";
  tool: string;
  machine?: string;
  args?: unknown;
  duration_ms?: number;
  ok?: boolean;
  summary?: string;
  thumb?: string;
}

export interface LogsResponse {
  file: string;
  lines: string[];
}

export interface CertVerifyEvent {
  machine: string;
  host: string;
  port: number;
  fingerprint: string;
  subject: string;
  issuer: string;
  reason: string;
  pending: boolean;
}

// SSE 事件
export interface SnapshotEvent {
  machines: MachineInfo[];
}
export interface MachineStateEvent {
  machine: string;
  state: MachineState;
  error?: string;
  /** 服务端发送 [width, height]；也兼容 "WxH" 字符串。 */
  resolution?: [number, number] | string;
}
export interface ControlModeEvent {
  machine: string;
  mode: ControlMode;
}
export type ToolCallEvent = AuditRecord;
export interface ServiceEvent {
  status: "started" | "stopping";
  version?: string;
}
export interface LogEvent {
  level: string;
  msg: string;
}

export type ServerEvent =
  | { type: "snapshot"; data: SnapshotEvent }
  | { type: "machine_state"; data: MachineStateEvent }
  | { type: "control_mode"; data: ControlModeEvent }
  | { type: "tool_call"; data: ToolCallEvent }
  | { type: "cert_verify"; data: CertVerifyEvent }
  | { type: "config_changed"; data: { machines?: number; source?: string } }
  | { type: "config_error"; data: { error: string } }
  | { type: "service"; data: ServiceEvent }
  | { type: "log"; data: LogEvent };

// 实时画面 WebSocket 消息
export type StreamMessage =
  | { type: "meta"; width: number; height: number; scale: number; screen: [number, number] }
  | { type: "cursor"; x: number; y: number; visible: boolean; shape?: string; hot?: [number, number]; size?: [number, number] }
  | { type: "control"; mode: ControlMode }
  | { type: "state"; state: MachineState; error?: string }
  | ({ type: "cert_verify" } & Partial<CertVerifyEvent>)
  | { type: "error"; message: string };

export type MouseButton = "left" | "right" | "middle" | "x1" | "x2";

export type StreamInput =
  | { type: "mouse"; action: "move" | "down" | "up"; x: number; y: number; button?: MouseButton }
  | { type: "mouse"; action: "wheel"; x: number; y: number; dy?: number; dx?: number }
  | { type: "key"; code: string; down: boolean }
  | { type: "text"; text: string }
  | { type: "clipboard"; text: string };

/** Rust 侧 service_discover 的返回。 */
export interface Discovery {
  running: boolean;
  listen?: string;
  admin_token?: string;
  managed_by?: ManagedBy;
  pid?: number;
  version?: string;
  config_path?: string;
  data_dir: string;
  /** 进程在但健康检查失败时的说明。 */
  error?: string;
  /** 停止状态下检测到的外部管理器（launchd / systemd）。 */
  manager?: string;
}
