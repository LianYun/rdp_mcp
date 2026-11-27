# Remoter 技术方案

> 状态：v0.6（2026-11-13）。本文档随实现持续更新，架构决策变化先改这里（§3 决策表 + §14 变更记录）。
>
> 命名（D20）：产品与桌面应用显示名 `Remoter`（`Remoter.app`）；技术名称一律小写：bundle id `com.lianyun.remoter`；服务 / CLI `remoter-mcp`；worker `remoter-worker`；数据目录 `~/.remoter`；环境变量 `REMOTER_MCP_*`。
>
> 子文档：[MCP 工具规范](mcp-tools.md) · [管理 API](admin-api.md) · [桌面应用](ui.md) · [配置说明](configuration.md) · [Worker IPC 协议](worker-ipc.md) · [构建与发布](packaging.md) · [路线图](roadmap.md)

---

## 1. 目标与范围

基于 FreeRDP 实现一个 MCP 服务，让大模型通过 MCP 控制多台 Windows 电脑，并提供桌面管理界面。macOS 被控端经系统自带的屏幕共享（VNC）+ 远程登录（SSH）接入（D21，§5a）。

| 目标 | 说明 |
|---|---|
| 功能全面 | 截图、鼠标、键盘、剪贴板、分辨率、命令执行、文件传输、会话管理 |
| 接口简单 | 约 9 个 MCP 工具，统一 `machine + action` 风格 |
| HTTP 传输 | MCP Streamable HTTP，端点 `/mcp` |
| 多机器 | `.remoter/configs.yaml` 声明，或在 UI 中管理；Windows（RDP）与 macOS（VNC + SSH）可混用 |
| 部署平台 | 服务：macOS、Linux（含无显示器服务器 / Docker）；UI：macOS、Linux 桌面 |
| 桌面 UI | Tauri 应用：管理机器、启停/重启服务、实时查看与人工接管、查看模型操作记录 |
| 可独立运行 | 服务不依赖 UI，CLI 即可运行；UI 只是服务的一个管理客户端 |

非目标（当前阶段）：Windows 上部署服务、音频、USB/打印机/智能卡重定向、多显示器、远端安装 agent。

## 2. 可行性依据（FreeRDP 3.x）

参考源码：FreeRDP `ec31c9fce`（3.x 主线）；macOS Homebrew 3.33.0。

| 能力 | FreeRDP API | 源码位置 |
|---|---|---|
| 无界面客户端骨架 | 连接 + 事件循环 + `gdi_init` | `client/Sample/tf_freerdp.c` |
| 读取屏幕 | `gdi_init(instance, PIXEL_FORMAT_BGRA32)` → `gdi->primary_buffer`；`update->EndPaint` 提供脏区域 | `include/freerdp/gdi/gdi.h` |
| 鼠标指针形状/位置 | `rdpPointer` 回调（`New/Set/SetPosition/SetNull/SetDefault`） | `include/freerdp/graphics.h` |
| 鼠标输入 | `freerdp_input_send_mouse_event` / `_extended_mouse_event`；滚轮 `PTR_FLAGS_WHEEL` / `PTR_FLAGS_HWHEEL` | `include/freerdp/input.h` |
| 键盘输入 | `freerdp_input_send_unicode_keyboard_event`（任意文本）；`freerdp_input_send_keyboard_event_ex`（扫描码） | `include/freerdp/input.h` |
| 键名映射 | `GetVirtualKeyCodeFromName`、`GetVirtualScanCodeFromVirtualKeyCode` | `winpr/include/winpr/input.h` |
| 剪贴板 | cliprdr 通道 | `include/freerdp/client/cliprdr.h` |
| 动态分辨率 | disp 通道 `SendMonitorLayout` | `include/freerdp/client/disp.h` |
| 文件共享 | rdpdr + drive 通道（本地目录 → `\\tsclient\<name>`） | `channels/drive/client` |
| 启动远程程序 | rail（RemoteApp）通道 | `channels/rail/client` |
| 认证/安全/重连 | NLA/TLS/RDP、证书回调、auto-reconnect | `libfreerdp/core` |

结论：所需能力均有现成 API；GDI 软件渲染不依赖 X11/Cocoa，可在 macOS/Linux/容器中无头运行。

## 3. 关键决策（ADR 摘要）

| # | 决策 | 理由 | 放弃的备选 |
|---|---|---|---|
| D1 | 服务用 **Go** | 单二进制；官方 Go MCP SDK 支持 Streamable HTTP；适合多会话并发 | Python（部署依赖重）、Rust（开发慢） |
| D2 | RDP 用 **C worker 独立进程**，每台机器一个 | 崩溃隔离；C 代码最小化；服务无需 cgo | cgo 内嵌（单点崩溃、交叉编译麻烦） |
| D3 | 截图编码在 Go 侧 | C 侧只传原始像素 | worker 内编码 |
| D4 | 不用纯 Go/Python RDP 库、不用 xfreerdp+Xvfb+xdotool | 成熟度不足 / macOS 不可用且脆弱 | — |
| D5 | 模型坐标 = 截图坐标，服务端换算 | 避免模型坐标错位 | — |
| D6 | `run_command` 走 drive 映射 + 键盘启动脚本 | RDP 无执行命令能力；Windows 端零安装 | 远端 agent、rdp2tcp |
| D7 | MCP 工具 `machine + action` 风格，约 9 个 | 简单 | 每个动作一个工具 |
| D8 | 动作类工具默认附带操作后截图 | 减少往返 | — |
| D9 | **UI 用 Tauri 2 + React + TypeScript** | 体积小、跨 macOS/Linux；Rust 侧做进程管理与托盘；React 生态成熟 | Electron（体积大）、原生（双平台成本高） |
| D10 | **服务是唯一的状态与配置拥有者**，UI 只通过管理 API 操作 | 服务可独立运行；CLI 与 UI 行为一致；避免两处写配置冲突 | UI 直接读写 YAML |
| D11 | 管理 API 与 MCP 同一 HTTP 服务、不同路径（`/api/v1` vs `/mcp`）、不同 token | 一个端口好部署；权限分离 | 单独端口 / unix socket（浏览器 webview 难直连） |
| D12 | UI 前端直接调用服务 HTTP/WS，Rust 侧只负责进程管理、托盘、系统集成 | Rust 代码最少，前后端解耦，也便于未来做 Web 版 | 全部经 Tauri command 代理（多一层） |
| D13 | 实时画面用 WebSocket 推送 JPEG 帧（按需、限帧率） | 实现简单，本地带宽充足 | WebRTC/H.264（复杂度高，后续再考虑） |
| D14 | 人工接管时**按机器加控制锁**，模型调用被拒绝 | 防止人和模型同时操作冲突 | 不仲裁 |
| D15 | 密码支持系统钥匙串（`keychain:` 引用） | UI 填密码不落盘明文 | 仅明文/环境变量 |
| D16 | OCR 在服务端用 tesseract 对已有截图识别 | Windows 端零依赖、锁屏 / 无焦点也可用、覆盖所有应用；返回文字坐标可直接点击 | Windows 内置 OCR（需解锁桌面、经 run_command，慢）作为未安装 tesseract 时的回退 |
| D17 | 不在 Windows 端常驻代理 | 零常驻、无杀软隔离风险、无需引导时间 | win-rdp-mcp 式常驻 exe（见 comparison-win-rdp-mcp.md） |
| D18 | 系统 / 窗口 / UIA 能力用预设临时 PowerShell 脚本实现，经 `run_command` 通道执行 | 复用现有零常驻通道；输出由脚本结构化并限制条数，避免撑爆上下文 | 每类一个专用工具（工具数膨胀） |
| D19 | 长任务通过 `run_command detach` + `job` 工具异步执行 | 复用 jobs 目录的 .out/.err/.code 文件，无需新机制；模型可边跑安装边操作界面 | 同步阻塞直到超时 |
| D20 | 全面更名为 **remoter**：应用显示名 `Remoter`（`Remoter.app`，bundle id `com.lianyun.remoter`），其余技术名称小写、`remoter-mcp`、`remoter-worker`、`~/.remoter`、`REMOTER_MCP_*`、钥匙串服务名 `remoter`；Go module 路径暂不变 | 名称统一；首次启动自动从 `~/.rdp_mcp` 复制配置、`.env`、`known_hosts`，钥匙串回退读取旧服务名，用户无需手动迁移 | 只改对外显示名（技术名与产品名不一致） |
| D21 | 支持 **macOS 被控端**：`protocol: vnc` 的机器经系统自带“屏幕共享”（RFB，Apple ARD 账号认证）取画面与输入，经“远程登录”（SSH）执行命令、传文件、读写剪贴板和输入文字；VNC 客户端用纯 Go 实现，以 `remoter-mcp vnc-worker` 子进程运行并复用 worker IPC | Mac 端零安装；实测 Apple VNC 服务端不支持标准剪贴板、光标形状和改分辨率，ASCII 按键会被中文输入法截获，而 SSH 能可靠完成这些；纯 Go 无新增 C 依赖与打包；独立进程保留崩溃隔离，Session 层几乎不变 | libvncclient C worker（多一套打包依赖、收益小）；Mac 端常驻 agent（需逐台授予 TCC 权限）；纯 SSH + `screencapture`（被 TCC 拦截） |

## 4. 总体架构

```
 ┌──────────────────────────┐        ┌──────────────────────────────┐
 │  MCP 客户端（模型）         │        │  Remoter 桌面应用（Tauri）     │
 │  Claude Code / Desktop … │        │  React UI ── HTTP/SSE/WS ──┐   │
 └────────────┬─────────────┘        │  Rust：进程管理/托盘/自启动   │   │
              │ /mcp  (Bearer mcp_token)  └──────────┬─────────────┘   │
              │                          spawn/stop  │   /api/v1 (Bearer admin_token)
              ▼                                      ▼                 │
 ┌────────────────────────────────────────────────────────────────────▼─┐
 │ remoter-mcp 服务（Go，可独立运行：CLI / launchd / systemd / Docker）          │
 │  ┌──────────┐ ┌───────────┐ ┌──────────┐ ┌──────────┐ ┌───────────┐  │
 │  │ MCP 层    │ │ 管理 API   │ │ 事件总线  │ │ 配置管理   │ │ 审计/日志  │  │
 │  └────┬─────┘ └─────┬─────┘ └────┬─────┘ └────┬─────┘ └───────────┘  │
 │       └────────┬────┴────────────┴────────────┘                      │
 │          ┌─────▼─────────────────────────────┐                        │
 │          │ Session Manager（每机器一个 Session）│                        │
 │          │ 状态机 / 控制锁 / 重连 / 空闲回收     │                        │
 │          └──┬──────────────┬──────────────┬──┘                        │
 └─────────────┼──────────────┼──────────────┼───────────────────────────┘
               │ unix socket IPC（JSON + 二进制帧）
         ┌─────▼────┐   ┌─────▼────┐   ┌─────▼────┐
         │remoter-worker│   │remoter-worker│ … │remoter-worker│   C + libfreerdp，GDI 内存渲染
         └─────┬────┘   └─────┬────┘   └─────┬────┘
               │ RDP          │              │
            Win-PC-1       Win-PC-2      Win-Server
```

三个可执行产物：

| 产物 | 语言 | 说明 |
|---|---|---|
| `remoter-mcp` | Go | 服务 + CLI |
| `remoter-worker` | C | 单机器 RDP 会话进程，由服务拉起 |
| Remoter 桌面应用 | Tauri（Rust + React） | 桌面管理应用，内置前两者作为 sidecar |

## 5. remoter-worker（C）

基于 `client/Sample/tf_freerdp.c` 改写，单线程事件循环。

1. **启动**：`remoter-worker --ipc <socket>`，连接参数（含密码）通过 IPC `connect` 下发，不出现在进程参数中。
2. **连接**：设置主机、端口、账号、分辨率、安全协议、`ColorDepth=32`；`gdi_init(BGRA32)`；按配置加载 `cliprdr`、`disp`、`rdpdr+drive`、可选 `rail`。
3. **证书**：`VerifyCertificateEx/VerifyChangedCertificateEx` 转成 IPC 事件，阻塞等服务回复。
4. **事件循环**：`freerdp_get_event_handles` 与 IPC socket 一起等待，所有 FreeRDP 调用在同一线程。
5. **帧缓冲**：维护 `dirty_seq`、`last_change_ts`；有订阅时发送 `frame_dirty` 事件（合并节流，最多 30 次/秒）。
6. **指针**：跟踪指针形状（位图 + 热点）与位置，供截图合成光标和 UI 显示。
7. **输入**：鼠标、扩展鼠标、扫描码、Unicode 文本。
8. **剪贴板**：CF_UNICODETEXT 读写（文件剪贴板后续）。
9. **断线**：上报 `disconnected`，由服务决定重连。
10. **退出**：IPC 断开即退出（服务崩溃时 worker 不残留）。

代码规模预估 1500–2500 行。协议见 [worker-ipc.md](worker-ipc.md)。

## 5a. macOS 被控端：remoter-mcp vnc-worker（Go）

`protocol: vnc` 的机器不使用 `remoter-worker`，而是由服务启动自身的隐藏子命令 `remoter-mcp vnc-worker --ipc <socket>`（`internal/vnc`）。它实现与 C worker 相同的 IPC 协议（[worker-ipc.md](worker-ipc.md)），因此 Session 的状态机、重连、控制锁、截图与坐标换算全部复用。

Mac 端前提：系统设置 → 通用 → 共享，打开“屏幕共享”（或“远程管理”）与“远程登录”，并允许配置中的账号访问。一套账号密码同时用于 VNC 与 SSH。

**VNC（画面与输入）**

1. 握手：RFB 3.8；认证优先 Apple ARD（security type 30：DH 协商 → MD5 → AES-128-ECB 加密账号密码），其次 VNC 密码（type 2）、无认证（type 1）。共享模式连接（不踢掉其他观看者）。
2. 像素格式 32bpp BGRX；编码 ZRLE（主）、CopyRect、Raw，伪编码 DesktopSize / LastRect。收到一次更新后立即再请求增量更新，帧缓冲始终保持最新；`dirty_seq` / `idle_ms` 由更新驱动。
3. 鼠标：RDP 指针标志翻译为 RFB 按钮掩码（左 1、中 2、右 4、滚轮 4–7 号按钮、`x1` = 8 号按钮）。
4. 键盘：扫描码翻译为 X11 keysym（美式布局）；`win` → `Super_L`，在 Mac 上即 Command；`alt` → Option。`unicode`：非 ASCII 字符用 Unicode keysym（`0x01000000 | 码点`，绕过输入法直接上屏），ASCII 用普通 keysym（会经过当前输入法）。
5. 光标：Apple 服务端既不发送光标形状、也不把光标画进帧缓冲；worker 上报最后一次发送的指针位置，截图时画默认箭头。物理鼠标的移动不可见。
6. 不支持：剪贴板消息（Apple 只对自家客户端同步剪贴板）、改分辨率、Ctrl+Alt+Del。

**SSH（命令、文件、剪贴板、文字输入）**

服务端用 `golang.org/x/crypto/ssh` 为每台 Mac 维护一个 SSH 连接（按需建立，会话断开时关闭，失败自动重拨一次）。主机密钥按 `cert` 策略校验：默认 TOFU，指纹记录在 `known_hosts` 中键为 `ssh:<host>:<port>`。

| 能力 | 实现 |
|---|---|
| `run_command` | 远端 `~/Library/Caches/remoter/jobs/<id>.*`：上传用户脚本与 runner，runner 以登录 shell（`zsh -l` 等，PATH 与终端一致）后台执行、独立进程组、自带超时，写 `.out/.err/.pid/.code`；服务轮询 `.code`。同步与 `detach` 共用一套，`job cancel` 用 `kill -- -<pgid>`。不依赖图形桌面 |
| `file` | `cat` 流式上传/下载，`stat`/`find` 列目录与搜索，路径为 POSIX，支持 `~` |
| `clipboard` | `pbcopy` / `pbpaste`（`LANG=en_US.UTF-8`），仅文本 |
| `keyboard type` | 默认“粘贴输入”：保存原剪贴板文本 → `pbcopy` 目标文字 → Cmd+V → 恢复；`\n`、`\t` 按 Return / Tab 键发送。实测中文、符号、全角/半角在拼音输入法下均正确。SSH 不可用或屏幕处于锁定/登录窗口（`ioreg` 检测）时回退为按键输入（锁屏密码框为安全输入，输入法不介入） |

不连接 VNC 也能用的工具：`run_command`、`job`、`file`、`clipboard`（仍受控制模式约束）。

## 6. remoter-mcp 服务（Go）

### 6.1 包结构

| 包 | 职责 |
|---|---|
| `cmd/remoter-mcp` | CLI 入口 |
| `internal/config` | YAML 加载/校验/保存（保留注释，`yaml.v3` Node API）、`${ENV}`、`password_cmd`、`keychain:`、文件监听热加载 |
| `internal/secrets` | 钥匙串读写（`zalando/go-keyring`：macOS Keychain、Linux Secret Service） |
| `internal/session` | Session 状态机、worker 进程监管、IPC 客户端、控制锁、重连、空闲回收 |
| `internal/worker` | IPC 编解码 |
| `internal/mcpserver` | MCP 工具注册与实现 |
| `internal/adminapi` | 管理 REST API、SSE 事件流、实时画面 WebSocket |
| `internal/events` | 进程内事件总线（状态变化、工具调用、日志） |
| `internal/imaging` | 缩放/裁剪/编码、光标合成、坐标换算 |
| `internal/keymap` | 键名 → 扫描码 |
| `internal/remote` | `run_command`、`file`：Windows 经共享目录 + 运行对话框，macOS 经 SSH（§5a）；任务登记与结果轮询 |
| `internal/vnc` | macOS 被控端的 RFB 客户端（ARD 认证、ZRLE 解码、keysym 映射）与 `vnc-worker` 子命令（§5a） |
| `internal/audit` | 审计日志（JSONL，按天滚动，可选保存截图缩略图） |
| `internal/runtime` | 单实例锁、运行时文件、优雅退出 |

### 6.2 CLI

```
remoter-mcp serve   [--config PATH] [--listen ADDR] [--worker PATH] [--log-level L]
remoter-mcp status                 # 读运行时文件并请求 /api/v1/health
remoter-mcp stop                   # 通过管理 API 优雅退出
remoter-mcp check-config [--config PATH]
remoter-mcp machine test <name>    # 测试连接并保存一张截图
remoter-mcp secret set <name>      # 交互输入密码写入钥匙串
remoter-mcp trust <name>           # 忘记已记录的证书指纹，下次连接重新信任
remoter-mcp version
remoter-mcp vnc-worker --ipc SOCK   # 内部使用：由服务为 protocol: vnc 的机器启动
```

已实现（v0.4）：`serve`、`status`、`stop`、`check-config`（含密码可解析性检查）、`secret set|delete`、`trust`、`version`。
未实现：`machine test`（可用管理 API `POST /machines/{name}/test` 或 UI 代替）。不做：`service install|uninstall`（2026-11-04 决定）。

开发辅助：
- `go run ./tools/smoke -url ... -machine <name> [-input] [-run "hostname"] [-call <tool> -args '<json>']`：最小 MCP 客户端，用于冒烟测试。
- `go run ./tools/wsprobe -url ... -token <admin> -machine <name>`：实时画面 WebSocket 探针。
- `go run ./tools/vncprobe -host <mac> -user <账号> info|input|input2|clip|ssh|all`：macOS 屏幕共享 / SSH 能力探针（密码从钥匙串 `remoter-probe` 读取），D21 的实测依据。
- `internal/testutil/fakeworker`：测试二进制扮演 remoter-worker（模拟桌面与运行对话框），用于会话 / MCP 工具 / run_command 的自动化测试。
- `internal/vnc/vnctest`（假 RFB 服务端：ARD / VNC 密码认证、全部 ZRLE 子编码、记录输入事件）与 `internal/testutil/fakessh`（假 SSH 服务端，在本机执行命令）：macOS 路径的自动化测试。

### 6.3 运行时与单实例

- 数据目录 `~/.remoter/`（可配）：

  ```
  ~/.remoter/
    configs.yaml        # 用户级配置（项目级为 ./.remoter/configs.yaml）
    known_hosts         # 证书指纹（TOFU）
    run/
      remoter-mcp.lock      # 单实例 flock
      service.json      # {pid, listen, admin_token, version, started_at, config_path}，权限 0600
      ipc/              # worker socket
    shares/<machine>/   # drive 映射目录
    logs/  audit/
  ```

- 启动时获取 `remoter-mcp.lock`，失败则报告已有实例（打印其 `service.json`）。
- `admin_token`：配置中未指定时每次启动随机生成，写入 `service.json`；UI 与 CLI 读该文件获得管理权限（文件权限即授权边界）。
- 收到 SIGTERM / `POST /api/v1/service/shutdown`：停止接收新请求 → 断开所有会话 → 结束 worker → 删除 `service.json`。

### 6.4 Session 与并发控制

状态机：

```
idle ─connect→ connecting ─ok→ connected ─断线→ reconnecting ─ok→ connected
  ▲                 │fail           │disconnect        │超过次数
  └─────────────────┴───────────────┴──────────────────┴─→ failed（下次调用再尝试）
```

- 每个 Session 一个操作互斥锁：同机器操作串行，不同机器并行。
- **控制模式**（每机器）：

  | 模式 | MCP 工具 | UI 实时画面输入 |
  |---|---|---|
  | `agent`（默认） | 允许 | 只读 |
  | `human` | 拒绝，返回"机器正在被人工接管" | 允许 |
  | `paused` | 拒绝，返回"已暂停" | 只读 |

  UI 提供"接管 / 归还 / 暂停"按钮，以及全局"紧急停止"（全部机器进入 `paused`）。模式变化通过事件总线广播。
- `enabled: false` 的机器对 MCP 不可见。
- 空闲回收：`idle_disconnect` 到期断开（有 UI 观看时不回收）。

### 6.5 截图、坐标与光标

- 默认长边缩放到 `screenshot_max_width`（1280），返回 `scale` 与真实分辨率。
- 模型坐标按最近一次截图坐标系解释：`real = round(x / scale)`，裁剪到屏幕范围。
- GDI 帧缓冲**不含鼠标指针**，截图时默认按 worker 上报的指针形状和位置合成光标（可配置关闭）。

### 6.6 run_command / file

1. 本地 `~/.remoter/shares/<machine>/` 经 drive 通道映射为 `\\tsclient\mcp`。
2. `run_command`：生成 `jobs/<id>.user.ps1`（用户命令）与 `jobs/<id>.ps1`（runner）→ 截图后按 `Win+R`，比较前后画面确认运行对话框已出现（否则不输入任何内容并报错）→ 输入 `conhost.exe --headless powershell … -File \\tsclient\mcp\jobs\<id>.ps1`（无控制台窗口）回车 → 等待 runner 写出 `.started`（10 秒未出现则改用 `powershell -WindowStyle Hidden` 重试；runner 以独占方式创建 `.started`，保证不会执行两次；每台机器记住可用的启动方式）→ runner 记录 `.pid`，用户命令的输出写到 `.out|.err`，退出码写到 `.code` → 服务轮询 `.code` 或超时 → 返回并清理。
   - `detach: true`：启动确认后立即返回任务 id；后台 goroutine 每秒检查 `.code`，完成时发 `job` 事件；运行期间暂停空闲断开。`job cancel` 用 `taskkill /PID <pid> /T /F` 结束进程树。
3. `file upload/download`：经共享目录中转 + `Copy-Item`。
4. 前提：会话处于解锁桌面、无模态弹窗。失败时返回提示并附截图。

### 6.7 配置管理

- 服务是配置的唯一写入方：UI 经管理 API 修改，服务校验后写回 YAML（保留注释与未知字段）。
- 文件监听：用户手动编辑 YAML 或 `.env` 后自动热加载（`.env` 中删除的变量会被移除，真实进程环境变量始终优先）；校验失败保留旧配置并发出 `config_error` 事件。
- 热加载影响：新增机器立即可用；删除机器断开其会话；连接参数变化在下次连接生效（UI 提示可重连）；`server.listen` 变化需重启服务。

### 6.8 事件总线

事件类型：`service`（启动/停止）、`machine_state`、`control_mode`、`tool_call`（开始/结束、参数摘要、耗时、结果摘要、截图缩略图 id）、`config_changed`、`config_error`、`cert_verify`、`log`。通过 SSE 推给 UI，同时写审计日志。

## 7. 管理 API

前缀 `/api/v1`，`Authorization: Bearer <admin_token>`；WebSocket 通过 `?token=` 传递。CORS 仅允许 Tauri 源（`tauri://localhost`、`http://tauri.localhost`）与配置中额外允许的源。详见 [admin-api.md](admin-api.md)。

主要能力：健康检查与服务信息、关闭服务；机器增删改查、测试连接、连接/断开、控制模式；密码写入钥匙串；配置读取与保存；SSE 事件流；实时画面与输入 WebSocket；审计记录查询；证书确认。

## 8. 桌面 UI（Tauri）

详见 [ui.md](ui.md)。要点：

- **技术栈**：Tauri 2、React 18 + TypeScript + Vite、Tailwind + shadcn/ui、TanStack Query、Zustand。
- **Rust 侧（薄）**：sidecar 进程管理（启动/停止/重启/附着已有实例）、读取 `service.json`、系统托盘、开机自启、系统通知、"关闭窗口时服务是否继续运行"策略。
- **前端**：直接用 HTTP/SSE/WS 访问服务。
- **页面**：概览（服务状态、MCP 地址、客户端接入片段）、机器列表与编辑、机器详情（实时画面 + 接管 + 会话操作）、活动记录（模型工具调用时间线 + 截图）、日志、设置。

服务与 UI 的生命周期关系：

| 场景 | 行为 |
|---|---|
| UI 启动，已有服务运行（`service.json` 有效且健康检查通过） | 直接附着，不重复启动 |
| UI 启动，无服务 | 用内置 sidecar 启动服务（可在设置中关闭"自动启动"） |
| UI 点"重启服务" | `POST /service/shutdown` → 等待退出 → 重新拉起 |
| UI 退出 | 默认服务继续在后台运行；可设置为"随 UI 退出" |
| 服务由 launchd/systemd 管理 | UI 检测到后，启停操作改为调用 `launchctl`/`systemctl --user` |
| 服务异常退出 | UI 显示状态并提供重启按钮；托盘图标变色 |

## 9. 安全

- 默认只监听 `127.0.0.1`；监听非本地地址时必须配置 `mcp_token`，且 `admin_token` 不允许为空。
- MCP 与管理 API 使用不同 token：给模型客户端的 token 不能管理服务。
- 密码来源优先级：`keychain:` 引用 > `password_cmd` > `${ENV}` > 明文（明文时 `check-config` 警告）。
- 密码只经 IPC 下发 worker；日志与审计对 `password`、`keyboard.type` 文本（可配置）脱敏。
- 证书默认 TOFU，记录在 `known_hosts`；指纹变化时拒绝连接并发 `cert_verify` 事件由 UI 确认。
- 运行时文件、socket、共享目录权限 0600/0700。
- 共享目录只映射本机器专属目录，防止跨机器数据串用。

## 10. 可观测性

- 结构化日志（`log/slog`，JSON），按天滚动。
- 审计日志：每次 MCP 工具调用一条 JSONL；可选保存操作前后截图缩略图（保留天数可配）。
- `/api/v1/health` 健康检查；`/api/v1/metrics`（可选，Prometheus 格式：会话数、工具调用次数/耗时/错误、重连次数）。

## 11. 已知限制

1. Windows 家庭版不能作为 RDP 被控端。
2. 桌面版 Windows 单会话：连接会挤掉本地登录用户；服务器版支持多会话。
3. 无 UI 元素树，模型只能看图操作；后续可选 OCR / UIAutomation。
4. RDP 断开后远端会话锁定，需保持连接（自动重连 + 保活）。
5. `run_command` 依赖 GUI 状态。
6. 实时画面为 JPEG 帧流，高帧率高分辨率下 CPU 占用较高（默认 10fps）。
7. macOS：屏幕共享控制的是物理控制台（与本地用户共用屏幕）；无显示器的 Mac mini 需 HDMI 欺骗器或虚拟显示器才有正常分辨率；开启 FileVault 的 Mac 重启后停在解锁界面，VNC 与 SSH 都不可用；不支持改分辨率；截图中的光标是服务端合成的（只反映经服务发出的移动）；`keyboard type` 会短暂占用剪贴板（只恢复文本内容）；SSH 会话默认无权访问“桌面/文稿/下载”等 TCC 保护目录，需在远程登录设置中开启“允许远程用户完全访问磁盘”。
8. 主控端为 macOS 15+ 时，连接同一子网的机器需要授予启动服务的程序（Remoter 或终端）“本地网络”权限；服务会诊断并提示（`hint: local_network`）。

## 12. 测试策略

| 层次 | 内容 |
|---|---|
| 单元测试 | Go：config、keymap、imaging（坐标换算/缩放）、remote 脚本生成、状态机 |
| IPC 契约测试 | Go 侧 fake worker（按协议回放）；C 侧 IPC 编解码测试 |
| 集成测试 | 真实 Windows（本地 VM 或云主机）：连接、截图、输入、剪贴板、run_command、file；以环境变量提供目标，CI 中可选执行 |
| MCP 端到端 | MCP Inspector / Go MCP 客户端调用全部工具 |
| UI | 前端组件测试（Vitest）；关键流程（启动服务、添加机器、接管）用 Playwright 对接 mock 服务 |

## 13. 仓库结构

```
remoter/
  docs/                    # 设计文档
  worker/                  # C，CMake，链接 FreeRDP
    src/  tests/  CMakeLists.txt
  cmd/remoter-mcp/             # Go 入口（go.mod 在仓库根）
  internal/                # Go 各包（§6.1）
  app/                     # Tauri 桌面应用
    src/                   # React 前端
    src-tauri/             # Rust：sidecar/托盘/自启动
      binaries/            # 构建时放入 remoter-mcp-<triple>、remoter-worker-<triple>
  examples/                # 配置示例
  packaging/               # Dockerfile、依赖打包脚本
  scripts/
  Makefile                 # make worker | service | app | dist | test
```

构建与发布细节见 [packaging.md](packaging.md)。

## 14. 变更记录

| 日期 | 版本 | 变更 |
|---|---|---|
| 2026-11-04 | v0.1 | 初版设计 |
| 2026-11-04 | v0.2 | 加入 Tauri 桌面 UI；服务独立运行与运行时文件；管理 API；控制模式与人工接管；钥匙串密码；光标合成；配置热加载；可观测性、测试策略、打包方案 |
| 2026-11-04 | v0.5 | 桌面 UI（U1–U4）首版：Tauri 2 + React；Rust 侧只做服务发现/启停（sidecar 用标准库 `Command` + `setsid` 启动，因此不使用 `shell` 插件）、托盘、单实例、开机自启、关闭到托盘；前端也可在浏览器里运行（Playwright 端到端测试用）；`REMOTER_MCP_DATA_DIR` 覆盖数据目录用于测试/多实例。macOS 打包：`scripts/bundle-macos-libs.py` 把 FreeRDP 及其依赖放进 `Contents/Frameworks` 并改写 install name，产出未签名 .dmg |
| 2026-11-04 | v0.4 | 管理 API 全部实现（M6）；`run_command` 先确认运行对话框出现再输入、`conhost --headless` 无窗口启动并带回退（M8）；`run_command detach` + `job` 工具（D19）；服务端 OCR（D16，`screenshot ocr`）；`file search` / `append`；fakeworker 自动化测试；CI 工作流与 Dockerfile。决定不做：`system` / `inspect` 工具、`session lock`、`service install` |
| 2026-11-04 | v0.4.1 | 全面更名为 remoter（D20）：`remoter-mcp`、`remoter-worker`、`~/.remoter`、`REMOTER_MCP_*`、钥匙串服务名；从 `~/.rdp_mcp` 自动迁移配置；旧钥匙串条目可读 |
| 2026-11-04 | v0.4.2 | 部署到 172.23.4.162 时发现两处服务抢同一台 Windows 会互相踢、且快速重试无次数限制：快速重试只做一次；服务器以“被其他连接接管/注销/管理员断开”（ERRINFO 0x1/0x2/0x5/0xB/0xC）结束会话时不再自动重连，状态置为 failed 并说明原因，下次工具调用再连回 |
| 2026-11-04 | v0.3.1 | 真实 Windows 11 测试后的修复：支持 `.env`；连接后等待登录通知与画面稳定再返回（避免首帧空白/锁屏过渡画面）；屏蔽 FreeRDP 自身证书库的误导性告警；区域截图改为“放大查看”且不改变坐标系；修复 `run_command` 退出码丢失（需在进程存活时访问 `$p.Handle`）；输出统一为 LF |
| 2026-11-04 | v0.3 | 首版实现（M1–M3 完成，M4–M6 部分）。实现细节：每次连接/重连都启动新 worker 进程；证书由服务决策，worker 永不让 FreeRDP 持久化证书（FreeRDP 配置目录指向 `<data_dir>/freerdp`）；启用 RemoteFX 作为非 GFX 服务器的后备编码；配置热加载采用 2 秒轮询（无额外依赖）；IPC JSON 整数必须编码为整数；新增 `remoter-mcp trust` 命令 |
| 2026-11-13 | v0.6 | 支持 macOS 被控端（D21，§5a）：`protocol: vnc`，VNC（Go 实现的 `remoter-mcp vnc-worker`，复用 worker IPC）+ SSH（命令、文件、剪贴板、粘贴输入）。先用 `tools/vncprobe` 在 macOS 27 上实测：ARD 认证、ZRLE、坐标 1:1、Command = `Super_L` 可用；剪贴板消息、光标伪编码不可用；拼音输入法会改写 ASCII 按键。未找到 `remoter-worker` 时服务不再拒绝启动（只影响 RDP 机器） |
| 2026-11-18 | v0.6.1 | `.env` 随配置热加载；`${VAR}` 未定义或为空时连接报错并给出变量名（此前会用空密码登录，表现为难以理解的认证失败）；管理 API 增加 `POST /machines/test`，UI 不保存即可测试连接 |
| 2026-11-27 | v0.6.2 | macOS 15+ “本地网络”权限：Remoter 未授权时连不上同一子网的机器（只报 `ERRCONNECT_CONNECT_FAILED`，走网关的机器正常）。新增 `internal/netcheck` 诊断这种失败（TCP 层错误 + 目标在直连子网 + 服务进程自己再试连仍失败），在 `MachineInfo` / 试连结果 / `machine_state` 事件中返回 `hint: local_network`，并在错误文本末尾追加英文说明；UI 显示授权引导（打开本地网络设置、重启服务）；应用 Info.plist 增加 `NSLocalNetworkUsageDescription`。xrdp（Linux）在桌面会话启动失败时会在连接后立刻回 `ERRINFO_LOGOFF_BY_USER`（0xC），且常常是暂时的，此前被当作“被其他客户端接管”而不重试、并报 Windows 专属的提示：现在连接后短窗口内（尚未进入 connected）收到 0xC 时重试一次（测试连接也重试），仍失败才置 failed，提示区分 Windows / xrdp 的可能原因；其他接管码（0x1/0x2/0x5/0xB）与已连接后的 0xC 维持 v0.4.2 的不重试；接管提示改为不限 Windows 的措辞 |
