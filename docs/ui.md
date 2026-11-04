# 桌面 UI（Tauri）

## 1. 定位

Remoter 桌面应用是 remoter-mcp 服务的管理客户端：管理机器、启停服务、实时查看与人工接管、查看模型操作记录。服务不依赖 UI，UI 也可以管理一个由 CLI/launchd/systemd 启动的服务。

## 2. 技术栈

| 层 | 选型 |
|---|---|
| 壳 | Tauri 2（macOS WKWebView、Linux WebKitGTK） |
| 前端 | React 18 + TypeScript + Vite |
| UI 组件 | Tailwind CSS + shadcn/ui，图标 lucide |
| 数据 | TanStack Query（REST）、原生 EventSource（SSE）、WebSocket（实时画面） |
| 状态 | Zustand |
| 路由 | React Router |
| Tauri 插件 | `autostart`、`notification`、`dialog`、`clipboard-manager`、`single-instance`（仅 Rust）、`opener`。sidecar 由 Rust 侧用标准库 `Command` 启动（需要 `setsid` 脱离 UI，`shell` 插件做不到），因此不引入 `shell` 插件 |
| 测试 | Vitest（API 客户端、事件归约、纯函数）、Playwright（Vite dev server + 真实测试服务的端到端） |

## 3. Rust 侧职责（保持精简）

| Tauri command | 说明 |
|---|---|
| `service_discover` | 读 `<data_dir>/run/service.json`（`data_dir` 默认 `~/.remoter`，环境变量 `REMOTER_MCP_DATA_DIR` 可覆盖）并做健康检查，返回 `{running, listen, admin_token, managed_by, pid, version, config_path, data_dir, error?, manager?}`（`manager` 为停止状态下检测到的 launchd/systemd 服务） |
| `service_start` | 已运行则直接附着；有 launchd/systemd 服务则通过它启动；否则以 sidecar 启动 `remoter-mcp serve --managed-by ui-sidecar --worker <内置 remoter-worker 路径>`，进程脱离 UI（`setsid`），等待 `service.json` 出现且健康检查通过 |
| `service_stop` | `managed_by` 为 launchd/systemd 时走 `launchctl bootout` / `systemctl --user stop`；否则优先 `POST /service/shutdown`，8 秒后发 SIGTERM，再 5 秒后 SIGKILL（并清理残留的 `service.json`） |
| `service_restart` | stop + start |
| `service_logs_path` | 返回日志目录 |
| `service_open_logs` | 在 Finder/文件管理器中打开日志目录（`open` / `xdg-open`） |
| `app_settings_get/set` | UI 自身偏好（自动启动服务、退出后服务继续运行、关闭到托盘、语言），存 `app_config_dir/settings.json`；**不是**服务配置，服务配置只走管理 API |
| `app_info` | 应用版本、数据目录、是否找到内置 sidecar |
| `save_file` | 保存截图等二进制文件（目标路径来自 `dialog` 插件的保存对话框） |
| `launchd/systemd` 适配 | 检测 `managed_by` 为 launchd/systemd 时，用 `launchctl kickstart/bootout` 或 `systemctl --user start/stop`；检测停止状态下是否安装了 `~/Library/LaunchAgents/com.lianyun.remoter-mcp.plist` / `~/.config/systemd/user/remoter-mcp.service` |

其他：系统托盘（见下）；单实例；开机自启（`autostart` 插件，`--minimized` 启动时隐藏主窗口）；关闭主窗口最小化到托盘；退出 UI 时若“服务随 UI 退出”则停止由 UI 自己启动（`managed_by=ui-sidecar`）的服务；后台线程每 3 秒刷新托盘，服务意外退出（非用户操作）时发系统通知并把托盘灯变红。

**托盘**：圆点图标表示状态（绿＝运行中；黄＝有机器失败或被暂停；灰＝已停止；红＝异常退出），提示文字含已连接机器数；菜单：状态行、打开主窗口、启动/停止/重启服务、紧急停止（全部暂停）、退出。语言跟随应用设置。

**浏览器模式**：前端同时可以在普通浏览器里运行（`pnpm dev`、Playwright）。此时没有 Rust 侧：服务地址与 token 来自 URL 参数 `?listen=127.0.0.1:8765&token=…`（保存在 localStorage）或 `VITE_REMOTER_LISTEN` / `VITE_REMOTER_TOKEN`，只能附着，不能启停进程；应用设置存 localStorage。

## 4. 页面

### 4.1 概览
- 服务状态卡片：运行中/已停止/异常，版本、PID、运行时长、运行方式；启动/停止/重启按钮。
- MCP 接入：MCP 地址、token（遮罩 + 复制 + 重新生成），一键复制接入片段：
  - Claude Code：`claude mcp add --transport http rdp http://127.0.0.1:8765/mcp --header "Authorization: Bearer <token>"`
  - Claude Desktop / Cursor 等的 JSON 片段
- 机器总览：每台机器的缩略图卡片（状态灯、控制模式、最近一次操作）。
- 全局"紧急停止"按钮。

### 4.2 机器列表 / 编辑
- 表格：名称、地址、标签、状态、控制模式、启用开关（行内切换，PUT 整体替换时用现有配置重建 body）；搜索（名称/地址/说明/用户名）与按标签过滤；删除前二次确认，可勾选“同时删除钥匙串中的密码”（默认勾选）。
- 新增/编辑表单：基础（名称、描述、标签、主机、端口、用户名、域、启用）、密码（明文输入 → 服务写入钥匙串，配置只留 `keychain:<name>`；编辑时留空＝保持不变，可勾选“清除”）、显示（分辨率）、安全（协议、证书策略/指纹）、高级（自动连接、空闲断开、重连次数、共享目录、剪贴板、关闭壁纸）。所有字段留空＝回退到 `defaults`。表单本地校验（名称 `[A-Za-z0-9_-]`、端口 1–65535、分辨率 200–8192、时长格式）。
- "测试连接"按钮：用表单当前值（密码留空则用已保存的）调用 `POST /machines/{name}/test`，显示结果、FreeRDP 版本与截图；不影响该机器的现有会话。新增时需先保存（接口以已有机器名为路径）。
- 导入 `.rdp` 文件（解析 `full address`（含端口/IPv6）、`username`（`DOMAIN\user`）、`domain`、`desktopwidth/height`、`enablecredsspsupport`）：预填表单，由用户确认后保存。

### 4.3 机器详情
- 实时画面（canvas 渲染 JPEG 帧 + 叠加光标），自适应缩放，显示帧率（滑动窗口）与延迟（接管时：发送输入到下一帧到达的间隔）。WebSocket 以 `connect=false` 打开，是否连接由“连接”按钮决定；断开后自动重试。
- 控制条：接管 / 归还给模型 / 暂停；连接 / 断开 / 重连；分辨率切换；发送 Ctrl+Alt+Del；截图保存；剪贴板发送。
  - 分辨率切换：管理 API 没有“动态改分辨率”接口，因此实现为 `PUT /machines/{name}`（只改 `width`/`height`）+ `POST /reconnect`（会断开当前会话，先确认）。
  - Ctrl+Alt+Del、剪贴板发送经实时画面 WebSocket 发送（`key` / `clipboard` 消息），仅接管模式可用。
  - 截图保存：`GET /machines/{name}/screenshot?format=png`（原始分辨率），Tauri 下弹出保存对话框。
- 接管模式下画面边框高亮并隐藏本地光标（远端光标由叠加层显示）；键鼠事件经 WebSocket 发送：鼠标坐标换算为帧坐标；滚轮按“格”（像素模式 100px/格，行模式 3 行/格）、方向同浏览器；键盘用 `KeyboardEvent.code`，失焦/退出接管时松开所有按下的键；输入法组合输入在 `compositionend` 时作为整段 `text` 发送，另有一个文本框可整段发送文字（Enter）。
- 侧栏：该机器的最近工具调用时间线（审计记录 + 实时 `tool_call` 事件）。

### 4.4 活动记录
- 所有模型工具调用的时间线：时间、机器、工具、参数摘要、耗时、结果；点击展开查看参数、摘要与结果截图缩略图（`/audit/thumbs/…`）。
- 数据来源：`GET /audit`（最近 300 条）合并实时 `tool_call` 事件（`start` 显示“进行中”，`end` 覆盖之）。
- 按机器/工具/结果（成功/失败/进行中）过滤；导出当前筛选结果为 JSONL。

### 4.5 日志
- 服务日志：每 2 秒拉取 `GET /logs`（末尾 500 行）+ 级别过滤 + 自动滚动；打开日志目录。

### 4.6 设置
- 服务：监听地址、截图最大宽度、绘制光标、日志级别、审计开关/截图缩略图/保留天数/隐藏输入文本（`PUT /config/server`，只发送有变化的键，清空＝删除该键；返回 `restart_required` 时提示并可一键重启）；MCP token 在概览页管理。
- 默认项：同配置 `defaults`（`PUT /config/defaults`）。
- 应用：开机自启、启动时自动启动服务、关闭 UI 时服务是否继续运行、关闭窗口最小化到托盘、主题（浅色/深色/跟随系统）、语言（中文/英文，默认中文）。
- 高级：原始 YAML 编辑器（`GET/PUT /config/raw`，服务端校验，失败时显示错误且不写入）。

## 5. 交互约定

- 证书确认（`cert_verify` 事件 / `pending_cert`）：弹出对话框显示指纹、主题、颁发者与原因，选"仅本次/始终信任/拒绝"；2 分钟未响应服务端自动拒绝。对话框不可点外部关闭。
- 机器连接失败、服务异常退出：系统通知 + 托盘图标变色。
- 所有破坏性操作（删除机器、停止服务、重新生成 token、改分辨率）需二次确认；紧急停止必须一键生效，不确认。
- 实时状态以 SSE `snapshot` 为基线，之后按事件增量更新（`machine_state` / `control_mode` / `tool_call` / `cert_verify` / `config_changed` …）；服务重启或重新附着后重新订阅。

## 6. 目录结构

```
app/
  package.json  vite.config.ts  tsconfig.json  index.html  playwright.config.ts
  src/
    main.tsx  App.tsx
    api/          # client.ts（REST）、events.ts（SSE）、stream.ts（实时画面 WS）、types.ts（与 admin-api.md 对齐）
    stores/       # live.ts（服务发现 + SSE 状态）、reduce.ts（事件归约，纯函数）、prefs.ts、toast.ts
    pages/        # Overview, Machines, MachineDetail, Activity, Logs, Settings
    components/   # LiveView, MachineForm, ServiceCard, McpAccessCard, CertDialog, ui/（shadcn 风格基础组件）
    lib/          # tauri.ts（桥接）、service.ts、snippets.ts、rdpfile.ts、machineForm.ts、input.ts …
    i18n/         # zh.ts（默认与回退）、en.ts
  tests/          # Vitest
  e2e/            # Playwright（global-setup 启动示例服务器 + 真实 remoter-mcp）
  src-tauri/
    Cargo.toml  tauri.conf.json  capabilities/
    src/ main.rs  service.rs  tray.rs  settings.rs
    binaries/     # remoter-mcp-<triple>、remoter-worker-<triple>（构建生成，不入库）
    frameworks/   # macOS 打包的 dylib（构建生成，不入库）
```

## 7. 开发与测试

```bash
make app-sidecars          # 先准备 sidecar（tauri-build 需要 binaries/）
cd app && pnpm install
pnpm dev                   # 浏览器模式：http://127.0.0.1:1420/?listen=127.0.0.1:8765&token=<admin_token>
pnpm tauri dev             # 桌面应用
pnpm build                 # tsc + vite
pnpm test                  # Vitest
pnpm test:e2e              # Playwright：自动启动 FreeRDP 示例服务器(13390) 与 remoter-mcp(18766)，数据目录 /tmp/rdptest-ui
cd src-tauri && cargo check && cargo clippy && cargo test
REMOTER_MCP_DATA_DIR=/tmp/x ./target/debug/remoter   # 用测试数据目录运行桌面应用
```
