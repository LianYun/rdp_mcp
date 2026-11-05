# Remoter

基于 [FreeRDP](https://github.com/FreeRDP/FreeRDP) 的 Windows 远程控制 MCP 服务：让大模型通过 MCP（Streamable HTTP）控制多台 Windows 电脑，并提供 Tauri 桌面管理应用。

> 状态：v0.4。服务端与桌面应用均可用；部分功能待真实 Windows 验证。进度见 [docs/roadmap.md](docs/roadmap.md)。

## 组成

| 组件 | 说明 |
|---|---|
| `remoter-mcp` | Go 服务 + CLI，可独立运行（命令行 / Docker），也可由桌面应用启动 |
| `remoter-worker` | C + libfreerdp，每台 Windows 一个无界面会话进程 |
| Remoter（桌面应用 `Remoter.app`） | Tauri 应用：管理机器、启停服务、实时画面与人工接管、操作记录 |

从 rdp_mcp 升级：首次以默认路径启动 `remoter-mcp` 时，会把 `~/.rdp_mcp` 中的 `configs.yaml`、`.env`、`known_hosts` 复制到 `~/.remoter`（旧目录保留）；旧钥匙串条目（服务名 `rdp_mcp`）仍可读取。环境变量已改为 `REMOTER_MCP_*`。

## 快速开始（macOS / Linux）

1. 安装依赖并构建：

   ```bash
   # macOS
   brew install freerdp cmake pkg-config go tesseract tesseract-lang   # tesseract 用于 OCR，可选
   # Debian 13 / Ubuntu 24.04+
   # sudo apt install build-essential cmake pkg-config freerdp3-dev golang tesseract-ocr tesseract-ocr-chi-sim

   make            # 生成 build/bin/remoter-mcp 和 build/bin/remoter-worker
   make app        # 可选：构建桌面应用 remoter（需要 Node.js、pnpm、Rust）
   ```

2. 写配置 `~/.remoter/configs.yaml`（字段说明见 [docs/configuration.md](docs/configuration.md)），或在桌面应用里添加机器：

   ```yaml
   machines:
     - name: win1
       description: "办公电脑"
       host: 192.168.1.20
       username: alice
       password: keychain:win1     # 先执行 build/bin/remoter-mcp secret set win1
   ```

3. 检查并启动：

   ```bash
   build/bin/remoter-mcp check-config
   build/bin/remoter-mcp serve          # MCP 端点 http://127.0.0.1:8765/mcp
   ```

4. 接入 MCP 客户端（桌面应用的“MCP 接入”卡片可一键复制各客户端的片段，含 token）：

   ```bash
   # Claude Code
   claude mcp add --transport http remoter http://127.0.0.1:8765/mcp
   # Codex CLI（与 Codex app 共用 ~/.codex/config.toml）
   codex mcp add remoter --url http://127.0.0.1:8765/mcp
   ```

   Codex app 也可以直接在 `~/.codex/config.toml` 中添加：

   ```toml
   [mcp_servers.remoter]
   url = "http://127.0.0.1:8765/mcp"
   startup_timeout_sec = 30
   tool_timeout_sec = 180   # 截图、run_command 可能超过默认 60 秒
   ```

5. 冒烟测试（可选）：

   ```bash
   go run ./tools/smoke -machine win1 -input -run "hostname"
   ```

Windows 端要求：专业版/企业版/服务器版，已开启远程桌面。Windows 端不需要安装任何东西。

## MCP 工具

`list_machines` `screenshot`（含 OCR）`mouse` `keyboard` `clipboard` `run_command`（可后台执行）`job` `file` `session` `wait`，详见 [docs/mcp-tools.md](docs/mcp-tools.md)。

## CLI

```
remoter-mcp serve | status | stop | check-config | secret set|delete <key> | trust <machine> | version
```

## 文档

- [技术方案](docs/design.md)
- [MCP 工具规范](docs/mcp-tools.md)
- [管理 API](docs/admin-api.md)
- [桌面应用](docs/ui.md)
- [配置说明](docs/configuration.md)
- [Worker IPC 协议](docs/worker-ipc.md)
- [构建与发布](docs/packaging.md)
- [与 win-rdp-mcp 的对比](docs/comparison-win-rdp-mcp.md)
- [路线图](docs/roadmap.md)
