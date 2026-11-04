import type {
  AuditRecord,
  ConfigView,
  ConfigWriteResult,
  Health,
  LogsResponse,
  MachineDef,
  MachineInfo,
  ServiceInfo,
  TestRequest,
  TestResult,
} from "./types";

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

/** 监听地址可能是 0.0.0.0 / [::]，访问时换成回环地址。 */
export function normalizeListen(listen: string): string {
  const i = listen.lastIndexOf(":");
  if (i < 0) return listen;
  const host = listen.slice(0, i);
  const port = listen.slice(i + 1);
  if (host === "" || host === "0.0.0.0" || host === "[::]" || host === "::") return `127.0.0.1:${port}`;
  return listen;
}

export class ApiClient {
  readonly listen: string;
  constructor(
    listen: string,
    readonly token: string,
  ) {
    this.listen = normalizeListen(listen);
  }

  get origin(): string {
    return `http://${this.listen}`;
  }
  get base(): string {
    return `${this.origin}/api/v1`;
  }

  /** 把 `?token=` 附到 URL 上（EventSource / WebSocket / <img> 无法设置请求头）。 */
  withToken(url: string, params: Record<string, string | number | boolean | undefined> = {}): string {
    const u = new URL(url);
    for (const [k, v] of Object.entries(params)) if (v !== undefined) u.searchParams.set(k, String(v));
    u.searchParams.set("token", this.token);
    return u.toString();
  }

  private async raw(method: string, path: string, body?: BodyInit | null, headers: Record<string, string> = {}): Promise<Response> {
    let res: Response;
    try {
      res = await fetch(this.base + path, {
        method,
        body,
        headers: { Authorization: `Bearer ${this.token}`, ...headers },
      });
    } catch (e) {
      throw new ApiError(0, "network", e instanceof Error ? e.message : String(e));
    }
    if (!res.ok) {
      let code = "http_" + res.status;
      let message = res.statusText;
      try {
        const j = await res.json();
        if (j?.error) {
          code = j.error.code ?? code;
          message = j.error.message ?? message;
        }
      } catch {
        /* 非 JSON 错误体 */
      }
      throw new ApiError(res.status, code, message);
    }
    return res;
  }

  private async json<T>(method: string, path: string, body?: unknown): Promise<T> {
    const res = await this.raw(
      method,
      path,
      body === undefined ? undefined : JSON.stringify(body),
      body === undefined ? {} : { "Content-Type": "application/json" },
    );
    const text = await res.text();
    return (text ? JSON.parse(text) : undefined) as T;
  }

  // ---- 服务
  health = () => this.json<Health>("GET", "/health");
  service = () => this.json<ServiceInfo>("GET", "/service");
  shutdown = () => this.json<{ status: string }>("POST", "/service/shutdown");
  mcpToken = () => this.json<{ mcp_token: string }>("GET", "/service/mcp-token");
  rotateMcpToken = () => this.json<{ mcp_token: string }>("POST", "/service/mcp-token/rotate");

  // ---- 机器
  machines = () => this.json<MachineInfo[]>("GET", "/machines");
  machine = (name: string) => this.json<MachineInfo>("GET", `/machines/${encodeURIComponent(name)}`);
  createMachine = (def: MachineDef) => this.json<MachineInfo>("POST", "/machines", def);
  updateMachine = (name: string, def: MachineDef) => this.json<MachineInfo>("PUT", `/machines/${encodeURIComponent(name)}`, def);
  deleteMachine = (name: string, deleteSecret = false) =>
    this.json<{ status: string }>("DELETE", `/machines/${encodeURIComponent(name)}${deleteSecret ? "?delete_secret=true" : ""}`);
  putSecret = (name: string, password: string) =>
    this.json<unknown>("PUT", `/machines/${encodeURIComponent(name)}/secret`, { password });
  testMachine = (name: string, req: TestRequest = {}) =>
    this.json<TestResult>("POST", `/machines/${encodeURIComponent(name)}/test`, req);
  connect = (name: string) => this.json<MachineInfo>("POST", `/machines/${encodeURIComponent(name)}/connect`);
  disconnect = (name: string) => this.json<MachineInfo>("POST", `/machines/${encodeURIComponent(name)}/disconnect`);
  reconnect = (name: string) => this.json<MachineInfo>("POST", `/machines/${encodeURIComponent(name)}/reconnect`);
  setControl = (name: string, mode: "agent" | "human" | "paused") =>
    this.json<MachineInfo>("PUT", `/machines/${encodeURIComponent(name)}/control`, { mode });
  emergencyStop = () => this.json<{ status: string }>("POST", "/control/emergency-stop");
  answerCert = (name: string, accept: "once" | "always" | "reject") =>
    this.json<unknown>("POST", `/machines/${encodeURIComponent(name)}/cert`, { accept });
  trust = (name: string) => this.json<unknown>("POST", `/machines/${encodeURIComponent(name)}/trust`);

  /** 单张截图；`X-Screen-Size` 头给出桌面尺寸。 */
  async screenshot(name: string, opts: { maxWidth?: number; format?: "png" | "jpeg" } = {}): Promise<{ blob: Blob; screen?: [number, number] }> {
    const q = new URLSearchParams();
    if (opts.maxWidth) q.set("max_width", String(opts.maxWidth));
    if (opts.format) q.set("format", opts.format);
    const res = await this.raw("GET", `/machines/${encodeURIComponent(name)}/screenshot${q.size ? "?" + q : ""}`);
    const sz = res.headers.get("X-Screen-Size")?.match(/^(\d+)x(\d+)$/);
    return { blob: await res.blob(), screen: sz ? [Number(sz[1]), Number(sz[2])] : undefined };
  }

  // ---- 配置
  config = () => this.json<ConfigView>("GET", "/config");
  putServer = (patch: Record<string, unknown>) => this.json<ConfigWriteResult>("PUT", "/config/server", patch);
  putDefaults = (patch: Record<string, unknown>) => this.json<ConfigWriteResult>("PUT", "/config/defaults", patch);
  async configRaw(): Promise<string> {
    return (await this.raw("GET", "/config/raw")).text();
  }
  async putConfigRaw(yaml: string): Promise<void> {
    await this.raw("PUT", "/config/raw", yaml, { "Content-Type": "application/yaml" });
  }

  // ---- 审计与日志
  audit = (q: { machine?: string; tool?: string; limit?: number } = {}) => {
    const p = new URLSearchParams();
    if (q.machine) p.set("machine", q.machine);
    if (q.tool) p.set("tool", q.tool);
    if (q.limit) p.set("limit", String(q.limit));
    return this.json<AuditRecord[]>("GET", `/audit${p.size ? "?" + p : ""}`);
  };
  logs = (q: { level?: string; limit?: number; since?: string } = {}) => {
    const p = new URLSearchParams();
    if (q.level) p.set("level", q.level);
    if (q.limit) p.set("limit", String(q.limit));
    if (q.since) p.set("since", q.since);
    return this.json<LogsResponse>("GET", `/logs${p.size ? "?" + p : ""}`);
  };
  /** 审计缩略图 URL（thumb 为服务返回的路径 `/api/v1/audit/thumbs/<id>.jpg`）。 */
  thumbUrl(thumb: string): string {
    return this.withToken(this.origin + thumb);
  }
}
