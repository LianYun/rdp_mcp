# 配置说明

## 快速开始

创建 `~/.rdp_mcp/configs.yaml`（全局）或在项目目录下创建 `./.rdp_mcp/configs.yaml`，最小配置：

```yaml
machines:
  - name: win1                 # 机器名，模型调用工具时用它
    description: "办公电脑"     # 给模型看的说明
    host: 192.168.1.20         # IP 或主机名
    username: alice            # Windows 登录用户名
    password: ${WIN1_PASSWORD} # 推荐引用环境变量或钥匙串，见下文
```

然后运行 `rdp-mcp check-config` 检查，`rdp-mcp serve` 启动。

## 查找顺序

1. 命令行 `--config <path>`
2. 当前目录 `./.rdp_mcp/configs.yaml`
3. 用户目录 `~/.rdp_mcp/configs.yaml`（文件不存在时服务以空机器列表启动）

服务每 2 秒检查一次配置文件，修改后自动热加载；校验失败时保留旧配置并记录错误。`server.listen`、`server.data_dir` 修改后需重启服务。

配置中出现未知字段会报错（防止拼写错误被静默忽略）。

## 密码的四种写法

| 写法 | 示例 | 说明 |
|---|---|---|
| 系统钥匙串（推荐） | `password: keychain:win1` | 先执行 `rdp-mcp secret set win1` 输入密码；macOS 用 Keychain，Linux 用 Secret Service |
| 环境变量 | `password: ${WIN1_PASSWORD}` | 在环境中 `export`，或写入 `.env` 文件（见下） |
| 命令 | `password_cmd: "pass show rdp/win1"` | 执行命令，取标准输出作为密码 |
| 明文（不推荐） | `password: P@ssw0rd` | `check-config` 会警告 |

无图形会话的 Linux 服务器通常没有 Secret Service，请用环境变量或 `password_cmd`。

### .env 文件

服务启动时依次读取配置文件同目录的 `.env` 和 `data_dir` 下的 `.env`（通常都是 `~/.rdp_mcp/.env`），格式 `KEY=VALUE`（支持 `#` 注释、`export ` 前缀和引号）。进程环境里已有的同名变量优先。修改 `.env` 后需重启服务。文件权限过宽时会警告，建议 `chmod 600 ~/.rdp_mcp/.env`。

## 字段

### server
| 字段 | 默认 | 需重启 | 说明 |
|---|---|---|---|
| listen | `127.0.0.1:8765` | 是 | HTTP 监听地址；MCP 端点 `/mcp`，管理 API `/api/v1` |
| mcp_token | 空 | 否 | MCP 客户端 Bearer token，支持 `${ENV}`；监听非本地地址时必填 |
| admin_token | 空 | 是 | 管理 API token；为空则每次启动随机生成并写入 `<data_dir>/run/service.json`；监听非本地地址时必填 |
| cors_origins | `[]` | 是 | 除 Tauri 与本地开发服务器外，额外允许访问管理 API 的源 |
| screenshot_max_width | 1280 | 否 | 截图缩放后的长边像素 |
| draw_cursor | true | 否 | 截图中合成鼠标指针 |
| data_dir | `~/.rdp_mcp` | 是 | 运行时文件、共享目录、known_hosts、日志、审计 |
| worker_path | 自动 | 是 | `rdp-worker` 路径；默认依次查找：与 `rdp-mcp` 同目录、`../worker/build/`、`PATH` |
| log_level | `info` | 否 | `debug`/`info`/`warn`/`error`；`debug` 时 worker 也输出 FreeRDP 调试日志 |
| audit.enabled | true | 是 | 记录工具调用到 `<data_dir>/audit/YYYY-MM-DD.jsonl` |
| audit.retention_days | 7 | 是 | 审计日志保留天数 |
| audit.redact_typed_text | false | 是 | 审计中隐藏 `text`/`content`/`command` 参数 |
| audit.screenshots | true | — | 预留：审计保存截图缩略图（未实现） |

### defaults（被每台机器继承，机器上同名字段覆盖）
| 字段 | 默认 | 说明 |
|---|---|---|
| port | 3389 | |
| width / height | 1920 / 1080 | 远程桌面分辨率（200–8192） |
| security | `auto` | `auto` / `nla` / `tls` / `rdp` |
| cert | `tofu` | `tofu`：首次连接信任并记录指纹，之后指纹变化则拒绝；`ignore`：不校验；`fingerprint`：必须等于 `cert_fingerprint` |
| auto_connect | false | true 服务启动即连接；false 首次使用时连接 |
| idle_disconnect | `30m` | 空闲多久自动断开；`"0"` 表示不断开 |
| reconnect_attempts | 5 | 意外断线后的自动重连次数（指数退避，最长 32s 间隔） |
| enable_drive | true | 映射本地共享目录为 `\\tsclient\mcp`（`run_command`/`file` 依赖） |
| enable_clipboard | true | 剪贴板通道 |
| disable_wallpaper | true | 关闭桌面壁纸，减少截图噪声与带宽 |

### machines[]
| 字段 | 必填 | 说明 |
|---|---|---|
| name | 是 | 唯一名，`[A-Za-z0-9_-]`，工具中的 `machine` 参数 |
| host | 是 | 主机名或 IP |
| username | 是 | Windows 用户名（域账号可写 `domain` 字段，或 `user@domain.com` 形式） |
| password / password_cmd | 建议 | 见上文 |
| domain | 否 | |
| description | 否 | 给模型看的说明，建议写清用途 |
| tags | 否 | 分组标签 |
| enabled | 否 | 默认 true；false 时对 MCP 不可见 |
| cert_fingerprint | 否 | `cert: fingerprint` 时使用，格式 `AB:CD:...` 或 `sha256:AB:CD:...` |
| 以及 defaults 中任意字段 | 否 | 覆盖默认值 |

## 证书

- 指纹记录在 `<data_dir>/known_hosts`。
- Windows 重装或证书更换后连接会被拒绝，错误信息里有新指纹。确认无误后执行 `rdp-mcp trust <machine>`，下次连接重新信任。

## 运行时目录（data_dir）

```
~/.rdp_mcp/
  configs.yaml
  known_hosts            # 证书指纹
  run/service.json       # 运行中服务的地址、PID、admin_token（0600）
  run/rdp-mcp.lock       # 单实例锁
  shares/<machine>/      # 映射为远端 \\tsclient\mcp
  logs/rdp-mcp.log       # 服务日志
  logs/worker-<machine>.log
  audit/YYYY-MM-DD.jsonl
  freerdp/               # FreeRDP 自身配置目录
```

完整示例见 [`examples/configs.yaml`](../examples/configs.yaml)。
