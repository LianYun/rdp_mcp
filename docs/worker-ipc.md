# Worker IPC 协议（v1）

主服务（Go）与 `rdp-worker`（C）之间通过 Unix domain socket 通信。主服务创建监听 socket，启动 worker 时传入路径：`rdp-worker --ipc /path/to.sock`，worker 主动连接。

## 帧格式

每帧：`4 字节大端长度 L` + `1 字节类型 T` + `L-1 字节负载`。

| T | 含义 | 负载 |
|---|---|---|
| `J` (0x4A) | JSON 消息 | UTF-8 JSON |
| `F` (0x46) | 帧缓冲 | 头 16 字节（`u32 width, u32 height, u32 stride, u32 req_id`，大端）+ BGRA 像素 |

## 消息

请求（主服务 → worker）：`{"id": <int>, "op": "<name>", ...参数}`
响应（worker → 主服务）：`{"id": <int>, "ok": true, ...结果}` 或 `{"id": <int>, "ok": false, "error": "..."}`
事件（worker → 主服务，无 id）：`{"event": "<name>", ...}`

### 请求

| op | 参数 | 结果 |
|---|---|---|
| `connect` | `host, port, username, password, domain, width, height, security, drive_path?, drive_name?, clipboard` | `{width,height}` |
| `disconnect` | — | — |
| `capture` | — | 先回 `F` 帧（req_id=id），再回 JSON `{dirty_seq}` |
| `status` | — | `{connected, width, height, dirty_seq, last_change_ms}` |
| `mouse` | `flags, x, y`（真实坐标，`flags` 为 RDP PTR_FLAGS_*） | — |
| `mouse_ex` | `flags, x, y`（PTR_XFLAGS_*） | — |
| `key` | `scancode, down, extended` | — |
| `unicode` | `text`（worker 逐字符发送 down/up，处理代理对） | — |
| `clipboard_get` | `timeout_ms` | `{text}` |
| `clipboard_set` | `text` | — |
| `resize` | `width, height` | — |
| `cert_reply` | `accept: 0 拒绝 / 1 永久 / 2 本次` | — |
| `subscribe` | `on: bool` | 开启/关闭 `frame_dirty` 事件推送（UI 实时画面需要） |
| `cursor_get` | — | `{x, y, visible, hot_x, hot_y}`，若形状有变化再回一个 `F` 帧（指针位图） |

### 事件

| event | 字段 | 说明 |
|---|---|---|
| `connected` | `width, height` | |
| `disconnected` | `reason` | 主服务决定是否重连 |
| `cert_verify` | `host, port, subject, issuer, fingerprint, changed` | worker 阻塞等待 `cert_reply` |
| `resized` | `width, height` | 服务器侧分辨率变化 |
| `frame_dirty` | `dirty_seq, rects` | 仅在 `subscribe` 开启时发送，合并节流（≤30 次/秒） |
| `cursor` | `x, y, visible, shape_seq` | 指针位置/形状变化（节流） |
| `log` | `level, msg` | 转发 worker 日志 |

## 线程模型

worker 单线程：RDP 事件句柄与 IPC socket 一起等待，所有 FreeRDP 调用都在同一线程执行。IPC 连接断开时 worker 立即断开 RDP 并退出。
