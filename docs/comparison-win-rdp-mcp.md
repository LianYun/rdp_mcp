# 与 win-rdp-mcp 的能力对比

> 对比对象：[stubbedev/win-rdp-mcp](https://github.com/stubbedev/win-rdp-mcp)（2026-11-04 版本，45 个工具）。只阅读了源码，未运行。
> 结论用于确定 M7（感知与异步）的范围，见 [roadmap.md](roadmap.md)。

## 1. 架构差异

| | win-rdp-mcp | rdp_mcp（本项目） |
|---|---|---|
| 控制端 | Linux：`xfreerdp` + Xvfb + xdotool + ImageMagick；macOS 需 Docker | C worker 链接 libfreerdp，内存渲染；macOS / Linux 原生 |
| Windows 端 | 经 `\\tsclient\mcp` 拷入并常驻 `win-rdp-mcp.exe`，通过目录里的 req/resp JSON 文件做 RPC；首次启动约 40 秒，可能被 Defender 隔离 | 不留常驻程序：每次 `run_command` 写一个临时 PowerShell 脚本到 `\\tsclient\mcp`，经 Win+R 执行 |
| 机器数 | 一个进程一台 | 一个服务多台 |
| 分辨率 | 固定 1280×800，1:1 坐标 | 可配置、可动态修改；截图缩放与坐标自动换算 |
| 传输 | MCP stdio（代理可单独跑 HTTP+OAuth） | MCP Streamable HTTP |
| 安全 | 三级工具开关（只读 / 桌面 / 破坏性） | MCP 与管理 token 分离、人工接管 / 暂停 / 紧急停止、审计日志、证书固定 |

两者整体思路相同（Windows 端不预装、通过 RDP 磁盘映射通信）；区别是它在远端常驻代理换取系统类工具，本项目坚持零常驻（D17）。

## 2. 工具对照

| 类别 | 它的工具 | 本项目 |
|---|---|---|
| 桌面输入 | Snapshot、Click、Type、Scroll、Move、Shortcut、Wait | 全覆盖且更强：三击、X1/X2、按下/松开、修饰键点击与拖拽、`wait until_stable`；动作后自动附截图 |
| 截图附带信息 | Snapshot 附窗口与控件列表；AnnotatedSnapshot 给控件画编号框 | **M7 补齐**：`screenshot ocr` 返回文字与坐标；`inspect` 返回窗口与 UIA 元素树 |
| 窗口 | FocusWindow、MinimizeAll、App（启动/切换/改大小） | **M7 补齐**：`inspect` 的 `windows`/`focus`/`window` action；显示桌面 = `win+d` |
| Shell | Shell | `run_command`（**M7 增加 `detach` 异步与 `job` 查询/取消**） |
| 剪贴板 | Get/SetClipboard（经代理） | `clipboard`（RDP 剪贴板通道，无需代理） |
| 文件 | FileRead/Write/List/Search/Download/Upload | `file` upload/download/list；**M7 增加 `search`、upload `append`** |
| 系统 | 进程、服务、注册表、计划任务、事件日志、网络、系统信息（约 15 个） | **M7 补齐**：`system` 工具（预设 PowerShell，限制行数、结构化输出） |
| 会话 | ReconnectSession（tscon）、LockScreen | `session`；**M7 增加 `lock`**；tscon 见 `system` |
| 异步任务 | GetTaskStatus、GetRunningTasks、CancelTask | **M7 补齐**：`job` 工具 |
| 其他 | OCR、ScreenRecord、Notification、PlaySound、Scrape | OCR → M7；其余不做（价值低或客户端已有） |

## 3. 本项目独有

多机器；动作后自动截图与坐标换算；屏幕稳定检测；人工接管 / 暂停 / 紧急停止；审计与管理 API、桌面 UI；Windows 端零常驻、无杀软风险；控制端 macOS 原生；动态分辨率；Ctrl+Alt+Del；不依赖代理的剪贴板与文件传输；文件直接落盘而不是 base64 进模型上下文。

## 4. 不做的项

- Windows 常驻代理：违背零常驻原则；它的控件枚举基于 Win32 子窗口，对浏览器 / Electron / UWP 基本无效，本项目改用临时脚本调 UI Automation。
- Scrape（MCP 客户端自身能抓网页）、PlaySound、Notification、录屏 GIF：价值低，需要时再评估。
