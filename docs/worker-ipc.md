# Worker IPC 协议（v1）

主服务（Go，`internal/worker`）与 `remoter-worker`（C，`worker/src`）之间通过 Unix domain socket 通信。主服务创建监听 socket，启动 worker：`remoter-worker --ipc /path/to.sock`，worker 主动连接后立即发送 `hello` 事件。

每次连接（含重连）都启动新的 worker 进程；RDP 断开后 worker 上报 `disconnected` 并退出。

## 帧格式

每帧：`4 字节大端长度 L` + `1 字节类型 T` + `L-1 字节负载`。

| T | 含义 | 负载 |
|---|---|---|
| `J` (0x4A) | JSON 消息 | UTF-8 JSON |
| `F` (0x46) | 像素数据 | 头 16 字节（`u32 width, u32 height, u32 stride, u32 req_id`，大端）+ BGRA 像素 |

`F` 帧总是紧跟在对应请求（`req_id` = 请求 id）的 JSON 响应之前发送。

JSON 中的整数一律以整数形式编码（不能是 `1.0`），Go 侧按 `int` 解码。

## 消息

请求（主服务 → worker）：`{"id": <int>, "op": "<name>", ...参数}`
响应（worker → 主服务）：`{"id": <int>, "ok": true, ...结果}` 或 `{"id": <int>, "ok": false, "error": "...", "code": <freerdp 错误码，可选>}`
事件（worker → 主服务，无 id）：`{"event": "<name>", ...}`

### 请求

| op | 参数 | 结果 |
|---|---|---|
| `connect` | `host, port, username, password, domain, width, height, security(auto/nla/tls/rdp), cert(ignore 时跳过校验), clipboard, disable_wallpaper, connect_timeout_ms, config_dir, drive_path?, drive_name?` | `{width,height}`；失败时 worker 随后退出 |
| `disconnect` | — | 回复后断开并退出 |
| `status` | — | `{connected, width, height, dirty_seq, idle_ms}`（`idle_ms` = 距上次画面变化的毫秒数，-1 表示尚无变化） |
| `capture` | — | 先 `F` 帧（整屏），再 `{width,height,dirty_seq}` |
| `cursor_get` | `shape: bool` | `{x,y,visible,hot_x,hot_y,width,height,shape_seq,has_shape}`；`shape` 为真且有自定义形状时先发 `F` 帧（指针 BGRA，含 alpha） |
| `subscribe` | `on: bool` | 开关 `frame_dirty` 事件 |
| `mouse` | `flags, x, y`（真实坐标，`PTR_FLAGS_*`） | — |
| `mouse_ex` | `flags, x, y`（`PTR_XFLAGS_*`，X1/X2 键） | — |
| `key` | `scancode, extended, down, repeat?` | — |
| `unicode` | `text`（UTF-8；`\n`→Enter，`\t`→Tab，`\r` 忽略） | — |
| `clipboard_get` | `timeout_ms` | `{text}`；远端剪贴板无文本时 `{text:"", empty:true}`（异步：worker 在收到数据后回复） |
| `clipboard_set` | `text` | — |
| `resize` | `width, height` | —（服务器应用后发 `resized` 事件） |
| `cert_reply` | `accept: 0 拒绝 / 非 0 接受（仅本次）` | — |

### 事件

| event | 字段 | 说明 |
|---|---|---|
| `hello` | `version, freerdp, protocol` | 连接 IPC 后第一条消息；`protocol` 不一致时主服务拒绝使用 |
| `cert_verify` | `host, port, common_name, subject, issuer, fingerprint, changed, mismatch` | worker 阻塞等待 `cert_reply`；期间其他请求返回 busy |
| `disconnected` | `code, reason` | RDP 断开，worker 随即退出 |
| `resized` | `width, height` | 远端分辨率变化 |
| `frame_dirty` | `dirty_seq` | 仅在 `subscribe` 开启时发送，节流 ≤30 次/秒 |
| `cursor` | `x, y, visible, shape_seq` | 指针位置/形状变化（节流） |

## 线程模型

worker 主线程用 `WaitForMultipleObjects` 同时等待 RDP 事件句柄、IPC socket 和一个唤醒事件，所有 IPC 写和 FreeRDP 调用都在主线程。GFX 解码和剪贴板回调可能在其他线程执行，它们只更新加锁的状态并触发唤醒事件。IPC 连接断开时 worker 立即断开 RDP 并退出。
