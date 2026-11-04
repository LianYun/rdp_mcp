# CLAUDE.md

基于 FreeRDP 的 Windows 远程控制 MCP 服务（Go 主服务 + C worker）。

- 设计以 `docs/` 为准，开始任何改动前先读 `docs/design.md`；架构决策变化时先更新文档（含 §3.1 决策表与 §9 变更记录），再改代码。
- 完成里程碑或其中一部分后，更新 `docs/roadmap.md` 的状态。
- MCP 工具的参数/行为变化必须同步 `docs/mcp-tools.md`；IPC 变化同步 `docs/worker-ipc.md`；配置字段变化同步 `docs/configuration.md` 与 `examples/configs.yaml`。
- 文档用中文撰写。
