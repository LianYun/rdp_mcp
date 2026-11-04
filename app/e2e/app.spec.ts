import { expect, test, type Page } from "@playwright/test";
import fs from "node:fs";
import path from "node:path";
import { ROOT, STATE } from "./env";

const st = () => JSON.parse(fs.readFileSync(STATE, "utf8")) as { listen: string; token: string };
const shot = (page: Page, name: string) => page.screenshot({ path: path.join(ROOT, "shots", `${name}.png`) });

async function api<T = unknown>(method: string, p: string, body?: unknown): Promise<{ status: number; data: T }> {
  const { listen, token } = st();
  const res = await fetch(`http://${listen}/api/v1${p}`, {
    method,
    headers: { Authorization: `Bearer ${token}`, ...(body ? { "Content-Type": "application/json" } : {}) },
    body: body ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  let data: unknown = text;
  try {
    data = JSON.parse(text);
  } catch {
    /* 非 JSON */
  }
  return { status: res.status, data: data as T };
}

async function open(page: Page, hash = "/") {
  const { listen, token } = st();
  await page.goto(`/?listen=${encodeURIComponent(listen)}&token=${token}#${hash}`);
}

/** 通过 MCP Streamable HTTP 调一次工具，用于产生审计记录与验证接管时被拒绝。 */
async function mcpCall(tool: string, args: Record<string, unknown>): Promise<{ isError: boolean; text: string }> {
  const { listen } = st();
  const url = `http://${listen}/mcp`;
  const headers: Record<string, string> = { "Content-Type": "application/json", Accept: "application/json, text/event-stream" };
  const parse = async (r: Response) => {
    const t = await r.text();
    const line = t.split("\n").find((l) => l.startsWith("data:"));
    return JSON.parse(line ? line.slice(5) : t);
  };
  const init = await fetch(url, {
    method: "POST",
    headers,
    body: JSON.stringify({
      jsonrpc: "2.0",
      id: 1,
      method: "initialize",
      params: { protocolVersion: "2025-03-26", capabilities: {}, clientInfo: { name: "e2e", version: "0" } },
    }),
  });
  const sid = init.headers.get("Mcp-Session-Id");
  await parse(init);
  const h2 = { ...headers, ...(sid ? { "Mcp-Session-Id": sid } : {}) };
  await fetch(url, { method: "POST", headers: h2, body: JSON.stringify({ jsonrpc: "2.0", method: "notifications/initialized" }) });
  const res = await parse(
    await fetch(url, { method: "POST", headers: h2, body: JSON.stringify({ jsonrpc: "2.0", id: 2, method: "tools/call", params: { name: tool, arguments: args } }) }),
  );
  const content = res.result?.content ?? [];
  return { isError: !!res.result?.isError || !!res.error, text: content.map((c: { text?: string }) => c.text ?? "").join("\n") || JSON.stringify(res.error ?? "") };
}

test.describe.configure({ mode: "serial" });

test("overview loads with service, MCP access and machine cards", async ({ page }) => {
  await open(page);
  await expect(page.getByTestId("service-status")).toHaveText("运行中");
  await expect(page.getByTestId("service-version")).not.toHaveText("-");
  await expect(page.getByTestId("mcp-url")).toContainText("127.0.0.1:18766/mcp");
  await expect(page.getByTestId("snippet-claude-code")).toContainText("claude mcp add --transport http rdp http://127.0.0.1:18766/mcp");
  await expect(page.getByTestId("machine-card-sample")).toBeVisible();
  await expect(page.getByTestId("sidebar-dot")).toHaveAttribute("data-status", "running");
  await shot(page, "01-overview");
});

test("machine create / edit / test connection / delete", async ({ page }) => {
  await open(page, "/machines");
  await expect(page.getByTestId("row-sample")).toBeVisible();

  // 新增（含密码 -> 钥匙串）
  await page.getByTestId("add-machine").click();
  await page.getByTestId("f-name").fill("e2e-pc");
  await page.getByTestId("f-host").fill("127.0.0.1");
  await page.getByTestId("f-port").fill("13390");
  await page.getByTestId("f-username").fill("test");
  await page.getByTestId("f-password").fill("test");
  await page.getByTestId("f-security").selectOption("tls");
  await page.getByTestId("f-tags").fill("e2e, tmp");
  await page.getByTestId("f-description").fill("created by playwright");
  await page.getByTestId("f-save").click();
  await expect(page.getByTestId("row-e2e-pc")).toBeVisible();
  await expect(page.getByTestId("row-e2e-pc")).toContainText("tmp");

  const cfg = await api<{ machines: { name: string; password?: string; port?: number }[] }>("GET", "/config");
  const created = cfg.data.machines.find((m) => m.name === "e2e-pc");
  expect(created?.port).toBe(13390);
  expect(created?.password).toBe("keychain:e2e-pc"); // 明文密码没有进配置文件

  // 校验：名称非法
  await page.getByTestId("add-machine").click();
  await page.getByTestId("f-name").fill("bad name");
  await page.getByTestId("f-save").click();
  await expect(page.getByTestId("machine-form")).toContainText("只能包含字母、数字");
  await page.keyboard.press("Escape");

  // 编辑 + 测试连接（密码留空 = 使用已保存的）
  await page.getByTestId("edit-e2e-pc").click();
  await expect(page.getByTestId("f-description")).toHaveValue("created by playwright");
  await page.getByTestId("f-description").fill("edited");
  await page.getByTestId("f-test").click();
  await expect(page.getByTestId("test-result")).toHaveAttribute("data-ok", "true", { timeout: 60_000 });
  await expect(page.getByTestId("test-screenshot")).toBeVisible();
  await shot(page, "02-test-connection");
  await page.getByTestId("f-save").click();
  await expect(page.getByTestId("row-e2e-pc")).toContainText("edited");

  // 搜索与标签过滤
  await page.getByTestId("machine-search").fill("sample");
  await expect(page.getByTestId("row-e2e-pc")).toHaveCount(0);
  await page.getByTestId("machine-search").fill("");
  await page.getByTestId("tag-filter").selectOption("tmp");
  await expect(page.getByTestId("row-sample")).toHaveCount(0);
  await expect(page.getByTestId("row-e2e-pc")).toBeVisible();
  await page.getByTestId("tag-filter").selectOption("");

  // 启用开关
  const enabledOf = async () => (await api<{ enabled: boolean }>("GET", "/machines/e2e-pc")).data.enabled;
  await page.getByTestId("enabled-e2e-pc").click();
  await expect.poll(enabledOf).toBe(false);
  await expect(page.getByTestId("toaster")).not.toContainText("internal");
  await expect(page.getByTestId("row-e2e-pc")).toBeVisible();
  await page.getByTestId("enabled-e2e-pc").click();
  await expect.poll(enabledOf).toBe(true);

  // 删除（二次确认）
  await page.getByTestId("delete-e2e-pc").click();
  await expect(page.getByTestId("confirm-dialog")).toBeVisible();
  await shot(page, "03-delete-confirm");
  await page.getByTestId("confirm-ok").click();
  await expect(page.getByTestId("row-e2e-pc")).toHaveCount(0);
  expect((await api("GET", "/machines/e2e-pc")).status).toBe(404);
});

test("certificate confirmation dialog on cert_verify", async ({ page }) => {
  const wrongFp = "sha256:" + Array(32).fill("00").join(":");
  const def = { name: "certm", host: "127.0.0.1", port: 13390, username: "test", password_cmd: "echo test", security: "tls", cert: "fingerprint", cert_fingerprint: wrongFp };
  expect((await api("POST", "/machines", def)).status).toBe(201);
  try {
    await open(page, "/machines/certm");
    await page.getByTestId("btn-connect").click();
    await expect(page.getByTestId("cert-dialog")).toBeVisible({ timeout: 30_000 });
    await expect(page.getByTestId("cert-fingerprint")).not.toBeEmpty();
    // 对话框不能被长指纹撑出视口：三个按钮都必须完整可见
    for (const id of ["cert-reject", "cert-once", "cert-always"]) {
      const b = await page.getByTestId(id).boundingBox();
      const dlg = await page.getByTestId("cert-dialog").boundingBox();
      expect(b && dlg && b.x >= dlg.x && b.x + b.width <= dlg.x + dlg.width + 1).toBe(true);
    }
    await shot(page, "04-cert-dialog");
    await page.getByTestId("cert-once").click();
    await expect(page.getByTestId("cert-dialog")).toHaveCount(0);
    await expect(page.getByTestId("state-badge")).toHaveText("已连接", { timeout: 60_000 });
    await page.getByTestId("btn-disconnect").click();
    await expect(page.getByTestId("state-badge")).toHaveText("未连接");
  } finally {
    await api("DELETE", "/machines/certm?delete_secret=true");
  }
});

test("connect, live view frames, take over and hand back", async ({ page }) => {
  await open(page, "/machines/sample");
  await expect(page.getByTestId("detail-name")).toHaveText("sample");
  await page.getByTestId("btn-connect").click();
  await expect(page.getByTestId("state-badge")).toHaveText("已连接", { timeout: 60_000 });

  // 实时画面收到帧
  const canvas = page.getByTestId("live-canvas");
  await expect(canvas).toBeVisible({ timeout: 30_000 });
  await expect.poll(async () => Number((await canvas.getAttribute("data-frames")) ?? 0), { timeout: 30_000 }).toBeGreaterThan(0);
  await expect(page.getByTestId("live-fps")).toContainText("帧率");
  await shot(page, "05-live-view");

  // 只读状态下不能操作
  await expect(page.getByTestId("live-view")).toHaveAttribute("data-takeover", "false");
  await expect(page.getByTestId("btn-cad")).toBeDisabled();

  // 接管
  await page.getByTestId("btn-takeover").click();
  await expect(page.getByTestId("live-view")).toHaveAttribute("data-takeover", "true");
  await expect(page.getByTestId("control-badge")).toHaveText("人工接管");
  expect((await api<{ control_mode: string }>("GET", "/machines/sample")).data.control_mode).toBe("human");

  // 接管期间模型调用被拒绝
  const denied = await mcpCall("mouse", { machine: "sample", action: "move", x: 10, y: 10 });
  expect(denied.isError).toBe(true);

  // 鼠标 / 键盘输入
  const box = (await canvas.boundingBox())!;
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.move(box.x + box.width / 2 + 40, box.y + box.height / 2 + 20, { steps: 4 });
  await page.mouse.down();
  await page.mouse.up();
  await page.mouse.wheel(0, 120);
  await page.getByTestId("live-input").focus();
  await page.keyboard.press("KeyA");
  await expect(page.getByTestId("live-latency")).toContainText(/\d+ ms/, { timeout: 15_000 });
  await expect(page.getByTestId("toaster")).not.toContainText("input ignored");
  await shot(page, "06-takeover");

  // 光标叠加层
  await expect(page.getByTestId("live-cursor")).toBeVisible();

  // 归还
  await page.getByTestId("btn-handback").click();
  await expect(page.getByTestId("live-view")).toHaveAttribute("data-takeover", "false");
  expect((await api<{ control_mode: string }>("GET", "/machines/sample")).data.control_mode).toBe("agent");

  // 暂停
  await page.getByTestId("btn-pause").click();
  await expect(page.getByTestId("control-badge")).toHaveText("已暂停");
  await page.getByTestId("btn-takeover").click();
  await expect(page.getByTestId("control-badge")).toHaveText("人工接管");
  await page.getByTestId("btn-handback").click();
});

test("emergency stop pauses all machines", async ({ page }) => {
  await open(page);
  await page.getByTestId("emergency-stop").click();
  await expect.poll(async () => (await api<{ control_mode: string }>("GET", "/machines/sample")).data.control_mode).toBe("paused");
  await expect(page.getByTestId("machine-card-sample")).toContainText("已暂停");
  await api("PUT", "/machines/sample/control", { mode: "agent" });
  await expect(page.getByTestId("machine-card-sample")).toContainText("模型控制");
});

test("activity timeline shows live tool calls and exports JSONL", async ({ page }) => {
  await open(page, "/activity");
  const r = await mcpCall("list_machines", {});
  expect(r.isError).toBe(false);
  await expect(page.getByTestId("activity-row").first()).toContainText("list_machines", { timeout: 15_000 });
  await page.getByTestId("filter-tool").selectOption("list_machines");
  await expect(page.getByTestId("activity-row").first()).toBeVisible();
  await page.getByTestId("activity-row").first().click();
  await expect(page.getByTestId("activity-detail")).toBeVisible();
  const [dl] = await Promise.all([page.waitForEvent("download"), page.getByTestId("export-jsonl").click()]);
  const text = fs.readFileSync((await dl.path())!, "utf8");
  expect(text.trim().split("\n").map((l) => JSON.parse(l).tool)).toContain("list_machines");
  await shot(page, "07-activity");
});

test("logs page shows service log lines", async ({ page }) => {
  await open(page, "/logs");
  await expect(page.getByTestId("log-view")).toContainText(/mcp started/, { timeout: 15_000 });
  await shot(page, "08-logs");
});

test("settings: server config, theme and language", async ({ page }) => {
  await open(page, "/settings");
  await expect(page.getByTestId("settings-server")).toBeVisible();
  await page.getByTestId("s-screenshot_max_width").fill("1100");
  await page.getByTestId("settings-save").click();
  await expect.poll(async () => (await api<{ server: { screenshot_max_width: number } }>("GET", "/config")).data.server.screenshot_max_width).toBe(1100);

  // 需要重启的项给出提示
  await page.getByTestId("s-listen").fill("127.0.0.1:18766");
  await page.getByTestId("s-screenshot_max_width").fill("");
  await page.getByTestId("settings-save").click();

  // 默认项
  await page.getByRole("tab", { name: "默认项" }).click();
  await expect(page.getByTestId("settings-defaults")).toBeVisible();

  // 高级：非法 YAML 被服务拒绝，错误显示出来
  await page.getByRole("tab", { name: "高级" }).click();
  const raw = page.getByTestId("raw-yaml");
  await expect(raw).toHaveValue(/machines:/);
  const good = await raw.inputValue();
  await raw.fill("machines: [this is: not valid");
  await page.getByTestId("raw-save").click();
  await expect(page.getByTestId("raw-error")).toBeVisible();
  await raw.fill(good + "\n# edited by e2e\n");
  await page.getByTestId("raw-save").click();
  await expect(page.getByTestId("raw-error")).toHaveCount(0);
  expect((await api<string>("GET", "/config/raw")).data).toContain("edited by e2e");

  // 应用：主题与语言
  await page.getByRole("tab", { name: "应用" }).click();
  await page.getByTestId("app-theme").selectOption("dark");
  await expect(page.locator("html")).toHaveClass(/dark/);
  await page.waitForTimeout(400); // 等颜色过渡结束再截图
  await shot(page, "09-settings-dark");
  await page.getByTestId("app-language").selectOption("en");
  await expect(page.getByTestId("nav-overview")).toHaveText("Overview");
  await expect(page.getByTestId("nav-settings")).toHaveText("Settings");
  await shot(page, "10-settings-en");
  await page.getByTestId("app-language").selectOption("zh");
  await page.getByTestId("app-theme").selectOption("light");
  await expect(page.locator("html")).not.toHaveClass(/dark/);
});

test("MCP token rotation updates the displayed token", async ({ page }) => {
  await open(page);
  const before = (await api<{ mcp_token: string }>("GET", "/service/mcp-token")).data.mcp_token;
  await page.getByTestId("mcp-rotate").click();
  await page.getByTestId("confirm-ok").click();
  await expect.poll(async () => (await api<{ mcp_token: string }>("GET", "/service/mcp-token")).data.mcp_token).not.toBe(before);
  await expect(page.getByTestId("snippet-claude-code")).toContainText("claude mcp add");
});
