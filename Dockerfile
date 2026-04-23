# syntax=docker/dockerfile:1.7

FROM golang:1.25-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" -o dist/mcp-linux-go .

FROM debian:bookworm-slim
RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates wget && \
    rm -rf /var/lib/apt/lists/*

COPY --from=builder /build/dist/mcp-linux-go /usr/local/bin/mcp-linux-go
RUN chmod +x /usr/local/bin/mcp-linux-go

ENV HOST=0.0.0.0 \
    PORT=3000
# SHELL_MCP_TOKEN required at runtime
# ALLOW_COMMANDS optional; unset = unrestricted lab mode

EXPOSE 3000
HEALTHCHECK --interval=30s --timeout=3s \
  CMD wget -qO- http://127.0.0.1:${PORT}/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/mcp-linux-go"]
