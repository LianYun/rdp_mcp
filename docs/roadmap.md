# 路线图

状态：⬜ 未开始 · 🟡 进行中 · ✅ 完成

## 服务端

| 里程碑 | 内容 | 验收标准 | 状态 |
|---|---|---|---|
| M1 | worker：连接、GDI 内存帧缓冲、指针跟踪、鼠标/键盘/Unicode 输入、IPC | 测试程序可连接 Windows、截图保存、点击与输入生效 | ✅ 已在 FreeRDP 示例服务器上验证 |
| M2 | Go 服务：配置加载、运行时文件/单实例、worker 监管、MCP HTTP；工具 `list_machines` `screenshot` `mouse` `keyboard` `session` | Claude Code 注册后能操作一台机器 | ✅ |
| M3 | 多机器、按机器加锁、自动重连、证书 TOFU、空闲断开、控制模式 | 同时稳定控制多台；暂停后模型调用被拒绝 | ✅ |
| M4 | 剪贴板、动态分辨率、`wait` 屏幕稳定检测、光标合成 | | ✅ 已在 Windows 11 验证（剪贴板双向含中文、动态分辨率） |
| M5 | drive 映射；`file`、`run_command` | 远程执行命令、上传下载文件 | ✅ 已在 Windows 11 验证（powershell/cmd、退出码、中文输出、二进制文件往返校验） |
| M6 | 管理 API（REST + SSE + 实时画面 WS）、事件总线、审计日志、配置写回与热加载、钥匙串 | 用 curl/websocat 完成全部管理操作 | ✅ 全部接口已实现（机器增删改与写回、钥匙串、试连、证书确认、配置读写、实时画面与接管 WS、日志、审计缩略图、token 轮换），有 httptest 覆盖 |
| M7a | `run_command detach` + `job` 工具（list/status/output/cancel/remove） | 长任务不阻塞，可查询输出与取消 | ✅ 有自动化测试，待真实 Windows 验证 |
| M7b | 服务端 OCR（tesseract，`screenshot ocr` / `ocr_only`）；`file search`、`file upload append` | 模型能按文字定位可点击位置、按名称查找文件 | ✅ OCR 有真实 tesseract 测试；file search/append 待真实 Windows 验证 |
| M7c | （不做）`system` 预设运维工具、`inspect` 窗口与 UIA 工具、`session lock`、工具描述中的运维配方 | — | ❌ 2026-11-04 决定不做；需要时用 `run_command` |
| M8 | 健壮性：`run_command` 先确认运行对话框已弹出再输入、避免终端窗口闪现；连接后立即被断开时快速重试；会话 / IPC / MCP 工具的自动化测试（fake worker） | | 🟡 已实现并有自动化测试（fake worker 模拟 RDP 与运行对话框）；`conhost --headless` 无窗口启动待真实 Windows 验证 |

## 桌面 UI

| 里程碑 | 内容 | 验收标准 | 状态 |
|---|---|---|---|
| U1 | Tauri 骨架、sidecar 启停/附着、托盘、概览页（服务状态 + MCP 接入片段） | 从 UI 启动/停止/重启服务；UI 退出后服务继续运行 | ✅ 已实现；Rust 侧有单测，前端经 Playwright 在真实服务上验证；打包后的 .app 可拉起内置 sidecar |
| U2 | 机器列表与编辑、测试连接、钥匙串密码、证书确认 | 不碰 YAML 完成机器管理 | ✅ 新增/编辑/删除/启用开关/搜索/标签过滤/测试连接/证书确认/.rdp 导入，Playwright 覆盖 |
| U3 | 机器详情：实时画面、接管/归还/暂停、紧急停止 | 人工接管时模型调用被拒绝 | ✅ 画面帧、光标叠加、接管输入、模型调用被拒绝均由 Playwright 验证；输入法组合输入、Ctrl+Alt+Del、剪贴板发送、分辨率切换已实现，**未在真实 Windows 上验证**（示例服务器不支持剪贴板/resize） |
| U4 | 活动记录、日志、设置、中英文、主题 | | ✅ |

## 发布

| 里程碑 | 内容 | 状态 |
|---|---|---|
| R1 | Makefile、CI（lint/test/build）、Docker 镜像 | 🟡 Makefile、CI 工作流、`packaging/Dockerfile` 已有；CI 未在 GitHub 上跑过。安装为系统服务（`service install`）不做 |
| R2 | macOS dylib 打包、签名公证；Linux AppImage/deb；GitHub Release | 🟡 macOS：dylib 打包脚本 + 未签名 .dmg 已完成；签名公证、Linux AppImage/deb（仅有配置说明）、GitHub Release 未做 |

建议顺序：M1 → M2 → M3 → M6（管理 API 基础部分）→ U1 → U2 → M4 → U3 → M5 → U4 → R1 → R2。

当前（2026-11-04 起）：服务端 M6 收尾 → M8 → M7 → R1，与 UI U1 → U4 → R2 并行。

## 后续想法（未排期）

- RemoteApp（rail）模式执行命令
- 文件剪贴板
- 多显示器
- WebRTC/H.264 实时画面
- 录屏 / 操作回放
- Web 版管理界面（复用前端）
- Windows 平台部署服务
