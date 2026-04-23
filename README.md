# mcp-linux-go

Single-binary MCP shell server. Go port of [mcp-linux-js](https://github.com/onixldlc/mcp-linux-js) (MIT), itself a port of [tumf/mcp-shell-server](https://github.com/tumf/mcp-shell-server) (MIT). HTTP transport with bearer auth, file helpers, cross-platform prebuilt binaries.

Same request/response shape as upstream, so existing clients work unchanged.

## What's in here

| Feature | Source | Notes |
|---|---|---|
| Allowlist via `ALLOW_COMMANDS` / `ALLOWED_COMMANDS` | tumf | Comma-sep, trimmed |
| Shell-operator validation (`;` `&&` `\|\|` `\|`) | tumf | Each segment head checked |
| No /bin/sh — direct execve | tumf | No shell injection |
| `stdin` input | tumf | Piped to first statement |
| In-process pipeline semantics | mcp-linux-js | `;` `&&` `\|\|` `\|` emulated, no shell |
| **Unrestricted mode** (ALLOW_COMMANDS unset) | mcp-linux-js | Lab convenience. Logged loudly. |
| **HTTP transport + bearer token** | mcp-linux-js | For remote control |
| `read_file` / `write_file` / `upload_file_b64` / `list_dir` / `system_info` | mcp-linux-js | Quality-of-life |
| Single-binary build (`go build`) | new | 5 platforms via CI |

HTTP-only. No stdio transport in this port.

## CLI flags

```
mcp-linux-go [options]

  -H, --host <addr>              HTTP bind address (default: 127.0.0.1) [env: HOST]
  -p, --port <num>               HTTP port (default: 3000)              [env: PORT]
      --token <str>              Bearer token for HTTP (>=16 chars)     [env: SHELL_MCP_TOKEN]
      --allow-commands <csv>     Comma-sep allowlist.                   [env: ALLOW_COMMANDS]
                                 Unset = unrestricted (lab mode).
  -h, --help                     Show help
  -v, --version                  Show version
```

CLI flags override env vars. Env vars override defaults.

## Modes

```bash
# Allowlist (safe-ish): only listed commands allowed
mcp-linux-go --token "$(openssl rand -hex 32)" \
  --allow-commands "ls,cat,pwd,grep,wc,find"

# Equivalent via env:
SHELL_MCP_TOKEN=$(openssl rand -hex 32) \
ALLOW_COMMANDS="ls,cat,pwd,grep,wc,find" mcp-linux-go

# Unrestricted (lab): no allowlist — emits WARNING on boot
mcp-linux-go --token "$(openssl rand -hex 32)"
```

## HTTP transport with bearer token

On the server (flags):
```bash
./mcp-linux-go-linux-amd64 \
  --host 127.0.0.1 --port 3000 \
  --token "$(openssl rand -hex 32)" \
  --allow-commands "ls,cat,grep"
```

Or via env:
```bash
export SHELL_MCP_TOKEN=$(openssl rand -hex 32)
# optionally: export ALLOW_COMMANDS="ls,cat,apt-get,..."
HOST=127.0.0.1 PORT=3000 ./mcp-linux-go-linux-amd64
```

Generate a token:
```bash
openssl rand -hex 32
```

On the client machine, SSH-tunnel the port:
```bash
ssh -N -L 3000:127.0.0.1:3000 user@remote-host
```

Claude Desktop config:
```json
{
  "mcpServers": {
    "shell-remote": {
      "url": "http://127.0.0.1:3000/mcp",
      "headers": { "Authorization": "Bearer YOUR_TOKEN" }
    }
  }
}
```

## Tool: `shell_execute`

Request (tumf-compatible):
```json
{
  "command": ["ls", "-l", "/tmp"],
  "stdin": "optional input",
  "directory": "/some/path",
  "timeout": 60
}
```

Response:
```json
{
  "stdout": "...",
  "stderr": "",
  "status": 0,
  "execution_time": 0.017,
  "timed_out": false,
  "truncated": false
}
```

Pipelines and sequences:
```json
{ "command": ["cat", "file.txt", "|", "grep", "foo", "|", "wc", "-l"] }
{ "command": ["apt-get", "update", "&&", "apt-get", "install", "-y", "nginx"] }
{ "command": ["systemctl", "restart", "nginx", "||", "journalctl", "-u", "nginx", "-n", "50"] }
```

## Env vars

All env vars have a CLI-flag equivalent (see [CLI flags](#cli-flags)). Flags override env.

| Var | Flag | Default | Notes |
|---|---|---|---|
| `HOST` | `-H, --host` | `127.0.0.1` | HTTP bind |
| `PORT` | `-p, --port` | `3000` | HTTP port |
| `SHELL_MCP_TOKEN` | `--token` | — | Required, ≥16 chars |
| `ALLOW_COMMANDS` | `--allow-commands` | unset | Comma list. Unset → unrestricted. |
| `ALLOWED_COMMANDS` | — | — | Alias of `ALLOW_COMMANDS` |

## Build locally

```bash
go build -o mcp-linux-go .

# cross-compile (single target)
GOOS=linux  GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o dist/mcp-linux-go-linux-arm64 .
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o dist/mcp-linux-go-darwin-arm64 .
```

CI builds 5 platforms (linux-amd64, linux-arm64, darwin-amd64, darwin-arm64, windows-amd64) on tag `v*`.

## Docker

```bash
docker build -t mcp-linux-go .

# via env
docker run --rm \
  -e SHELL_MCP_TOKEN=$(openssl rand -hex 32) \
  -e ALLOW_COMMANDS="ls,cat,grep" \
  -p 127.0.0.1:3000:3000 \
  mcp-linux-go

# via CLI flags
docker run --rm \
  -p 127.0.0.1:3000:3000 \
  mcp-linux-go \
    --token "$(openssl rand -hex 32)" \
    --allow-commands "ls,cat,grep"

# print help
docker run --rm mcp-linux-go --help
```

## Security reality check

- **Allowlist is not a security boundary** if you include `bash`, `sh`, `python`, `perl`, `node`, `ruby`, `awk`, `find`, `xargs`, `ssh`, `sudo`, `make`, or `env`. Any of these → RCE via `-c`/`-e`/`-exec`. Allowlist is only meaningful for inspection-only binaries (`ls`, `cat`, `grep`, `wc`, `head`, `tail`, `df`, `uptime`, etc.).
- **Never expose to the public internet**, even with a token. Bind `127.0.0.1` + SSH tunnel.
- **Run as non-root.** Passwordless sudo = RCE-to-root.
- **On EC2**: enforce IMDSv2 with hop-limit 1 to stop credential theft → IAM pivot.
- **Destroy the box when done.**

## Credits

Original design, request/response shape, and validation semantics: [tumf/mcp-shell-server](https://github.com/tumf/mcp-shell-server) (MIT).
TypeScript/Bun reimplementation: [onixldlc/mcp-linux-js](https://github.com/onixldlc/mcp-linux-js) (MIT).
This repo is an independent Go port.

## License

MIT.
