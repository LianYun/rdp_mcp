# MCP 工具规范

> 传输：Streamable HTTP，端点 `http://<listen>/mcp`。所有工具（除 `list_machines`）第一个参数为 `machine`（配置中的机器名）。
> 坐标一律使用**最近一次截图的坐标系**，服务端负责换算到真实分辨率（见 design.md §4.3）。

## 通用约定

- 动作类工具（`mouse`、`keyboard`）默认在动作完成、屏幕稳定（最多等待约 300ms）后附带截图；传 `screenshot_after: false` 可关闭。
- 机器未连接时，工具调用会按需自动连接。
- 认证：配置了 `mcp_token` 时需 `Authorization: Bearer <mcp_token>`。
- 控制模式：机器处于 `human`（人工接管）或 `paused` 时，除 `list_machines`、`screenshot`、`session status` 外的工具返回错误"机器正在被人工接管/已暂停"。`enabled: false` 的机器不出现在列表中。
- 截图默认合成鼠标指针（`server.draw_cursor`）。
- 错误以 MCP 工具错误返回（`isError: true`），文本说明原因与建议（如"会话已锁定，请先截图确认"）。

## 工具列表

### list_machines
无参数。返回每台机器：`name`、`description`、`tags`、`state`（idle/connecting/connected/reconnecting/failed）、`control_mode`（agent/human/paused）、`resolution`。

### screenshot
| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| machine | string | 是 | |
| region | `{x,y,width,height}` | 否 | 截图坐标系下的裁剪区域 |
| max_width | int | 否 | 覆盖默认缩放长边 |
| format | `png`\|`jpeg` | 否 | 默认 png |

返回：图片 + 文本 `{"width":..,"height":..,"scale":..,"screen":[w,h]}`。

### mouse
| 参数 | 类型 | 说明 |
|---|---|---|
| machine | string | |
| action | enum | `move` `click` `double_click` `right_click` `middle_click` `down` `up` `drag` `scroll` |
| x, y | int | 目标坐标（`scroll` 时为滚动位置，可省略=当前位置） |
| button | `left`\|`right`\|`middle`\|`x1`\|`x2` | `click/down/up` 时可用，默认 left |
| to_x, to_y | int | `drag` 终点 |
| direction | `up`\|`down`\|`left`\|`right` | `scroll` 方向 |
| amount | int | `scroll` 格数，默认 3 |
| modifiers | string[] | 点击时按住的修饰键，如 `["ctrl"]` |
| screenshot_after | bool | 默认 true |

### keyboard
| 参数 | 类型 | 说明 |
|---|---|---|
| machine | string | |
| action | enum | `type` `press` `hotkey` `down` `up` |
| text | string | `type`：任意 Unicode 文本（换行发送 Enter） |
| keys | string[] | `press`：单键 `["enter"]`；`hotkey`：组合键 `["ctrl","shift","esc"]`；`down/up`：要按住/松开的键 |
| repeat | int | `press` 重复次数 |
| screenshot_after | bool | 默认 true |

键名：`ctrl` `shift` `alt` `win` `enter` `esc` `tab` `backspace` `delete` `insert` `home` `end` `pageup` `pagedown` `up` `down` `left` `right` `space` `f1`…`f24` `capslock` `printscreen` `menu` `a`…`z` `0`…`9` 以及符号键。

### clipboard
| 参数 | 类型 | 说明 |
|---|---|---|
| machine | string | |
| action | `get`\|`set` | |
| text | string | `set` 时必填 |

### run_command
| 参数 | 类型 | 说明 |
|---|---|---|
| machine | string | |
| command | string | 命令或脚本内容 |
| shell | `powershell`\|`cmd` | 默认 powershell |
| timeout | int（秒） | 默认 60 |

返回 `{"exit_code":..,"stdout":"..","stderr":".."}`。依赖解锁的桌面，见 design.md §4.4。

### file
| 参数 | 类型 | 说明 |
|---|---|---|
| machine | string | |
| action | `upload`\|`download`\|`list` | |
| local_path | string | 本机路径 |
| remote_path | string | Windows 路径 |

### session
| 参数 | 类型 | 说明 |
|---|---|---|
| machine | string | |
| action | enum | `connect` `disconnect` `reconnect` `status` `set_resolution` `send_ctrl_alt_del` |
| width, height | int | `set_resolution` 时使用 |

### wait
| 参数 | 类型 | 说明 |
|---|---|---|
| machine | string | |
| seconds | number | 固定等待 |
| until_stable | bool | 等到屏幕在 `stable_ms`（默认 500ms）内无变化，`seconds` 作为上限 |
| screenshot_after | bool | 默认 true |
