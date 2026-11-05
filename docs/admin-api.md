# 管理 API（v1）

- 前缀：`/api/v1`，与 MCP 的 `/mcp` 同一 HTTP 服务。
- 认证：`Authorization: Bearer <admin_token>`；WebSocket / SSE 可用 `?token=`。`admin_token` 来自配置或 `<data_dir>/run/service.json`。
- 格式：JSON；错误统一为 `{"error": {"code": "...", "message": "..."}}`，HTTP 状态码表达类别（400 参数/配置无效、401 未认证、404 不存在、409 冲突、502 远端连接失败）。
- CORS / WebSocket Origin：允许 Tauri 源（`tauri://localhost`、`http(s)://tauri.localhost`）、本地开发源（`http://localhost:*`、`http://127.0.0.1:*`）与 `server.cors_origins`。

> 实现状态（v0.4）：本文所列接口均已实现（✅）。

## 服务

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/health` | ✅ `{status, version, uptime_s}`；不需要 token |
| GET | `/service` | ✅ `{version, pid, listen, mcp_url, config_path, data_dir, managed_by(cli/ui-sidecar/launchd/systemd), started_at, worker_path}` |
| POST | `/service/shutdown` | ✅ 202，随后优雅退出 |
| GET | `/service/mcp-token` | ✅ `{mcp_token}`（空字符串表示未设置） |
| POST | `/service/mcp-token/rotate` | ✅ 生成新 token 写回 `server.mcp_token`，立即生效；返回 `{mcp_token}` |

## 机器

### 机器信息（MachineInfo）

`GET /machines`、`GET /machines/{name}` 以及多数机器操作返回：

```json
{
  "name": "win1", "description": "…", "tags": ["dev"], "host": "10.0.0.5", "port": 3389,
  "username": "administrator", "enabled": true,
  "state": "idle|connecting|connected|reconnecting|failed",
  "control_mode": "agent|human|paused",
  "width": 1920, "height": 1080,            // 仅 connected 时
  "last_error": "…", "last_activity": "RFC3339", "connected_at": "RFC3339",
  "viewers": 0,                              // 正在观看实时画面的连接数
  "pending_cert": {                          // 有待确认证书时
    "host": "…", "port": 3389, "fingerprint": "ab:cd:…", "subject": "…", "issuer": "…", "reason": "…"
  }
}
```

机器的完整配置（含分辨率、安全等选项）从 `GET /config` 的 `machines[]` 取。

### 接口

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/machines` | ✅ `MachineInfo[]`（配置顺序） |
| POST | `/machines` | ✅ 新增，body 为机器定义（见下），返回 201 + MachineInfo；重名 409 |
| GET | `/machines/{name}` | ✅ MachineInfo |
| PUT | `/machines/{name}` | ✅ **整体替换**机器定义（未提供的可选字段从配置中移除，回退到 defaults）；改名用 `new_name` |
| DELETE | `/machines/{name}?delete_secret=true` | ✅ 删除，断开会话；`delete_secret=true` 同时删除钥匙串条目 |
| PUT | `/machines/{name}/secret` | ✅ `{password}` 写入钥匙串，配置改为 `password: keychain:<name>` |
| POST | `/machines/{name}/test` | ✅ 用临时会话试连（不影响该机器现有会话，不弹证书确认）。body 可选覆盖：`{host, port, username, password, security, width, height}`（password 仅用于本次，不保存）。返回 `{ok, error?, width, height, freerdp, screenshot(base64 JPEG)}` |
| POST | `/machines/{name}/connect` · `/disconnect` · `/reconnect` | ✅ 返回 MachineInfo；连接失败 502 |
| PUT | `/machines/{name}/control` | ✅ `{mode: agent\|human\|paused}` |
| POST | `/control/emergency-stop` | ✅ 全部机器进入 `paused` |
| POST | `/control/resume` | ✅ 所有 `paused` 的机器恢复为 `agent`（`human` 接管中的不变），返回 `{status, resumed: [机器名]}` |
| GET | `/machines/{name}/screenshot?max_width=&format=png\|jpeg` | ✅ 单张截图（图片二进制，头 `X-Screen-Size: WxH`），不改变模型的坐标系 |
| POST | `/machines/{name}/cert` | ✅ `{accept: once\|always\|reject}` 回应待确认证书（见 `pending_cert` / `cert_verify` 事件）；无待确认时 409。未在 2 分钟内回应则自动拒绝 |
| POST | `/machines/{name}/trust` | ✅ 忘记已记录的证书指纹，下次连接重新信任（同 `remoter-mcp trust`） |

### 机器定义（POST / PUT body）

```json
{
  "name": "win1", "new_name": "win1-renamed",   // new_name 仅 PUT
  "description": "…", "tags": ["dev"], "enabled": true,
  "host": "10.0.0.5", "port": 3389, "username": "administrator", "domain": "CORP",
  "password": "…", "password_cmd": "…",
  "width": 1920, "height": 1080, "security": "auto|nla|tls|rdp",
  "cert": "tofu|ignore|fingerprint", "cert_fingerprint": "…",
  "auto_connect": false, "idle_disconnect": "30m", "reconnect_attempts": 5,
  "enable_drive": true, "enable_clipboard": true, "disable_wallpaper": true
}
```

密码字段语义：
- `password` 为明文：存入钥匙串（key = 机器名），配置写 `password: keychain:<name>`。
- `password` 为引用（`keychain:<key>` 或含 `${VAR}`）：原样写入配置。
- `password: ""`：清除密码；`password_cmd` 同理。
- PUT 时两者都省略：保留原有设置。改名时若密码为 `keychain:<旧名>`，钥匙串条目随之迁移到新名，配置改为 `keychain:<新名>`。

## 配置

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/config` | ✅ 当前生效配置：`{path, server, defaults, machines[]}`。`server` 中 token 只给出 `mcp_token_set`/`admin_token_set`；机器密码若为明文显示 `***`，引用原样显示 |
| PUT | `/config/server` | ✅ 部分更新，body 为 `{key: value}`，`null` 删除该键（回退默认）。允许的键：`listen` `cors_origins` `screenshot_max_width` `draw_cursor` `data_dir` `worker_path` `log_level` `audit.enabled` `audit.screenshots` `audit.retention_days` `audit.redact_typed_text` `ocr.enabled` `ocr.tesseract` `ocr.languages`。返回 `{status, restart_required}`（`listen`/`data_dir`/`worker_path`/`cors_origins` 需重启） |
| PUT | `/config/defaults` | ✅ 同上，允许的键：`port` `width` `height` `security` `cert` `auto_connect` `idle_disconnect` `reconnect_attempts` `enable_drive` `enable_clipboard` `disable_wallpaper` |
| GET | `/config/raw` | ✅ 原始 YAML（`application/yaml`） |
| PUT | `/config/raw` | ✅ body 为完整 YAML，校验通过才保存（400 返回校验错误） |

所有写操作都保留 YAML 注释，写入后立即生效并发出 `config_changed` 事件。

## 事件流（SSE）

`GET /events` ✅ → `text/event-stream`。连接建立时先推送 `snapshot`（`{machines: MachineInfo[]}`），之后：

| event | data |
|---|---|
| `machine_state` | `{machine, state, error, resolution?}` |
| `control_mode` | `{machine, mode}` |
| `tool_call` | `{id, time, phase: start\|end, tool, machine, args, duration_ms?, ok?, summary?, thumb?}`（`thumb` 为缩略图 URL 路径） |
| `cert_verify` | `{machine, host, port, fingerprint, subject, issuer, reason, pending: true}` |
| `config_changed` | `{machines, source?}` |
| `config_error` | `{error}` |
| `service` | `{status: started\|stopping, version?}` |
| `log` | `{level, msg}` |

每 20 秒发送一次注释行 `: ping` 保活。

## 实时画面（WebSocket）

`GET /machines/{name}/stream?token=&fps=10&max_width=1600&quality=70&connect=true` ✅

- `connect=false` 时不自动连接（默认会在未连接时发起连接）。
- 服务 → 客户端：
  - 二进制消息：JPEG 帧（不含鼠标指针）。只在画面变化后发送，最多 `fps` 帧/秒，无订阅者时不抓帧。
  - 文本消息：
    - `{"type":"meta","width":W,"height":H,"scale":S,"screen":[SW,SH]}`：帧尺寸变化时发送；`scale` = 帧像素 / 桌面像素。
    - `{"type":"cursor","x":X,"y":Y,"visible":bool,"shape":"<base64 PNG>","hot":[hx,hy],"size":[w,h]}`：x/y 为帧坐标；`shape`/`hot`/`size` 仅在形状变化时出现，`shape` 为空字符串表示系统默认箭头；形状位图为桌面像素尺寸，绘制时按 `scale` 缩放。
    - `{"type":"control","mode":"agent|human|paused"}`、`{"type":"state","state":"…","error":"…"}`、`{"type":"cert_verify",…}`
    - `{"type":"error","message":"…"}`
- 客户端 → 服务（仅 `control_mode == human` 时生效，否则回 `error`）：
  - `{"type":"mouse","action":"move|down|up","x":X,"y":Y,"button":"left|right|middle|x1|x2"}`（帧坐标）
  - `{"type":"mouse","action":"wheel","x":X,"y":Y,"dy":N,"dx":N}`：滚轮格数，`dy>0` 向下、`dx>0` 向右（与浏览器 `WheelEvent` 同向）
  - `{"type":"key","code":"KeyA","down":true}`（浏览器 `KeyboardEvent.code`）
  - `{"type":"text","text":"…"}`（输入法提交的文本）
  - `{"type":"clipboard","text":"…"}`（写入远端剪贴板）
- 连接断开时服务会松开该连接按下未松开的键和鼠标键。

## 审计与日志

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/audit?machine=&tool=&limit=` | ✅ 工具调用记录（最新在前，`phase=end` 的记录，含 `thumb`） |
| GET | `/audit/thumbs/{id}.jpg` | ✅ 工具结果截图的缩略图（宽 320，`server.audit.screenshots` 控制） |
| GET | `/logs?level=info&limit=500&since=RFC3339` | ✅ 服务日志末尾：`{file, lines[]}`，`level` 为最低级别 |
