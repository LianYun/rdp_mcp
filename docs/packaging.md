# 构建与发布

## 1. 依赖

| 组件 | 依赖 |
|---|---|
| rdp-worker | C 编译器、CMake ≥ 3.13、pkg-config、FreeRDP 3.x（`freerdp3`、`freerdp-client3`、`winpr3`） |
| rdp-mcp | Go（以 `go.mod` 声明版本为准） |
| Desktop | Rust stable、Node.js LTS + pnpm、Tauri CLI 2；Linux 另需 `libwebkit2gtk-4.1-dev`、`libayatana-appindicator3-dev` 等 |

安装：
- macOS：`brew install freerdp cmake pkg-config go node pnpm rust`
- Debian 13 / Ubuntu 24.04+：`apt install build-essential cmake pkg-config freerdp3-dev golang nodejs`
- 其他 Linux：源码构建最小化 FreeRDP：
  ```
  cmake -B build -DWITH_CLIENT=ON -DWITH_CLIENT_COMMON=ON -DWITH_X11=OFF -DWITH_CLIENT_SDL=OFF \
        -DWITH_SERVER=OFF -DWITH_FFMPEG=OFF -DWITH_FUSE=OFF -DWITH_WAYLAND=OFF -DWITH_MANPAGES=OFF
  ```

## 2. Make 目标

| 目标 | 说明 |
|---|---|
| `make worker` | 构建 `build/bin/rdp-worker` |
| `make service` | 构建 `build/bin/rdp-mcp` |
| `make app` | 构建 worker + service，复制到 `app/src-tauri/binaries/<name>-<target-triple>`，再 `pnpm tauri build` |
| `make test` | Go 测试 + worker 测试 + 前端测试 |
| `make dist` | 生成发布包 |
| `make dev` | 并行启动服务（开发配置）与 `pnpm tauri dev` |

## 3. 发布产物

| 产物 | 平台 | 内容 |
|---|---|---|
| CLI 压缩包 `rdp-mcp-<ver>-<os>-<arch>.tar.gz` | macOS arm64/x64、Linux x64/arm64 | `rdp-mcp`、`rdp-worker`；macOS 版附带 FreeRDP dylib（见下） |
| Docker 镜像 | linux/amd64、linux/arm64 | Debian slim + `freerdp3` 运行库 + 两个二进制；`/data` 挂载 `~/.rdp_mcp` |
| Desktop | macOS `.dmg`（universal 或分架构）、Linux `.AppImage` / `.deb` | 内置 sidecar |

## 4. FreeRDP 动态库处理

- **macOS**：Homebrew 的 FreeRDP 不能假设用户已安装。构建时用 `dylibbundler`（或脚本 + `install_name_tool`）把 `libfreerdp3`、`libfreerdp-client3`、`libwinpr3` 及其依赖（OpenSSL 等）复制到 `lib/` 并改写为 `@executable_path/../lib/...`；Tauri 包中放入 `Contents/Frameworks` 并相应改写。签名与公证覆盖所有 dylib。
- **Linux .deb**：声明依赖 `libfreerdp3-3, libfreerdp-client3-3, libwinpr3-3`。
- **Linux AppImage / CLI 压缩包**：使用源码最小构建的 FreeRDP 并打包进 `lib/`，`rpath=$ORIGIN/../lib`。
- 静态链接 FreeRDP 作为后续可选优化（`-DBUILD_SHARED_LIBS=OFF`）。

## 5. 服务化部署

- macOS：`rdp-mcp service install` 生成 `~/Library/LaunchAgents/com.lianyun.rdp-mcp.plist`。
- Linux：生成 `~/.config/systemd/user/rdp-mcp.service`，提示 `loginctl enable-linger` 以便无登录时运行。
- 模板放在 `packaging/`。

## 6. CI（GitHub Actions）

| Job | 内容 |
|---|---|
| lint | `go vet`、`golangci-lint`、`clang-format --dry-run`、`eslint`、`cargo clippy` |
| test | Linux + macOS 矩阵：Go 单测、worker 单测、前端单测 |
| build | 两平台构建 CLI 包与 Desktop 包（artifacts） |
| integration（手动/夜间） | 连接配置的 Windows 测试机跑集成测试（secrets 提供凭据） |
| release（tag） | 签名、公证、发布 GitHub Release、推送 Docker 镜像 |
