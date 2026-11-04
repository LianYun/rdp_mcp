# 路线图

状态：⬜ 未开始 · 🟡 进行中 · ✅ 完成

| 里程碑 | 内容 | 验收标准 | 状态 |
|---|---|---|---|
| M1 | worker：连接、GDI 内存帧缓冲、鼠标/键盘/Unicode 输入、IPC | 测试程序可连接 Windows、截图保存、点击与输入生效 | ⬜ |
| M2 | Go 主服务：配置加载、worker 监管、MCP HTTP；工具 `list_machines` `screenshot` `mouse` `keyboard` `session` | Claude Code 注册后能操作一台机器 | ⬜ |
| M3 | 多机器、按机器加锁、自动重连、证书 TOFU、空闲断开 | 同时稳定控制多台 | ⬜ |
| M4 | 剪贴板、动态分辨率、`wait` 屏幕稳定检测 | | ⬜ |
| M5 | drive 映射；`file`、`run_command` | 远程执行命令、上传下载文件 | ⬜ |
| M6 | 鉴权、审计日志、Dockerfile、macOS/Linux CI、发布 | | ⬜ |

## 后续想法（未排期）

- OCR / UIAutomation 辅助定位元素
- RemoteApp（rail）模式执行命令
- 多显示器
- 录屏 / 操作回放
- Windows 平台部署服务端
