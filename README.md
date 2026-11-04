# rdp_mcp

基于 [FreeRDP](https://github.com/FreeRDP/FreeRDP) 的 MCP 服务：让大模型通过 MCP（Streamable HTTP）控制多台 Windows 电脑，并提供 Tauri 桌面管理界面。

> 状态：设计阶段，尚未可用。进度见 [docs/roadmap.md](docs/roadmap.md)。

## 组成

| 组件 | 说明 |
|---|---|
| `rdp-mcp` | Go 服务 + CLI，可独立运行（命令行 / launchd / systemd / Docker） |
| `rdp-worker` | C + libfreerdp，每台 Windows 一个无界面会话进程 |
| `rdp_mcp Desktop` | Tauri 桌面应用：管理机器、启停服务、实时画面与人工接管、操作记录 |

## 特性（规划）

- 多台 Windows 机器，通过 `.rdp_mcp/configs.yaml` 或 UI 配置
- 截图、鼠标、键盘（任意 Unicode 文本 + 组合键）、剪贴板、分辨率调整
- 远程执行命令、文件上传下载（Windows 端零安装）
- 人工接管 / 暂停 / 紧急停止，模型操作审计
- 部署：macOS、Linux（服务支持无显示器服务器 / Docker）

## 文档

- [技术方案](docs/design.md)
- [MCP 工具规范](docs/mcp-tools.md)
- [管理 API](docs/admin-api.md)
- [桌面 UI](docs/ui.md)
- [配置说明](docs/configuration.md)
- [Worker IPC 协议](docs/worker-ipc.md)
- [构建与发布](docs/packaging.md)
- [路线图](docs/roadmap.md)
