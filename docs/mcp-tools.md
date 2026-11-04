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
| region | `{x,y,width,height}` | 否 | 放大查看：区域用**上一张全屏截图**的坐标表示，以不超过原始分辨率输出；不改变后续动作的坐标系，返回文本说明放大图像素与坐标的换算 |
| max_width | int | 否 | 覆盖默认缩放长边 |
| format | `png`\|`jpeg` | 否 | 默认 png |
| ocr | bool | 否 | 同时识别屏幕（或 region）上的文字，返回每行的文本、框和中心点（截图坐标，可直接点击） |
| ocr_only | bool | 否 | 与 ocr 一起用：只返回文字，不返回图片（省 token） |

返回：文本说明（图片尺寸、真实分辨率、缩放比例、光标位置）+ 图片。png 超过 3MB 自动改用 jpeg。传入 `max_width` 会改变后续动作的坐标系。

OCR 说明（design.md D16）：
- 在运行 rdp-mcp 的机器上用 tesseract 识别，Windows 端无需任何东西，锁屏或任何应用都可用。
- 对原始分辨率像素识别（再放大 2 倍提高小字准确率），坐标换算回截图坐标；region 放大时只识别该区域。
- 返回 `{"languages": "...", "lines": [{"text","x","y","w","h","center":[cx,cy],"conf"}]}`，按从上到下、从左到右排序；中文词之间不加空格。
- 语言默认自动选择：装了中文语言包时为 `chi_sim+eng`，否则 `eng`；可用 `server.ocr.languages` 指定。未安装 tesseract 时返回安装提示，截图本身照常返回。

### mouse
| 参数 | 类型 | 说明 |
|---|---|---|
| machine | string | |
| action | enum | `move` `click` `double_click` `triple_click` `right_click` `middle_click` `down` `up` `drag` `scroll` |
| x, y | int | 目标坐标（`scroll` 时为滚动位置，可省略=当前位置） |
| button | `left`\|`right`\|`middle`\|`x1`\|`x2` | `click/down/up` 时可用，默认 left |
| to_x, to_y | int | `drag` 终点 |
| direction | `up`\|`down`\|`left`\|`right` | `scroll` 方向 |
| amount | int | `scroll` 格数，默认 3 |
| modifiers | string[] | 点击/拖拽时按住的修饰键，如 `["ctrl"]` |
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
| timeout | int（秒） | 默认 60；`detach` 时默认 3600 |
| detach | bool | 后台执行，启动成功后立即返回任务 id，用 `job` 工具跟进 |

返回 `{"exit_code":..,"stdout":"..","stderr":".."}`（`detach` 时返回 `{"job": JobInfo}`）。依赖解锁的桌面，见 design.md §6.6。

安全检查：按 Win+R 后先确认屏幕上出现了运行对话框，否则直接返回错误、不输入任何字符（可能原因：会话锁定、登录界面、全屏程序或 UAC 占据焦点、策略禁用了 Win+R）。远端确认已启动后才开始计时；10 秒内未启动会换一种启动方式重试一次。

### job
| 参数 | 类型 | 说明 |
|---|---|---|
| machine | string | |
| action | `list`\|`status`\|`output`\|`cancel`\|`remove` | |
| id | string | 任务 id（`list` 以外必填） |
| max_bytes | int | `output` 时每个流返回末尾多少字节，默认 65536 |

JobInfo：`{id, machine, command, shell, state(running/done/timeout/cancelled/lost), exit_code, pid, started_at, ended_at, elapsed, timeout}`。
- `cancel`：结束任务的整个进程树（`taskkill /T /F`，经一次 `run_command` 执行，需要解锁的桌面）。
- `remove`：删除已结束任务的记录和输出文件；每台机器最多保留 50 个任务，超出时自动清理最早已结束的。
- 任务运行期间该机器不会因空闲被断开；RDP 断开后共享目录失效，任务结果无法回传，状态会变为 `lost`。
- 任务完成时事件流发出 `job` 事件（`{machine, id, state, exit_code}`）。

### file
| 参数 | 类型 | 说明 |
|---|---|---|
| machine | string | |
| action | `upload`\|`download`\|`list`\|`search` | |
| local_path | string | MCP 服务所在机器的路径；download 时为空则直接返回文本内容（≤1MB） |
| remote_path | string | Windows 路径（upload 目标可以是目录；list/search 为目录） |
| content | string | upload 时代替 local_path 直接上传文本 |
| append | bool | upload 时追加到 remote_path 末尾（不存在则创建），而不是覆盖 |
| pattern | string | search：文件名通配（`*`、`?`），在 remote_path 下递归查找，默认 `*` |
| limit | int | search：最多返回条数，默认 200，上限 1000 |
| timeout | int（秒） | 默认 120 |

search 返回 `{"entries": [{"path","type","size","last_write_time"}], "truncated": bool}`。

`file` 基于 `run_command` 实现，同样要求桌面已解锁；暂不支持目录下载。

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
| until_stable | bool | 等到屏幕在 `stable_ms`（默认 500ms）内无变化，`seconds` 作为上限（默认 10） |
| stable_ms | int | 稳定判定时长 |
| screenshot_after | bool | 默认 true |
