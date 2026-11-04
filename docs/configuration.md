# 配置说明

## 查找顺序

1. 命令行 `--config <path>`
2. 当前目录 `./.rdp_mcp/configs.yaml`
3. 用户目录 `~/.rdp_mcp/configs.yaml`（不存在时服务以空机器列表启动，并在此创建）

服务是配置的唯一写入方：UI 通过管理 API 修改后由服务写回（保留注释）。手动编辑文件也会被自动热加载，校验失败时保留旧配置。

## 字段

### server
| 字段 | 默认 | 需重启 | 说明 |
|---|---|---|---|
| listen | `127.0.0.1:8765` | 是 | HTTP 监听地址；MCP 端点 `/mcp`，管理 API `/api/v1` |
| mcp_token | 空 | 否 | MCP 客户端 Bearer token；监听非本地地址时必填 |
| admin_token | 空 | 是 | 管理 API token；为空则每次启动随机生成并写入 `run/service.json`；监听非本地地址时必填 |
| cors_origins | `[]` | 是 | 除 Tauri 外额外允许访问管理 API 的源 |
| screenshot_max_width | 1280 | 否 | 截图缩放后的长边 |
| draw_cursor | true | 否 | 截图中合成鼠标指针 |
| data_dir | `~/.rdp_mcp` | 是 | 运行时文件、共享目录、known_hosts、日志 |
| worker_path | 自动 | 是 | `rdp-worker` 路径；默认依次查找：与 `rdp-mcp` 同目录、`PATH` |
| log_level | `info` | 否 | `debug`/`info`/`warn`/`error` |
| audit.enabled | true | 否 | 记录工具调用 |
| audit.screenshots | true | 否 | 审计保存操作前后缩略图 |
| audit.retention_days | 7 | 否 | |
| audit.redact_typed_text | false | 否 | 审计中隐藏 `keyboard.type` 的文本 |

### defaults（被每台机器继承，机器上同名字段覆盖）
| 字段 | 默认 | 说明 |
|---|---|---|
| port | 3389 | |
| width / height | 1920 / 1080 | 远程桌面分辨率 |
| security | `auto` | `nla` / `tls` / `rdp` / `auto` |
| cert | `tofu` | `tofu` / `ignore` / `fingerprint`（需 `cert_fingerprint`） |
| auto_connect | false | true 启动即连接；false 首次使用时连接 |
| idle_disconnect | `30m` | 空闲断开；`0` 不断开（UI 观看中不计空闲） |
| reconnect_attempts | 5 | |
| enable_drive | true | 映射共享目录（`run_command`/`file` 依赖） |
| enable_clipboard | true | |
| enable_remoteapp | false | 服务器允许 RemoteApp 时，用 rail 执行命令 |

### machines[]
| 字段 | 必填 | 说明 |
|---|---|---|
| name | 是 | 唯一名，工具中的 `machine` 参数；字母数字、`-`、`_` |
| host | 是 | 主机名或 IP |
| username | 是 | |
| password | 否 | 三种形式：`keychain:<key>`（系统钥匙串，推荐）、`${ENV}`、明文（不推荐） |
| password_cmd | 否 | 执行命令取 stdout 作为密码；与 `password` 二选一 |
| domain | 否 | |
| description | 否 | 给模型看的说明，建议写清用途 |
| tags | 否 | 分组标签 |
| enabled | 否 | 默认 true；false 时对 MCP 不可见 |
| cert_fingerprint | 否 | `cert: fingerprint` 时使用 |
| 以及 defaults 中任意字段 | 否 | 覆盖默认值 |

钥匙串条目：service 名 `rdp_mcp`，account 为 `<key>`。可用 `rdp-mcp secret set <name>` 或 UI 写入。无 Secret Service 的 Linux 服务器请使用 `${ENV}` 或 `password_cmd`。

完整示例见 [`examples/configs.yaml`](../examples/configs.yaml)。
