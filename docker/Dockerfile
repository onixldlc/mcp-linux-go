# syntax=docker/dockerfile:1.7

# Builder stays on the native arch and cross-compiles: no QEMU for arm64.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
    -ldflags "-s -w -X main.serverVersion=${VERSION}" \
    -o /out/mcp-linux-go .

FROM debian:bookworm-slim

RUN apt-get update
RUN apt-get install -y --no-install-recommends ca-certificates wget
RUN rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/mcp-linux-go /usr/local/bin/mcp-linux-go
RUN chmod +x /usr/local/bin/mcp-linux-go

# token=auto so the image runs with no configuration; it prints the token in the
# banner. ALLOW_COMMANDS stays unset: unrestricted, which is what the image is for.
ENV HOST=0.0.0.0 \
    PORT=3000 \
    SHELL_MCP_TOKEN=auto

EXPOSE 3000
HEALTHCHECK --interval=30s --timeout=3s \
  CMD wget -qO- http://127.0.0.1:${PORT}/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/mcp-linux-go"]
