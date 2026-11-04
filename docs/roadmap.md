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
| M6 | 管理 API（REST + SSE + 实时画面 WS）、事件总线、审计日志、配置写回与热加载、钥匙串 | 用 curl/websocat 完成全部管理操作 | 🟡 已实现：服务信息/关闭、机器列表与会话操作、控制模式、紧急停止、截图、证书重置、SSE、审计查询、钥匙串（CLI）、配置热加载；未实现：机器增删改、配置写回、实时画面 WS |

## 桌面 UI

| 里程碑 | 内容 | 验收标准 | 状态 |
|---|---|---|---|
| U1 | Tauri 骨架、sidecar 启停/附着、托盘、概览页（服务状态 + MCP 接入片段） | 从 UI 启动/停止/重启服务；UI 退出后服务继续运行 | ⬜ |
| U2 | 机器列表与编辑、测试连接、钥匙串密码、证书确认 | 不碰 YAML 完成机器管理 | ⬜ |
| U3 | 机器详情：实时画面、接管/归还/暂停、紧急停止 | 人工接管时模型调用被拒绝 | ⬜ |
| U4 | 活动记录、日志、设置、中英文、主题 | | ⬜ |

## 发布

| 里程碑 | 内容 | 状态 |
|---|---|---|
| R1 | Makefile、CI（lint/test/build）、Docker 镜像、launchd/systemd 安装 | 🟡 Makefile 已有 |
| R2 | macOS dylib 打包、签名公证；Linux AppImage/deb；GitHub Release | ⬜ |

建议顺序：M1 → M2 → M3 → M6（管理 API 基础部分）→ U1 → U2 → M4 → U3 → M5 → U4 → R1 → R2。

## 后续想法（未排期）

- OCR / UIAutomation 辅助定位元素
- RemoteApp（rail）模式执行命令
- 文件剪贴板
- 多显示器
- WebRTC/H.264 实时画面
- 录屏 / 操作回放
- Web 版管理界面（复用前端）
- Windows 平台部署服务
