# CLAUDE.md

基于 FreeRDP 的 Windows 远程控制 MCP 服务：Go 服务（`rdp-mcp`）+ C worker（`rdp-worker`）+ Tauri 桌面 UI（`app/`）。

- 设计以 `docs/` 为准，开始改动前先读 `docs/design.md`；架构决策变化时先更新文档（§3 决策表与 §14 变更记录），再改代码。
- 完成里程碑或其中一部分后，更新 `docs/roadmap.md` 的状态。
- 同步规则：
  - MCP 工具参数/行为 → `docs/mcp-tools.md`
  - 管理 API → `docs/admin-api.md`（同时更新 `app/src/api` 的类型）
  - IPC → `docs/worker-ipc.md`（Go 与 C 两侧同时改）
  - 配置字段 → `docs/configuration.md` 与 `examples/configs.yaml`
  - UI 页面/交互 → `docs/ui.md`
  - 构建/打包 → `docs/packaging.md`
- 服务必须能脱离 UI 独立运行；UI 不直接读写配置文件，只通过管理 API。
- 文档用中文撰写。
