# 配置说明

## 查找顺序

1. 命令行 `--config <path>`
2. 当前目录 `./.rdp_mcp/configs.yaml`
3. 用户目录 `~/.rdp_mcp/configs.yaml`

## 字段

### server
| 字段 | 默认 | 说明 |
|---|---|---|
| listen | `127.0.0.1:8765` | MCP HTTP 监听地址，端点为 `/mcp` |
| auth_token | 空 | 设置后要求 `Authorization: Bearer <token>`；监听非本地地址时必填 |
| screenshot_max_width | 1280 | 截图缩放后的长边 |
| data_dir | `~/.rdp_mcp` | 共享目录、known_hosts、日志所在目录 |
| log_level | `info` | `debug`/`info`/`warn`/`error` |
| audit_log | true | 记录工具调用审计日志 |

### defaults（被每台机器继承，机器上同名字段覆盖）
| 字段 | 默认 | 说明 |
|---|---|---|
| port | 3389 | |
| width / height | 1920 / 1080 | 远程桌面分辨率 |
| security | `auto` | `nla` / `tls` / `rdp` / `auto` |
| cert | `tofu` | `tofu`（首次信任并记录）/ `ignore` / `fingerprint`（需 `cert_fingerprint`） |
| auto_connect | false | true 启动即连接；false 首次使用时连接 |
| idle_disconnect | `30m` | 空闲多久自动断开，`0` 表示不断开 |
| reconnect_attempts | 5 | 断线自动重连次数 |
| enable_drive | true | 是否映射共享目录（`run_command`/`file` 依赖） |
| enable_clipboard | true | |

### machines[]
| 字段 | 必填 | 说明 |
|---|---|---|
| name | 是 | 唯一名，工具中的 `machine` 参数 |
| host | 是 | 主机名或 IP |
| username | 是 | |
| password / password_cmd | 二选一 | `password` 支持 `${ENV}`；`password_cmd` 执行命令取 stdout |
| domain | 否 | |
| description | 否 | 给模型看的说明，建议写清用途 |
| tags | 否 | 分组标签 |
| cert_fingerprint | 否 | `cert: fingerprint` 时使用 |
| 以及 defaults 中任意字段 | 否 | 覆盖默认值 |

完整示例见 [`examples/configs.yaml`](../examples/configs.yaml)。
