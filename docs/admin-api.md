# 管理 API（v1）

- 前缀：`/api/v1`，与 MCP 的 `/mcp` 同一 HTTP 服务。
- 认证：`Authorization: Bearer <admin_token>`；WebSocket / SSE 可用 `?token=`。`admin_token` 来自配置或 `~/.rdp_mcp/run/service.json`。
- 格式：JSON；错误统一为 `{"error": {"code": "...", "message": "..."}}`，HTTP 状态码表达类别。
- CORS：仅允许 Tauri 源、本地开发源（`http://localhost:*`）与 `server.cors_origins` 配置的源。

> 实现状态（v0.3）：标 ✅ 的接口已实现，其余随 UI 里程碑（U1–U4）实现。

## 服务

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/health` | ✅ `{status, version, uptime_s}`；不需要 token |
| GET | `/service` | ✅ 服务信息：版本、pid、监听地址、配置文件路径、MCP 地址、数据目录、运行方式（cli/ui-sidecar/launchd/systemd） |
| POST | `/service/shutdown` | ✅ 优雅退出 |
| GET | `/service/mcp-token` | ✅ 返回 MCP token（用于 UI 生成客户端接入片段） |
| POST | `/service/mcp-token/rotate` | 重新生成 MCP token 并写回配置 |

## 机器

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/machines` | ✅ 列表：配置 + 运行状态（state、control_mode、resolution、last_error、last_activity） |
| POST | `/machines` | 新增（body 同配置字段，可带 `password`，服务转存钥匙串） |
| GET | `/machines/{name}` | ✅ 详情 |
| PUT | `/machines/{name}` | 修改（改名用 `new_name`） |
| DELETE | `/machines/{name}` | 删除（同时断开会话、可选删除钥匙串条目） |
| PUT | `/machines/{name}/secret` | `{password}` 写入钥匙串，配置改为 `password: keychain:<name>` |
| POST | `/machines/{name}/test` | 用给定或已保存参数试连接，返回成功与否 + 截图（base64 JPEG） |
| POST | `/machines/{name}/connect` · `/disconnect` · `/reconnect` | ✅ 会话操作 |
| PUT | `/machines/{name}/control` | ✅ `{mode: agent\|human\|paused}` |
| POST | `/control/emergency-stop` | ✅ 全部机器进入 `paused` |
| GET | `/machines/{name}/screenshot?max_width=&format=` | ✅ 单张截图（图片二进制） |
| POST | `/machines/{name}/cert` | `{accept: once\|always\|reject}` 回应证书确认 |
| POST | `/machines/{name}/trust` ✅ | 忘记已记录的证书指纹，下次连接重新信任（同 `rdp-mcp trust`） |

## 配置

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/config` | 当前生效配置（密码字段脱敏） |
| PUT | `/config/server` · `/config/defaults` | 修改服务/默认项；返回 `restart_required` |
| GET | `/config/raw` / PUT | 原始 YAML 读写（高级模式），保存前校验 |

## 事件流（SSE）

`GET /events` ✅ → `text/event-stream`，事件名即类型：

```
event: machine_state
data: {"machine":"dev-pc","state":"connected","resolution":[1920,1080]}

event: tool_call
data: {"id":"tc_123","machine":"dev-pc","tool":"mouse","phase":"end","args":{...},"duration_ms":420,"ok":true,"thumb":"/api/v1/audit/thumbs/tc_123.jpg"}
```

类型：`service`、`machine_state`、`control_mode`、`tool_call`、`config_changed`、`config_error`、`cert_verify`、`log`。连接建立时先推送一次全量快照 `snapshot`。

## 实时画面（WebSocket）

`GET /machines/{name}/stream?token=&fps=10&max_width=1600`

- 服务 → 客户端：
  - 二进制消息：JPEG 帧；
  - 文本消息：`{"type":"meta","width":..,"height":..,"scale":..}`、`{"type":"cursor","x":..,"y":..,"shape":"<base64 png>","hot":[x,y]}`、`{"type":"control","mode":"human"}`。
- 客户端 → 服务（仅 `control_mode == human` 时生效，否则忽略并回 `{"type":"error"}`）：
  - `{"type":"mouse","action":"move|down|up|wheel","x":..,"y":..,"button":"left","delta":..}`（帧坐标）
  - `{"type":"key","code":"KeyA","down":true}`（浏览器 `KeyboardEvent.code`，服务映射扫描码）
  - `{"type":"text","text":"..."}`（输入法提交文本）
  - `{"type":"clipboard","text":"..."}`
- 无订阅者时服务不抓帧；帧只在 `frame_dirty` 后推送，静止画面不占带宽。

## 审计

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/audit?machine=&tool=&limit=` ✅ | 工具调用记录（最新在前） |
| GET | `/audit/thumbs/{id}.jpg` | 缩略图 |
| GET | `/logs?level=&since=` | 服务日志 |
