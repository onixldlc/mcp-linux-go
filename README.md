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

## MCP protocol support

Speaks MCP revision **2026-07-28** (go-sdk v1.7.0), negotiating down to
`2025-11-25`, `2025-06-18`, `2025-03-26`, and `2024-11-05` for older clients.

The server runs in **stateless** mode, which `2026-07-28` requires over HTTP:

- No `initialize` handshake and no `Mcp-Session-Id`. Clients call `server/discover`
  and carry `_meta` (protocol version, client info, capabilities) on each request.
- `GET` and `DELETE` on `/mcp` return `405`. There is no resumable event stream.
- Per SEP-2243, requests must mirror `Mcp-Protocol-Version`, `Mcp-Method`, and —
  for `tools/call` — `Mcp-Name` into HTTP headers. Mismatches return `-32020`.
  Real MCP clients send these automatically; hand-rolled `curl` calls must not omit them.

Nothing here depends on session state: the server issues no server→client
requests and uses no sampling, roots, subscriptions, or logging (all deprecated
in `2026-07-28`).

## CLI flags

```
mcp-linux-go [options]

  -H, --host <addr>              HTTP bind address (default: 127.0.0.1) [env: HOST]
  -p, --port <num>               HTTP port (default: 3000)              [env: PORT]
      --token <str>              Bearer token for HTTP (>=16 chars)     [env: SHELL_MCP_TOKEN]
      --allow-commands <csv>     Comma-sep allowlist.                   [env: ALLOW_COMMANDS]
                                 Unset = unrestricted (lab mode).
      --trusted-hosts <csv>      Extra Host header values to accept     [env: TRUSTED_HOSTS]
                                 when listening on loopback.
                                 e.g. host.docker.internal
      --log-requests             Log every HTTP request                 [env: LOG_REQUESTS]
                                 Rejections always log.
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

# if you are using docker for the mcp client you need to add --trusted-hosts
HOST=127.0.0.1 PORT=3000 ./mcp-linux-go-linux-amd64 --trusted-hosts "host.docker.internal"
```

Generate a token:
```bash
openssl rand -hex 32
```

On the client machine, SSH-tunnel the port:
```bash
ssh -N -L 3000:127.0.0.1:3000 user@remote-host
```

### Claude Code

```bash
claude mcp add --transport http shell-remote http://127.0.0.1:3000/mcp \
  --header "Authorization: Bearer YOUR_TOKEN"
```

**Client inside a container** (Claude Code in Docker/Podman reaching the host):
the client addresses the server as `host.docker.internal`, but the runtime
forwards that into the host's loopback. The connection is loopback while the
`Host` header is not, which trips DNS-rebinding protection and returns **403** —
easy to misread as a bad token, since Claude Code reports it as a rejected
`Authorization` header. Name the host explicitly:

```bash
mcp-linux-go --token "$TOKEN" --trusted-hosts "host.docker.internal"
```

```bash
claude mcp add --transport http shell-remote http://host.docker.internal:3000/mcp \
  --header "Authorization: Bearer YOUR_TOKEN"
```

Or as JSON in `.mcp.json` / `~/.claude.json`:
```json
{
  "mcpServers": {
    "shell-remote": {
      "type": "http",
      "url": "http://127.0.0.1:3000/mcp",
      "headers": { "Authorization": "Bearer YOUR_TOKEN" }
    }
  }
}
```

`"type"` is **required**. An entry with a `url` but no `type` is read as a stdio
server and skipped. `"streamable-http"` is accepted as an alias for `"http"`.

### Diagnosing connection failures

Claude Code reports every 4xx as an `Authorization` problem, so its message is
not a reliable diagnosis. Read the server's stderr instead — rejections always
log the real reason:

```
[mcp-linux-go 04:21:45] 403 DNS-rebinding guard rejected POST /mcp host="host.docker.internal:3000" from=127.0.0.1:38094 mcp-method=tools/list proto=2026-07-28
[mcp-linux-go 04:21:45]     if that name is yours, restart with --trusted-hosts "host.docker.internal" (or TRUSTED_HOSTS=host.docker.internal)
[mcp-linux-go 04:21:45] 401 POST /mcp host="127.0.0.1:3117" — token mismatch (presented fp wrongt…rong, expected fp 9de4dd…9a24)
[mcp-linux-go 04:21:45] 401 POST /mcp host="127.0.0.1:3117" — missing Authorization header
```

Token fingerprints (first 6 + last 4 chars) let you compare what the client sent
against the boot banner without the log ever holding the token itself.

`--log-requests` adds a line per request — status, `Host`, MCP method/tool name,
user agent, duration:

```
[mcp-linux-go 04:22:09] 200 POST /mcp host="127.0.0.1:3117" from=127.0.0.1:37384 mcp-method=tools/call mcp-name=shell_execute proto=2026-07-28 ua="claude-code/2.1.0" 2ms
```

Two response messages distinguish the guards, which matters because both are
403s and both look like auth failures to the client:

| Body | From | Meaning |
|---|---|---|
| `Forbidden: rejected Host header … (DNS-rebinding protection)` | this server | Add `--trusted-hosts` |
| `Forbidden: invalid Host header …` | the go-sdk | **Stale binary** — predates the `--trusted-hosts` support; rebuild |

### Claude API MCP connector

Pass the token as `authorization_token`; the connector needs both the server
entry and a matching `mcp_toolset`:
```json
{
  "mcp_servers": [
    { "type": "url", "name": "shell", "url": "https://your-host/mcp",
      "authorization_token": "YOUR_TOKEN" }
  ],
  "tools": [ { "type": "mcp_toolset", "mcp_server_name": "shell" } ]
}
```

### claude.ai custom connectors — not supported

Custom connectors added from claude.ai require the MCP server to act as a full
OAuth 2.1 authorization server (browser-interactive flow, PKCE S256, Protected
Resource Metadata discovery). A static bearer token is not accepted there. This
server is bearer-only, so use Claude Code or the API connector above.

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
| `TRUSTED_HOSTS` | `--trusted-hosts` | unset | Extra `Host` values. Loopback always allowed. |
| `LOG_REQUESTS` | `--log-requests` | unset | `1`/`true`/`yes` → log every request. |

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
- **Bearer token is the only auth.** The server validates a shared secret; it does
  not validate OAuth tokens, audiences, or scopes. Treat the token as root-equivalent.
- **No `Origin`/`Sec-Fetch-Site` checking, on purpose.** It would 403 legitimate
  non-browser clients that send `Origin: null`, while adding nothing: a browser
  cannot attach `Authorization: Bearer <secret>` cross-origin without a CORS
  preflight this server never approves. DNS rebinding is still blocked by the
  `Host`-header guard below.
- **DNS-rebinding guard is on by default.** A non-loopback `Host` header on a
  loopback connection is rejected with 403. `--trusted-hosts` pokes a named hole
  in it, which is much narrower than disabling it: rebinding needs the attacker
  to control the DNS name being trusted, and `host.docker.internal` is
  container-runtime-local, not registrable. Do not add public names you don't own.
- **Run as non-root.** Passwordless sudo = RCE-to-root.
- **On EC2**: enforce IMDSv2 with hop-limit 1 to stop credential theft → IAM pivot.
- **Destroy the box when done.**

## Credits

Original design, request/response shape, and validation semantics: [tumf/mcp-shell-server](https://github.com/tumf/mcp-shell-server) (MIT).
TypeScript/Bun reimplementation: [onixldlc/mcp-linux-js](https://github.com/onixldlc/mcp-linux-js) (MIT).
This repo is an independent Go port.

## License

MIT.
