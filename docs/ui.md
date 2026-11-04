# 桌面 UI（Tauri）

## 1. 定位

`rdp_mcp Desktop` 是 rdp-mcp 服务的管理客户端：管理机器、启停服务、实时查看与人工接管、查看模型操作记录。服务不依赖 UI，UI 也可以管理一个由 CLI/launchd/systemd 启动的服务。

## 2. 技术栈

| 层 | 选型 |
|---|---|
| 壳 | Tauri 2（macOS WKWebView、Linux WebKitGTK） |
| 前端 | React 18 + TypeScript + Vite |
| UI 组件 | Tailwind CSS + shadcn/ui，图标 lucide |
| 数据 | TanStack Query（REST）、原生 EventSource（SSE）、WebSocket（实时画面） |
| 状态 | Zustand |
| 路由 | React Router |
| Tauri 插件 | `shell`（sidecar）、`autostart`、`notification`、`dialog`、`clipboard-manager`、`single-instance`、`opener` |

## 3. Rust 侧职责（保持精简）

| Tauri command | 说明 |
|---|---|
| `service_discover` | 读 `~/.rdp_mcp/run/service.json` 并做健康检查，返回 `{running, listen, admin_token, managed_by}` |
| `service_start` | 以 sidecar 启动 `rdp-mcp serve --worker <内置 rdp-worker 路径>`，进程脱离 UI（`setsid`），等待 `service.json` 出现且健康检查通过 |
| `service_stop` | 优先 `POST /service/shutdown`，超时后发 SIGTERM，再超时 SIGKILL |
| `service_restart` | stop + start |
| `service_logs_path` | 返回日志目录，便于"在 Finder/文件管理器中打开" |
| `launchd/systemd` 适配 | 检测 `managed_by` 为 launchd/systemd 时，用 `launchctl kickstart/bootout` 或 `systemctl --user restart/stop` |

其他：系统托盘（服务状态灯、已连接机器数、打开主窗口、启动/停止/重启服务、紧急停止、退出）；单实例；开机自启；关闭主窗口最小化到托盘。

## 4. 页面

### 4.1 概览
- 服务状态卡片：运行中/已停止/异常，版本、PID、运行时长、运行方式；启动/停止/重启按钮。
- MCP 接入：MCP 地址、token（遮罩 + 复制 + 重新生成），一键复制接入片段：
  - Claude Code：`claude mcp add --transport http rdp http://127.0.0.1:8765/mcp --header "Authorization: Bearer <token>"`
  - Claude Desktop / Cursor 等的 JSON 片段
- 机器总览：每台机器的缩略图卡片（状态灯、控制模式、最近一次操作）。
- 全局"紧急停止"按钮。

### 4.2 机器列表 / 编辑
- 表格：名称、地址、标签、状态、控制模式、启用开关；搜索与按标签过滤。
- 新增/编辑表单：基础（名称、描述、标签、主机、端口、用户名、域）、密码（写入钥匙串）、显示（分辨率）、安全（协议、证书策略/指纹）、高级（自动连接、空闲断开、剪贴板、共享目录、重连次数）。
- "测试连接"按钮：显示结果与截图。
- 导入 `.rdp` 文件（解析 `full address`、`username` 等常用字段）。

### 4.3 机器详情
- 实时画面（canvas 渲染 JPEG 帧 + 叠加光标），自适应缩放，显示帧率与延迟。
- 控制条：接管 / 归还给模型 / 暂停；连接 / 断开 / 重连；分辨率切换；发送 Ctrl+Alt+Del；截图保存；剪贴板发送。
- 接管模式下画面边框高亮，键鼠事件经 WebSocket 发送；支持输入法提交文本。
- 侧栏：该机器的最近工具调用时间线。

### 4.4 活动记录
- 所有模型工具调用的时间线：时间、机器、工具、参数摘要、耗时、结果；点击展开查看前后截图。
- 按机器/工具/结果过滤；导出 JSONL。

### 4.5 日志
- 服务日志实时流 + 级别过滤；打开日志目录。

### 4.6 设置
- 服务：监听地址、MCP token、截图尺寸、日志级别、审计截图保留天数（需重启的项给出提示）。
- 默认项：同配置 `defaults`。
- 应用：开机自启、启动时自动启动服务、关闭 UI 时服务是否继续运行、主题（浅色/深色/跟随系统）、语言（中文/英文）。
- 高级：原始 YAML 编辑器（带校验）。

## 5. 交互约定

- 证书确认（`cert_verify` 事件）：弹出对话框显示指纹，选"仅本次/始终信任/拒绝"。
- 机器连接失败、服务异常退出：系统通知 + 托盘图标变色。
- 所有破坏性操作（删除机器、停止服务、紧急停止）需二次确认，紧急停止除外（必须一键生效）。

## 6. 目录结构

```
app/
  package.json  vite.config.ts  tsconfig.json  index.html
  src/
    main.tsx  App.tsx
    api/          # REST 客户端、SSE、WS 封装、类型（与 admin-api.md 对齐）
    stores/       # Zustand
    pages/        # Overview, Machines, MachineDetail, Activity, Logs, Settings
    components/   # LiveView, MachineForm, ServiceCard, ...
    i18n/
  src-tauri/
    Cargo.toml  tauri.conf.json  capabilities/
    src/ main.rs  service.rs  tray.rs
    binaries/     # rdp-mcp-<triple>、rdp-worker-<triple>（构建生成，不入库）
```
