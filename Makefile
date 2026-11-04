VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)
BIN := build/bin

.PHONY: all worker service test clean run

all: worker service

worker:
	cmake -S worker -B build/worker -DCMAKE_BUILD_TYPE=RelWithDebInfo >/dev/null
	cmake --build build/worker -j
	@mkdir -p $(BIN)
	cp build/worker/rdp-worker $(BIN)/rdp-worker

service:
	@mkdir -p $(BIN)
	go build -ldflags "-X main.version=$(VERSION)" -o $(BIN)/rdp-mcp ./cmd/rdp-mcp

test:
	go vet ./...
	go test ./...

run: all
	$(BIN)/rdp-mcp serve

clean:
	rm -rf build
