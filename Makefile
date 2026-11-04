VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)
BIN := build/bin

.PHONY: all worker service test clean run

all: worker service

worker:
	cmake -S worker -B build/worker -DCMAKE_BUILD_TYPE=RelWithDebInfo >/dev/null
	cmake --build build/worker -j
	@mkdir -p $(BIN)
	cp build/worker/remoter-worker $(BIN)/remoter-worker

service:
	@mkdir -p $(BIN)
	go build -ldflags "-X main.version=$(VERSION)" -o $(BIN)/remoter-mcp ./cmd/remoter-mcp

test:
	go vet ./...
	go test ./...

run: all
	$(BIN)/remoter-mcp serve

clean:
	rm -rf build

# 桌面应用：构建 sidecar（含 macOS 动态库打包）并执行 tauri build，见 docs/packaging.md §6
.PHONY: app app-sidecars
app:
	scripts/build-app.sh

# 只准备 sidecar 与动态库（cargo check / pnpm tauri dev 之前用）
app-sidecars:
	scripts/build-app.sh --sidecars-only
