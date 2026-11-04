# rdp-mcp service image (no desktop UI). Build:
#   docker build -t rdp-mcp -f packaging/Dockerfile .
# Run (the host's ~/.rdp_mcp becomes the container user's ~/.rdp_mcp):
#   docker run -d -p 127.0.0.1:8765:8765 -v ~/.rdp_mcp:/home/rdpmcp/.rdp_mcp rdp-mcp
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
    && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o build/rdp-mcp ./cmd/rdp-mcp

FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      libfreerdp3-3 libfreerdp-client3-3 libwinpr3-3 tesseract-ocr tesseract-ocr-chi-sim ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --create-home --home-dir /home/rdpmcp --shell /usr/sbin/nologin rdpmcp \
    && mkdir -p /home/rdpmcp/.rdp_mcp && chown rdpmcp /home/rdpmcp/.rdp_mcp
COPY --from=build /src/build/rdp-mcp /src/build/worker/rdp-worker /usr/local/bin/
USER rdpmcp
WORKDIR /home/rdpmcp
# Same layout as on a desktop: configs.yaml, .env, known_hosts, logs, ... in ~/.rdp_mcp
VOLUME ["/home/rdpmcp/.rdp_mcp"]
EXPOSE 8765
ENTRYPOINT ["rdp-mcp", "serve", "--listen", "0.0.0.0:8765", "--managed-by", "docker"]
