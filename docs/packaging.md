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
| `make app` | 构建 worker + service，复制到 `app/src-tauri/binaries/<name>-<target-triple>`，macOS 打包动态库，再 `pnpm tauri build`（详见 §6） |
| `make app-sidecars` | 只准备 sidecar 与动态库，不执行 tauri build（`cargo check` / `pnpm tauri dev` 之前使用） |
| `make test` | Go 测试 + worker 测试 + 前端测试 |
| `make dist` | 生成发布包 |
| `make dev` | 并行启动服务（开发配置）与 `pnpm tauri dev` |

## 3. 发布产物

| 产物 | 平台 | 内容 |
|---|---|---|
| CLI 压缩包 `rdp-mcp-<ver>-<os>-<arch>.tar.gz` | macOS arm64/x64、Linux x64/arm64 | `rdp-mcp`、`rdp-worker`；macOS 版附带 FreeRDP dylib（见下） |
| Docker 镜像 | linux/amd64、linux/arm64 | Debian slim + `freerdp3` 运行库 + tesseract + 两个二进制；挂载 `~/.rdp_mcp` 到 `/home/rdpmcp/.rdp_mcp` |
| Desktop | macOS `.dmg`（universal 或分架构）、Linux `.AppImage` / `.deb` | 内置 sidecar |

## 4. FreeRDP 动态库处理

- **macOS**：Homebrew 的 FreeRDP 不能假设用户已安装。`scripts/bundle-macos-libs.py`（不依赖 `dylibbundler`）递归解析 `rdp-worker` 的非系统依赖（`libfreerdp3`、`libfreerdp-client3`、`libwinpr3`、OpenSSL 等），平铺复制到 `app/src-tauri/frameworks/`，把每个库的 id 与依赖改为 `@rpath/<name>`（库自身加 rpath `@loader_path`，worker 加 rpath `@executable_path/../Frameworks`），再 ad-hoc 重签名（`install_name_tool` 会使签名失效，arm64 上未签名的二进制会被系统杀掉）。脚本最后校验没有任何文件仍指向 `/opt/homebrew` 等非系统路径，并生成 `tauri.macos.conf.json`（`bundle.macOS.files` 把这些库放进 `Contents/Frameworks`）。

  **OpenSSL provider 模块**：`libcrypto` 在运行时按编译进去的路径（Homebrew 的 Cellar 目录）`dlopen` `ossl-modules/legacy.dylib`（WinPR 的 NTLM 需要其中的 MD4，没有它 NLA 登录会失败）。这个文件不在 `otool -L` 的依赖里，脚本会把它连同依赖一起放进 `Frameworks/ossl-modules/`；运行时必须设置 `OPENSSL_MODULES=<App>.app/Contents/Frameworks/ossl-modules`——桌面应用启动 sidecar 时由 Rust 侧设置（`service.rs`）。**CLI 压缩包（macOS 版）也有同样的需求**：需要由启动脚本设置该环境变量，或让 `rdp-worker` 启动时按自身路径设置（待办）。正式发布时签名与公证需覆盖所有 dylib。
- **Linux .deb**：声明依赖 `libfreerdp3-3, libfreerdp-client3-3, libwinpr3-3`。
- **Linux AppImage / CLI 压缩包**：使用源码最小构建的 FreeRDP 并打包进 `lib/`，`rpath=$ORIGIN/../lib`。
- 静态链接 FreeRDP 作为后续可选优化（`-DBUILD_SHARED_LIBS=OFF`）。

## 5. Docker

```bash
docker build -t rdp-mcp -f packaging/Dockerfile .
docker run -d -p 127.0.0.1:8765:8765 -v ~/.rdp_mcp:/home/rdpmcp/.rdp_mcp rdp-mcp
```

- 两阶段构建：Debian trixie 编译 worker 与服务，运行镜像只含 `libfreerdp3`、`tesseract-ocr`（含中文语言包）和两个二进制，以非 root 用户运行。
- 容器内用户的 `~/.rdp_mcp`（`/home/rdpmcp/.rdp_mcp`）就是配置与数据目录，布局与桌面上相同；挂载宿主的 `~/.rdp_mcp` 即可。宿主目录需要容器用户可写。
- 本机构建验证受网络限制未完成（连不上 Docker Hub）；CI 的 docker job 会构建并运行 `rdp-mcp version`。
- 容器内监听 `0.0.0.0:8765`，因此配置中必须设置 `server.mcp_token` 与 `server.admin_token`（服务启动时校验）。
- 容器内没有系统钥匙串：密码请用 `${ENV}`（`docker run -e`，或数据目录下的 `.env`）或 `password_cmd`。
- 只把端口映射到 `127.0.0.1`，除非确实要对外提供服务。

## 6. 桌面应用（Tauri）

### 6.1 构建

```bash
make app            # = scripts/build-app.sh
make app-sidecars   # 只准备 sidecar（cargo check / tauri dev 之前）
```

`scripts/build-app.sh` 依次：`make worker service` → 复制为 `app/src-tauri/binaries/rdp-mcp-<triple>`、`rdp-worker-<triple>`（Tauri `externalBin` 约定，`<triple>` 取自 `rustc -vV`；该目录不入库）→ macOS 上运行 `scripts/bundle-macos-libs.py` → `pnpm install --frozen-lockfile && pnpm tauri build`。`tauri build` 之前必须已有 `binaries/`，否则 `tauri-build` 报缺少 sidecar。

产物在 `app/src-tauri/target/release/bundle/`：`macos/rdp_mcp.app`、`dmg/rdp_mcp_<ver>_<arch>.dmg`（macOS）；`appimage/*.AppImage`、`deb/*.deb`（Linux）。

### 6.2 sidecar 的查找与布局

Tauri 打包后 sidecar 去掉 triple 后缀，与应用可执行文件同目录（macOS：`rdp_mcp.app/Contents/MacOS/{rdp-mcp,rdp-worker}`；动态库在 `Contents/Frameworks/`）。Rust 侧按以下顺序找可执行文件：环境变量 `RDP_MCP_BIN` / `RDP_MCP_WORKER` → 与应用同目录 → 开发目录（`src-tauri/binaries`、`build/bin`）→ `PATH`。启动命令为 `rdp-mcp serve --managed-by ui-sidecar --worker <rdp-worker>`，进程用 `setsid` 脱离 UI。

测试/多实例：设置 `RDP_MCP_DATA_DIR=<dir>` 后，应用把该目录当作数据目录（读取 `<dir>/run/service.json`），并以 `--config <dir>/configs.yaml`（或 `RDP_MCP_CONFIG`）启动服务；此时不检测 launchd/systemd，不启用单实例插件，应用设置存在 `<dir>/ui-settings.json`。

### 6.3 macOS：未签名的 .dmg

当前发布的 `.dmg` **未签名、未公证**（`signingIdentity: "-"`，即 ad-hoc 签名，保证 arm64 上能运行，但 Gatekeeper 不认）。

**hardened runtime 与库校验**：Tauri 重新签名 sidecar 时会带上 hardened runtime，此时 dyld 会拒绝加载与进程 Team ID 不同的 dylib（ad-hoc 签名的库没有 Team ID），`rdp-worker` 启动即 abort（`mapping process and mapped file have different Team IDs`）。因此 `app/src-tauri/entitlements.plist` 声明了 `com.apple.security.cs.disable-library-validation`，由 `tauri.conf.json` 的 `bundle.macOS.entitlements` 引用。改用 Developer ID 签名所有 dylib 后可去掉这一项。打包后务必实际启动一次验证（见 §6.4），`codesign --verify` 通过不代表 dyld 肯加载。

首次打开：

- 把 `rdp_mcp.app` 拖到「应用程序」后，在访达中**右键 → 打开 → 打开**；或
- 终端执行 `xattr -cr /Applications/rdp_mcp.app` 去掉隔离属性后正常双击。

正式签名与公证（Developer ID + `notarytool`）是后续工作：在 `tauri.conf.json` 设置 `bundle.macOS.signingIdentity`，并提供 `APPLE_*` 环境变量；`scripts/bundle-macos-libs.py` 里的 ad-hoc 签名届时由 Tauri 重新签名覆盖。

### 6.4 验证打包结果

```bash
APP=app/src-tauri/target/release/bundle/macos/rdp_mcp.app
# 1) 不能再引用 Homebrew 等非系统路径
for f in $APP/Contents/MacOS/* $APP/Contents/Frameworks/*.dylib; do
  otool -L "$f" | tail -n +2 | grep -E '/opt/homebrew|/usr/local' && echo "BAD $f"
done
# 2) 用测试数据目录启动应用，确认拉起内置 sidecar，并用内置 worker 连接示例服务器
mkdir -p /tmp/x && cat > /tmp/x/configs.yaml <<EOF
server: {listen: "127.0.0.1:18766", data_dir: /tmp/x}
machines: [{name: sample, host: 127.0.0.1, port: 13390, username: test, password: test, security: tls, enable_drive: false}]
EOF
RDP_MCP_DATA_DIR=/tmp/x $APP/Contents/MacOS/rdp-mcp-desktop &
# /tmp/x/run/service.json 中 managed_by 应为 ui-sidecar；POST /machines/sample/connect 成功；
# lsof -p <worker pid> 里 dylib 全部来自 rdp_mcp.app/Contents/Frameworks，没有 /opt/homebrew
```

### 6.5 Linux：AppImage 与 .deb（配置说明，未在本机构建）

- **.deb**：`tauri.conf.json` 的 `bundle.linux.deb.depends` 声明 `libfreerdp3-3`、`libfreerdp-client3-3`、`libwinpr3-3`，sidecar 直接链接系统库（`rdp-worker` 的 rpath 为 `$ORIGIN/../lib`，找不到时回退系统库路径）。构建机需要 `libwebkit2gtk-4.1-dev libayatana-appindicator3-dev librsvg2-dev patchelf`；目标系统需 Debian 13 / Ubuntu 24.04+（`freerdp3` 包）。
- **AppImage**：不能假设目标系统有 FreeRDP 3，需要把库打进包里。做法：在 Linux 上用 `ldd` 收集 `rdp-worker` 的非系统依赖（`libfreerdp3`、`libfreerdp-client3`、`libwinpr3`、`libssl`、`libcrypto` 等，不含 glibc/libstdc++），复制到 `app/src-tauri/frameworks/`，用 `patchelf --set-rpath '$ORIGIN/../lib' rdp-worker`，再通过 `tauri.linux.conf.json` 的 `bundle.linux.appimage.files`（目标 `usr/lib/<lib>`）放入 AppImage；sidecar 位于 `usr/bin/`，所以 `$ORIGIN/../lib` 正好指向 `usr/lib/`。源码最小构建 FreeRDP 的参数见 §1。
- 托盘需要 `libayatana-appindicator3`；Wayland 下托盘图标取决于桌面环境。
- 这两种产物尚未在 CI 里构建；`scripts/build-app.sh` 在 Linux 上只做 sidecar 复制与 `tauri build`，库收集与 `patchelf` 步骤待实现（R2 后续）。

## 7. CI（GitHub Actions，`.github/workflows/ci.yml`）

| Job | 内容 |
|---|---|
| go（ubuntu-24.04、macos-14） | 安装 FreeRDP 与 tesseract → `gofmt` 检查 → `go vet` → `go test -race` → `make all`，上传 `build/bin` |
| docker | 构建 `packaging/Dockerfile` 并运行 `rdp-mcp version` |

计划中（未实现）：前端 lint/测试与 `cargo clippy`、Desktop 打包、tag 发布 GitHub Release、推送 Docker 镜像、连接 Windows 测试机的集成测试。
