# rdp_mcp

基于 [FreeRDP](https://github.com/FreeRDP/FreeRDP) 的 MCP 服务：让大模型通过 MCP（Streamable HTTP）控制多台 Windows 电脑。

> 状态：设计阶段，尚未可用。进度见 [docs/roadmap.md](docs/roadmap.md)。

## 特性（规划）

- 多台 Windows 机器，通过 `.rdp_mcp/configs.yaml` 配置
- 截图、鼠标、键盘（任意 Unicode 文本 + 组合键）、剪贴板、分辨率调整
- 远程执行命令、文件上传下载（无需在 Windows 端安装任何软件）
- 部署平台：macOS、Linux（支持无显示器服务器 / Docker）

## 文档

- [设计文档](docs/design.md)
- [MCP 工具规范](docs/mcp-tools.md)
- [配置说明](docs/configuration.md)
- [Worker IPC 协议](docs/worker-ipc.md)
- [路线图](docs/roadmap.md)
