# Remoter

让大模型通过 MCP（Streamable HTTP）控制多台 Windows 和 macOS 电脑，并提供 Tauri 桌面管理应用。

- **Windows**：基于 [FreeRDP](https://github.com/FreeRDP/FreeRDP) 的远程桌面（RDP）连接。
- **macOS**：经系统自带的“屏幕共享”（VNC）取画面与键鼠输入，经“远程登录”（SSH）执行命令、传文件、读写剪贴板（`protocol: vnc`）。

被控端都不需要安装任何东西。

> 状态：v0.6（2026-11-13）。服务端与桌面应用均可用；macOS 被控端已实现，部分功能还没在真机上验证。进度见 [docs/roadmap.md](docs/roadmap.md)。

## 组成

| 组件 | 说明 |
|---|---|
| `remoter-mcp` | Go 服务 + CLI，可独立运行（命令行 / Docker），也可由桌面应用启动；内置 macOS 用的 VNC 客户端 |
| `remoter-worker` | C + libfreerdp，每台 Windows 一个无界面会话进程；只控制 Mac 时不需要 |
| Remoter（桌面应用 `Remoter.app`） | Tauri 应用：管理机器、启停服务、实时画面与人工接管、操作记录 |

## 快速开始

### 1. 构建

```bash
# macOS
brew install freerdp cmake pkg-config go tesseract tesseract-lang   # tesseract 用于 OCR，可选
# Debian 13 / Ubuntu 24.04+
# sudo apt install build-essential cmake pkg-config freerdp3-dev golang tesseract-ocr tesseract-ocr-chi-sim

make            # 生成 build/bin/remoter-mcp 和 build/bin/remoter-worker
make app        # 可选：构建桌面应用 Remoter（需要 Node.js、pnpm、Rust）
```

只控制 Mac 时可以只装 Go，执行 `make service`（只生成 `remoter-mcp`，不依赖 FreeRDP）。

### 2. 准备被控端

**Windows**：专业版 / 企业版 / 服务器版，已开启远程桌面（设置 → 系统 → 远程桌面）。

**macOS**：系统设置 → 通用 → 共享，打开：

- “屏幕共享”（或“远程管理”）：用于截图和键鼠输入；
- “远程登录”：用于命令执行、文件、剪贴板和文字输入。

两项都要允许配置中的账号访问。账号写 Mac 的**短名**（在 Mac 终端执行 `whoami` 可查到），密码就是这个账号的登录密码，VNC 与 SSH 共用这一套。如果要让 `run_command` / `file` 访问“桌面”“文稿”“下载”等目录，还需在“远程登录”旁的 ⓘ 中勾选“允许远程用户完全访问磁盘”。

### 3. 配置机器

写 `~/.remoter/configs.yaml`（也可放在项目目录 `./.remoter/configs.yaml`），或在桌面应用里添加机器。字段说明见 [docs/configuration.md](docs/configuration.md)，完整示例见 [examples/configs.yaml](examples/configs.yaml)。

```yaml
machines:
  # Windows（默认 protocol: rdp）
  - name: win1                       # 机器名，模型调用工具时用它
    description: "办公电脑，装了 Office 和 Chrome"   # 给模型看的说明，建议写清用途
    host: 192.168.1.20
    username: alice                  # Windows 用户名；域账号可加 domain: CORP
    password: keychain:win1          # 密码写法见下一步

  # macOS
  - name: mac1
    description: "Mac mini，跑 iOS 构建"
    protocol: vnc                    # 屏幕共享（VNC）+ 远程登录（SSH）
    host: 192.168.1.40
    username: alice                  # Mac 账号短名
    password: ${MAC1_PASSWORD}       # 从环境变量 / .env 读取
    # port: 5900                     # VNC 端口，默认 5900
    # ssh_port: 22                   # SSH 端口，默认 22
```

配置文件修改后服务会自动热加载（每 2 秒检查一次），不需要重启。

### 4. 配置密码

不要把明文密码写进配置文件。Windows 和 Mac 机器的写法完全相同，有四种：

| 写法 | 配置 | 适用场景 |
|---|---|---|
| 系统钥匙串 | `password: keychain:win1` | 有图形桌面的 macOS / Linux（个人电脑） |
| 环境变量 / `.env` | `password: ${WIN1_PASSWORD}` | Linux 服务器、Docker、无图形会话的环境 |
| 命令 | `password_cmd: "pass show remoter/win1"` | 已有密码管理器（`pass`、`op`、`security` 等） |
| 明文 | `password: 明文密码` | 不推荐，`check-config` 会警告 |

**钥匙串**：先把密码存进去，配置里引用同一个键名：

```bash
build/bin/remoter-mcp secret set win1      # 交互输入密码，存入 macOS Keychain / Linux Secret Service
build/bin/remoter-mcp secret delete win1   # 删除
```

`keychain:` 后面不写键名时默认用机器名。桌面应用的机器表单里填写密码时也会存进钥匙串。

**环境变量与 `.env`**：服务启动时读取 `~/.remoter/.env`（配置文件同目录、以及 `data_dir` 下的 `.env`），然后把配置中的 `${VAR}` 替换为对应的值：

```bash
cat >> ~/.remoter/.env <<'EOF'
# KEY=VALUE，支持 # 注释、export 前缀和引号
WIN1_PASSWORD='Windows 密码'
MAC1_PASSWORD='Mac 登录密码'
REMOTER_MCP_TOKEN='一串随机字符'      # 可选：供 server.mcp_token: ${REMOTER_MCP_TOKEN} 引用
EOF
chmod 600 ~/.remoter/.env            # 权限过宽时服务会警告
```

- 也可以直接在启动服务的环境里 `export WIN1_PASSWORD=...`，进程环境中已有的同名变量优先于 `.env`。
- Docker 中用 `docker run -e WIN1_PASSWORD=...`，或把 `.env` 放进挂载的数据目录。
- `${VAR}` 同样可用于 `server.mcp_token` 等字段。
- 服务运行中修改 `.env` 会自动重新加载（约 2 秒内生效），无需重启。

### 5. 检查并启动

```bash
build/bin/remoter-mcp check-config   # 校验配置，并检查每台机器的密码能否解析
build/bin/remoter-mcp serve          # MCP 端点 http://127.0.0.1:8765/mcp
```

也可以打开桌面应用，由它启动服务；关闭桌面应用后服务继续运行。

首次连接时，Windows 的 RDP 证书和 Mac 的 SSH 主机密钥会被自动信任并记录指纹（TOFU）。之后指纹变了连接会被拒绝；确认是对方重装等正常原因后，执行 `remoter-mcp trust <machine>` 重新信任。

### 6. 接入 MCP 客户端

桌面应用的“MCP 接入”卡片可以一键复制各客户端的片段（已带上 token）。手动配置：

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

设置了 `server.mcp_token` 时，客户端要带上 `Authorization: Bearer <token>` 头：

```bash
claude mcp add --transport http remoter http://127.0.0.1:8765/mcp --header "Authorization: Bearer <token>"
export REMOTER_MCP_TOKEN=<token>     # Codex 启动时从这个环境变量读取 token，需写进 ~/.zshrc 等
codex mcp add remoter --url http://127.0.0.1:8765/mcp --bearer-token-env-var REMOTER_MCP_TOKEN
```

### 7. 冒烟测试（可选）

```bash
go run ./tools/smoke -machine win1 -input -run "hostname"
go run ./tools/smoke -machine mac1 -run "sw_vers"
```

## 部署到服务器

服务不依赖桌面应用，可以跑在 Linux 服务器或 Docker 里，供多个客户端共用：

```yaml
server:
  listen: 0.0.0.0:8765
  mcp_token: ${REMOTER_MCP_TOKEN}      # 监听非本地地址时必填
  admin_token: ${REMOTER_ADMIN_TOKEN}  # 同上；管理 API（/api/v1）的 token
```

- 服务器上通常没有钥匙串，密码统一用 `.env` + `${VAR}`（见上文）。
- Docker：`docker build -t remoter-mcp -f packaging/Dockerfile .`，然后 `docker run -d -p 127.0.0.1:8765:8765 -v ~/.remoter:/home/remoter/.remoter remoter-mcp`。详见 [docs/packaging.md](docs/packaging.md)。

## MCP 工具

`list_machines` `screenshot`（含 OCR）`mouse` `keyboard` `clipboard` `run_command`（可后台执行）`job` `file` `session` `wait`，Windows 和 Mac 都用这同一套工具，详见 [docs/mcp-tools.md](docs/mcp-tools.md)。

macOS 上的差异：

- `win` 键对应 Command，`alt` 对应 Option。
- `keyboard type` 默认经剪贴板粘贴输入，中文和符号不受输入法影响（会短暂占用剪贴板）。
- `run_command` 在 SSH 登录 shell 中执行（zsh，PATH 与终端一致）；文件路径为 POSIX 风格，支持 `~`。
- 不支持改分辨率和 Ctrl+Alt+Del；截图里的光标是服务端画的，只反映经服务发出的移动。
- 屏幕共享控制的是物理屏幕，会与在场的人共用；无显示器的 Mac mini 需接 HDMI 欺骗器或虚拟显示器；开启 FileVault 的 Mac 重启后需先在本地解锁。

## CLI

```
remoter-mcp serve [--config PATH] [--listen ADDR] [--worker PATH] [--log-level LEVEL]
remoter-mcp status | stop | check-config        # 均支持 --config PATH
remoter-mcp secret set|delete <key>             # 管理钥匙串中的密码
remoter-mcp trust <machine>                     # 忘记已记录的证书 / 主机密钥指纹
remoter-mcp version
```

配置查找顺序：`--config` → `./.remoter/configs.yaml` → `~/.remoter/configs.yaml`。

## 从 rdp_mcp 升级

首次以默认路径启动 `remoter-mcp` 时，会把 `~/.rdp_mcp` 中的 `configs.yaml`、`.env`、`known_hosts` 复制到 `~/.remoter`（旧目录保留）；旧钥匙串条目（服务名 `rdp_mcp`）仍可读取。环境变量已改为 `REMOTER_MCP_*`。

## 文档

- [技术方案](docs/design.md)
- [配置说明](docs/configuration.md)
- [MCP 工具规范](docs/mcp-tools.md)
- [管理 API](docs/admin-api.md)
- [桌面应用](docs/ui.md)
- [Worker IPC 协议](docs/worker-ipc.md)
- [构建与发布](docs/packaging.md)
- [与 win-rdp-mcp 的对比](docs/comparison-win-rdp-mcp.md)
- [路线图](docs/roadmap.md)
