# rdp_mcp 设计文档

> 状态：v0.1 设计稿（2026-11-04）。本文档随实现持续更新，任何架构决策变化都应先改这里。
> 相关文档：[MCP 工具规范](mcp-tools.md) · [配置说明](configuration.md) · [Worker IPC 协议](worker-ipc.md) · [路线图](roadmap.md)

## 1. 目标

基于 FreeRDP 实现一个 MCP 服务，让大模型通过 MCP 控制多台 Windows 电脑。

- **功能尽可能全面**：截图、鼠标、键盘、剪贴板、分辨率、命令执行、文件传输、会话管理。
- **接口简单**：少量工具，统一 `machine` + `action` 参数风格。
- **HTTP 传输**：MCP Streamable HTTP（`/mcp` 端点）。
- **部署平台**：首先支持 macOS、Linux（含无显示器服务器 / Docker）。
- **配置**：`.rdp_mcp/configs.yaml` 声明多台机器。

非目标（当前阶段）：Windows 上部署服务端、音频、USB/打印机/智能卡重定向、多显示器。

## 2. 可行性依据（FreeRDP 3.x）

参考源码：FreeRDP `ec31c9fce`（3.x 主线），macOS Homebrew 版本 3.33.0。

| 能力 | FreeRDP API | 源码位置 |
|---|---|---|
| 无界面客户端骨架 | 连接 + 事件循环 + `gdi_init` | `client/Sample/tf_freerdp.c` |
| 读取屏幕 | `gdi_init(instance, PIXEL_FORMAT_XRGB32)` → `gdi->primary_buffer`；`update->EndPaint` 得到脏区域 | `include/freerdp/gdi/gdi.h` |
| 鼠标 | `freerdp_input_send_mouse_event` / `freerdp_input_send_extended_mouse_event`（X1/X2）/ 滚轮 `PTR_FLAGS_WHEEL`、`PTR_FLAGS_HWHEEL` | `include/freerdp/input.h` |
| 键盘 | `freerdp_input_send_unicode_keyboard_event`（任意文本，与布局无关）；`freerdp_input_send_keyboard_event_ex`（扫描码，组合键） | `include/freerdp/input.h` |
| 键名映射 | `GetVirtualKeyCodeFromName` + `GetVirtualScanCodeFromVirtualKeyCode` | `winpr/include/winpr/input.h` |
| 剪贴板 | cliprdr 通道 `CliprdrClientContext` | `include/freerdp/client/cliprdr.h` |
| 动态分辨率 | disp 通道 `SendMonitorLayout` | `include/freerdp/client/disp.h` |
| 文件共享 | rdpdr + drive 通道（本地目录 → `\\tsclient\<name>`） | `channels/drive/client` |
| 启动远程程序 | rail（RemoteApp）通道 | `channels/rail/client` |
| PNG 编码（备用） | `winpr_image_write_buffer(..., WINPR_IMAGE_PNG)` | `winpr/include/winpr/image.h` |
| 认证/安全/重连 | NLA/TLS/RDP、证书回调、auto-reconnect | `libfreerdp/core` |

结论：所需能力全部有现成 API，且 GDI 软件渲染不依赖 X11/Cocoa，可在 macOS/Linux/容器中无头运行。

## 3. 总体架构

```
          模型 / MCP 客户端
                │  Streamable HTTP  (POST/GET /mcp)
     ┌──────────▼──────────────────────────┐
     │ rdp-mcp  主服务（Go）                 │
     │  - MCP 协议与工具定义                  │
     │  - 读取 configs.yaml，管理多台机器      │
     │  - 截图缩放、坐标换算、PNG/JPEG 编码     │
     │  - 每台机器一把锁，同机操作串行          │
     │  - worker 进程监管（拉起/重启/空闲回收）  │
     └──┬─────────────┬──────────────┬──────┘
        │ unix socket（JSON 命令 + 二进制帧）
  ┌─────▼────┐  ┌─────▼────┐   ┌─────▼────┐
  │ rdp-worker│  │ rdp-worker│ … │ rdp-worker│  每台 Windows 一个 C 进程
  │ libfreerdp│  │ libfreerdp│   │ libfreerdp│  GDI 内存渲染，无窗口
  └─────┬────┘  └─────┬────┘   └─────┬────┘
        │ RDP         │              │
     Win-PC-1      Win-PC-2       Win-Server
```

### 3.1 关键决策（ADR 摘要）

| # | 决策 | 理由 | 备选及放弃原因 |
|---|---|---|---|
| D1 | 主服务用 **Go** | 单二进制部署；官方 Go MCP SDK 支持 Streamable HTTP；并发模型适合多机器管理 | Python（部署依赖重）；Rust（开发慢） |
| D2 | RDP 部分用 **C worker 独立进程**，每台机器一个 | 崩溃隔离（C 代码出错只影响一台机器）；C 代码量最小化；主服务无需 cgo | cgo 内嵌（一处崩溃全挂，交叉编译麻烦） |
| D3 | 截图编码在 **Go 侧** 完成 | C 侧只传原始 BGRA，逻辑简单；Go 侧方便做缩放/裁剪/JPEG 质量控制 | worker 内编码 PNG（C 侧复杂度上升） |
| D4 | 不用纯 Go/Python RDP 库 | 成熟度不足，NLA/GFX/通道支持不全 | — |
| D5 | 不用 xfreerdp + Xvfb + xdotool | macOS 不可用，链路脆弱 | — |
| D6 | 模型侧坐标 = **截图坐标** | 模型只看到缩放后的图，服务端统一换算，避免坐标错位这一最常见错误 | 让模型自己换算（易错） |
| D7 | `run_command` 走 **drive 映射 + 键盘启动脚本** | RDP 协议本身没有执行命令能力；此方式无需在 Windows 端安装任何东西 | 远端装 agent（违背"零安装"）；rdp2tcp（需远端组件） |
| D8 | 工具采用 `machine + action` 风格，总数控制在 ~9 个 | 接口简单、模型易选择 | 每个动作一个工具（工具数膨胀） |
| D9 | 动作类工具默认附带操作后截图 | 减少模型来回调用次数 | — |

## 4. 组件设计

### 4.1 rdp-worker（C）

基于 `client/Sample/tf_freerdp.c` 改写。职责：

1. **启动**：由主服务 fork/exec，参数只有 IPC socket 路径；连接参数通过 IPC `connect` 命令下发（避免密码出现在进程参数里）。
2. **连接**：设置 `ServerHostname/Port/Username/Password/Domain/DesktopWidth/Height`、安全协议、`ColorDepth=32`；`gdi_init(PIXEL_FORMAT_BGRA32)`；加载 `cliprdr`、`disp`、`rdpdr+drive`、可选 `rail` 通道。
3. **证书**：`VerifyCertificateEx` / `VerifyChangedCertificateEx` 回调转发给主服务决策（TOFU / 指纹 / 忽略）。
4. **事件循环**：`freerdp_get_event_handles` + IPC socket 一起 `WaitForMultipleObjects`；IPC 命令在 RDP 线程内执行，避免跨线程调用 FreeRDP。
5. **帧缓冲**：维护 `dirty_seq`（每次 `EndPaint` 有脏区则 +1）与 `last_change_ts`，供"等屏幕稳定"使用。
6. **输入**：鼠标/键盘/Unicode 原样注入；组合键由主服务拆解成 down/up 序列。
7. **剪贴板**：实现 CF_UNICODETEXT 的读写；服务端格式列表变化时缓存。
8. **断线**：检测断开后上报 `event: disconnected`，由主服务决定重连策略。

### 4.2 rdp-mcp 主服务（Go）

包划分：

| 包 | 职责 |
|---|---|
| `cmd/rdp-mcp` | CLI：`serve`、`check-config`、`version` |
| `internal/config` | 加载/校验 YAML，环境变量展开 `${VAR}`，`password_cmd` 执行 |
| `internal/session` | 每台机器一个 `Session`：worker 进程生命周期、IPC 客户端、互斥锁、状态机、自动重连、空闲断开 |
| `internal/mcp` | 工具注册、参数校验、调用 session、组装返回（文本 + 图片） |
| `internal/imaging` | BGRA → 缩放 → PNG/JPEG；坐标正/反向换算 |
| `internal/keymap` | 键名（`ctrl`、`enter`、`f5`、`a`…）→ 扫描码 + extended 标志 |
| `internal/remote` | `run_command`、`file` 的脚本生成、共享目录管理、结果轮询 |

Session 状态机：

```
idle ──connect──▶ connecting ──ok──▶ connected ──断线──▶ reconnecting ──ok──▶ connected
  ▲                    │fail                │disconnect          │超过重试次数
  └────────────────────┴────────────────────┴────────────────────┴──▶ failed（下次调用再尝试）
```

- 工具调用时若机器未连接且允许按需连接，则自动连接（`auto_connect: false` 的默认行为）。
- 同一台机器的所有工具调用通过 `Session.mu` 串行，不同机器完全并行。
- `idle_disconnect` 到期后断开并结束 worker。

### 4.3 截图与坐标

- 默认输出长边 `screenshot_max_width`（1280），保持比例；返回内容带上 `scale` 与原始分辨率文本说明。
- 模型传入的坐标均按"最近一次截图的坐标系"解释：`real = round(x / scale)`，并裁剪到屏幕范围。
- `region` 截图时坐标系仍为全屏截图坐标，返回图仅裁剪。

### 4.4 run_command / file 实现

1. 每台机器在本地有共享目录 `~/.rdp_mcp/shares/<machine>/`，通过 drive 通道映射为 `\\tsclient\mcp`。
2. `run_command`：
   1. 生成 `jobs/<id>.ps1`（或 `.cmd`），脚本把 stdout/stderr/exit code 写到 `jobs/<id>.out|.err|.code`；
   2. 若 RemoteApp 可用，用 rail 直接执行；否则键盘方式：`Win+R` → 输入 `powershell -NoP -W Hidden -EP Bypass -File \\tsclient\mcp\jobs\<id>.ps1` → Enter；
   3. 主服务轮询 `.code` 文件出现或超时，返回结果并清理。
3. `file upload`：本地复制到共享目录 → `run_command` 执行 `Copy-Item` 到目标路径；`download` 反之。
4. 限制：需会话处于解锁桌面、无模态弹窗抢焦点。失败时工具返回提示并附截图。

## 5. 安全

- 默认只监听 `127.0.0.1`；对外暴露时必须配置 `auth_token`（Bearer）。
- 密码支持 `${ENV}` 与 `password_cmd`（如 macOS 钥匙串、`pass`），不建议明文。
- 密码只通过 IPC 下发给 worker，不出现在命令行/日志中；日志对敏感字段脱敏。
- 证书默认 TOFU，指纹记录在 `~/.rdp_mcp/known_hosts`。
- 审计日志：记录每次工具调用（机器、动作、参数摘要、结果），`type` 文本内容可配置是否脱敏。

## 6. 已知限制

1. Windows 家庭版不能作为 RDP 被控端。
2. 桌面版 Windows 单会话：连接会挤掉本地登录用户；服务器版支持多会话。
3. RDP 只传画面，无 UI 元素树；后续可选 OCR 或通过 `run_command` 调 UIAutomation 补充。
4. RDP 断开后远端会话锁定，需要保持连接（worker 自动重连 + 保活）。
5. `run_command` 依赖 GUI 状态（见 4.4）。

## 7. 构建与部署

- 依赖：FreeRDP 3.x（`freerdp3`、`freerdp-client3`、`winpr3`）、CMake、C 编译器、Go。
- macOS：`brew install freerdp cmake`。
- Linux：Debian 13 / Ubuntu 24.04+ 安装 `freerdp3-dev`；否则源码最小构建：
  `-DWITH_CLIENT=ON -DWITH_X11=OFF -DWITH_CLIENT_SDL=OFF -DWITH_SERVER=OFF -DWITH_FFMPEG=OFF -DWITH_FUSE=OFF`。
- Docker：基于 Debian 的镜像，worker 无需 X。
- 运行：`rdp-mcp serve`；Claude Code 注册：`claude mcp add --transport http rdp http://127.0.0.1:8765/mcp`。

## 8. 仓库结构

```
rdp_mcp/
  docs/            # 设计文档（本目录）
  worker/          # C，CMake，链接 FreeRDP
  cmd/rdp-mcp/     # Go 主入口
  internal/        # config / session / mcp / imaging / keymap / remote
  examples/        # 配置示例
  scripts/  Dockerfile  README.md
```

## 9. 变更记录

| 日期 | 版本 | 变更 |
|---|---|---|
| 2026-11-04 | v0.1 | 初版设计 |
