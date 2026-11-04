#!/usr/bin/env bash
# 构建桌面应用（docs/packaging.md §2）：
#   1. 构建 remoter-mcp 与 remoter-worker（make worker service）
#   2. 复制为 app/src-tauri/binaries/<name>-<target-triple>（Tauri sidecar 约定）
#   3. macOS：把 FreeRDP/WinPR/OpenSSL 动态库打进应用并改写 install name
#   4. pnpm tauri build（未签名；macOS 为 ad-hoc 签名，见 docs/packaging.md）
#
# 用法：scripts/build-app.sh [--sidecars-only]
#   --sidecars-only  只准备 sidecar 与动态库，不执行 tauri build（cargo check / tauri dev 前使用）
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

SIDECARS_ONLY=0
[[ "${1:-}" == "--sidecars-only" ]] && SIDECARS_ONLY=1

TRIPLE="$(rustc -vV | sed -n 's/^host: //p')"
BIN_DIR="app/src-tauri/binaries"

echo "==> 构建 remoter-worker / remoter-mcp"
make worker service

rm -rf "$BIN_DIR"   # 清掉旧名字的残留（改名后 externalBin 名字变了）
mkdir -p "$BIN_DIR"
cp build/bin/remoter-mcp "$BIN_DIR/remoter-mcp-$TRIPLE"
cp build/bin/remoter-worker "$BIN_DIR/remoter-worker-$TRIPLE"
chmod +x "$BIN_DIR"/*

if [[ "$(uname -s)" == "Darwin" ]]; then
  echo "==> 打包 macOS 动态库"
  python3 -I scripts/bundle-macos-libs.py \
    --binary "$BIN_DIR/remoter-worker-$TRIPLE" \
    --out app/src-tauri/frameworks \
    --conf app/src-tauri/tauri.macos.conf.json
  # Go 服务不应依赖 Homebrew 库
  if otool -L "$BIN_DIR/remoter-mcp-$TRIPLE" | grep -E '/opt/homebrew|/usr/local' >/dev/null; then
    echo "error: remoter-mcp links against non-system libraries" >&2
    exit 1
  fi
fi

if [[ $SIDECARS_ONLY -eq 1 ]]; then
  echo "sidecars ready in $BIN_DIR"
  exit 0
fi

echo "==> tauri build"
cd app
pnpm install --frozen-lockfile
pnpm tauri build

echo "==> 产物"
find src-tauri/target/release/bundle -maxdepth 2 \( -name '*.dmg' -o -name '*.app' -o -name '*.AppImage' -o -name '*.deb' \) -print
