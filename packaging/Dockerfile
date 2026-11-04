# remoter-mcp service image (no desktop UI). Build:
#   docker build -t remoter-mcp -f packaging/Dockerfile .
# Run (the host's ~/.remoter becomes the container user's ~/.remoter):
#   docker run -d -p 127.0.0.1:8765:8765 -v ~/.remoter:/home/remoter/.remoter remoter-mcp
# The service listens on 0.0.0.0 inside the container, so server.mcp_token and
# server.admin_token must be set in the config (or via env, see docs/packaging.md).

# Go version must satisfy go.mod; the golang image is Debian trixie based.
FROM golang:1.26-trixie AS build
RUN apt-get update && apt-get install -y --no-install-recommends \
      build-essential cmake pkg-config freerdp3-dev \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=docker
RUN cmake -S worker -B build/worker -DCMAKE_BUILD_TYPE=Release \
    && cmake --build build/worker -j"$(nproc)" \
    && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o build/remoter-mcp ./cmd/remoter-mcp

FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      libfreerdp3-3 libfreerdp-client3-3 libwinpr3-3 tesseract-ocr tesseract-ocr-chi-sim ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --create-home --home-dir /home/remoter --shell /usr/sbin/nologin remoter \
    && mkdir -p /home/remoter/.remoter && chown remoter /home/remoter/.remoter
COPY --from=build /src/build/remoter-mcp /src/build/worker/remoter-worker /usr/local/bin/
USER remoter
WORKDIR /home/remoter
# Same layout as on a desktop: configs.yaml, .env, known_hosts, logs, ... in ~/.remoter
VOLUME ["/home/remoter/.remoter"]
EXPOSE 8765
ENTRYPOINT ["remoter-mcp", "serve", "--listen", "0.0.0.0:8765", "--managed-by", "docker"]
