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
| Provider layer for other runners | new | `--provider claudecode\|opencode\|openclaw\|generic` |
| Container image on GHCR | new | `ghcr.io/onixldlc/mcp-linux-go`, linux/amd64 + linux/arm64 |
| Self-minted token (`--token auto`) | new | Printed in the boot banner; makes the image zero-config |

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
      --token <str>              Bearer token for HTTP (>=16 chars).     [env: SHELL_MCP_TOKEN]
                                 "auto" mints one per run and prints it
                                 in the boot banner.
      --allow-commands <csv>     Comma-sep allowlist.                   [env: ALLOW_COMMANDS]
                                 Unset = unrestricted (lab mode).
      --provider <name>          Agent runner whose client config is     [env: MCP_PROVIDER]
                                 printed on boot: claudecode (default),
                                 opencode, openclaw, generic.
      --print-config             Print that client config and exit
      --list-providers           List the known runners and exit
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

# No token to hand around: the server mints one and prints it in the banner
mcp-linux-go --token auto
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

Pre-safe so you can start it at anytime:
```bash
# generate it once
echo "$(openssl rand -hex 32)" > .mcpkey

# and load it to env
export SHELL_MCP_TOKEN=$(cat ~/.mcpkey) && HOST=127.0.0.1 PORT=3000 ./mcp-linux-go-linux-amd64 --trusted-hosts=host.docker.internal
```

Generate a token:
```bash
openssl rand -hex 32
```

Or let the server do it — `auto` mints 32 random bytes per run and prints them
in the boot banner, which is the only place they exist:
```bash
./mcp-linux-go-linux-amd64 --token auto
# [mcp-linux-go] auth:        token generated for this run (--token auto) — it lives nowhere else:
# [mcp-linux-go]              34aa8f7b…f0f72d
```

On the client machine, SSH-tunnel the port:
```bash
ssh -N -L 3000:127.0.0.1:3000 user@remote-host
```

### Agent runners (providers)

Every runner spells an MCP server differently, so the server renders its own
connection details into the shape yours reads. `claudecode` is the default:

```bash
mcp-linux-go --list-providers
# * claudecode   Claude Code                  .mcp.json (project) or ~/.claude.json (user)
#   generic      generic mcpServers block     the file your client reads mcpServers from
#   openclaw     OpenClaw                     ~/.openclaw/openclaw.json (JSON5)
#   opencode     opencode                     opencode.json (project root)

# render one without starting a server
mcp-linux-go --print-config --provider opencode --token "$TOKEN"
```

| Provider | Config file | Entry shape |
|---|---|---|
| `claudecode` (default) | `.mcp.json`, `~/.claude.json` | `mcpServers.<name>`, `"type": "http"` |
| `opencode` | `opencode.json` | `mcp.servers.<name>`, `"type": "remote"`, `"protocol": "auto"` |
| `openclaw` | `~/.openclaw/openclaw.json` (JSON5) | `mcp.servers.<name>`, `transport: "streamable-http"` |
| `generic` | whatever your client reads | plain `mcpServers`, `"type": "http"` |

The selected provider's block is also printed in the boot banner with the live
URL and token, ready to paste:

```
[mcp-linux-go] client:      opencode — paste into opencode.json (project root)
[mcp-linux-go]              {
[mcp-linux-go]                "$schema": "https://opencode.ai/config.json",
[mcp-linux-go]                "mcp": {
[mcp-linux-go]                  "servers": {
[mcp-linux-go]                    "mcp-linux-go": {
[mcp-linux-go]                      "type": "remote",
[mcp-linux-go]                      "url": "http://127.0.0.1:3000/mcp",
...
```

A wildcard bind (`0.0.0.0`) is rendered as `127.0.0.1` in the config, since a
client cannot dial a wildcard.

Adding a runner is one file in [`internal/providers/`](internal/providers): a
`Render` returning the block, an optional `CLI` one-liner, and `register()`.
`go test ./internal/providers/` then checks it carries the URL, token and server
name, and that the JSON shapes parse.

### Claude Code

Also reachable as the default provider: `mcp-linux-go --print-config`.

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
| `SHELL_MCP_TOKEN` | `--token` | — | Required, ≥16 chars. `auto` → server mints one and logs it. |
| `ALLOW_COMMANDS` | `--allow-commands` | unset | Comma list. Unset → unrestricted. |
| `ALLOWED_COMMANDS` | — | — | Alias of `ALLOW_COMMANDS` |
| `MCP_PROVIDER` | `--provider` | `claudecode` | Which runner's client config the banner prints. |
| `TRUSTED_HOSTS` | `--trusted-hosts` | unset | Extra `Host` values. Loopback always allowed. |
| `LOG_REQUESTS` | `--log-requests` | unset | `1`/`true`/`yes` → log every request. |

## Build locally

```bash
go build -o mcp-linux-go .

# cross-compile (single target)
GOOS=linux  GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o dist/mcp-linux-go-linux-arm64 .
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o dist/mcp-linux-go-darwin-arm64 .
```

CI builds those 5 platforms (linux-amd64, linux-arm64, darwin-amd64, darwin-arm64,
windows-amd64) plus the container image. See [Releasing](#releasing).

## Docker

Published on every release, `linux/amd64` and `linux/arm64`:

```bash
docker pull ghcr.io/onixldlc/mcp-linux-go:latest
```

The image needs no configuration. It binds `0.0.0.0` inside the container, runs
unrestricted, and mints its own token — read it out of the log:

```bash
docker run -d --name mcp -p 127.0.0.1:3000:3000 ghcr.io/onixldlc/mcp-linux-go:latest
docker logs mcp | grep Bearer
# [mcp-linux-go]              Authorization: Bearer 34aa8f7b…f0f72d
```

That is what the image is for: a disposable box an agent can run any command in.
It is also why you publish the port to `127.0.0.1` (or a private network) and
never to a public interface.

Pin the token so it survives a restart, and narrow what may run:

```bash
docker run -d --name mcp \
  -e SHELL_MCP_TOKEN="$(openssl rand -hex 32)" \
  -e ALLOW_COMMANDS="ls,cat,grep" \
  -p 127.0.0.1:3000:3000 \
  ghcr.io/onixldlc/mcp-linux-go:latest
```

Point it at a different runner, and read the config out of the log:

```bash
docker run -d --name mcp -e MCP_PROVIDER=openclaw -p 127.0.0.1:3000:3000 \
  ghcr.io/onixldlc/mcp-linux-go:latest
docker logs mcp | grep -A12 'client:'
```

Flags work the same as env, and `--help` works without starting a server:

```bash
docker run --rm ghcr.io/onixldlc/mcp-linux-go:latest --help

docker run --rm -p 127.0.0.1:3000:3000 ghcr.io/onixldlc/mcp-linux-go:latest \
  --token "$(openssl rand -hex 32)" \
  --allow-commands "ls,cat,grep" \
  --log-requests
```

Build it yourself — the Dockerfile lives in `docker/`, the context is the repo root:

```bash
docker build -f docker/Dockerfile -t mcp-linux-go --build-arg VERSION="$(cat VERSION)" .
```

## Releasing

`VERSION` is both the trigger and the single source of the version. Push a commit
that changes it to `main` and [build.yml](.github/workflows/build.yml) does the rest:

1. **determine-version** — reads `VERSION`. It carries the whole tag: a base
   version plus this repo's revision counter, `v0.2.2-r4`. Bump `-rN` for a
   release on top of the same base, or move the base and drop back to no suffix.
2. **test** — `go vet`, `gofmt -l`, `go test ./...`.
3. **build** — the 5 binaries, with the version compiled in. linux-amd64 is then
   started with `--token auto` and driven through [scripts/smoke.sh](scripts/smoke.sh):
   `/healthz`, 401 without a token, the legacy `initialize` path, all six tools
   advertised, and `shell_execute` actually running a command.
4. **image** — `docker/Dockerfile` built for amd64 and smoke tested as a container
   the same way, then pushed for amd64 + arm64 as `:<version>` and `:latest`.
5. **release** — tags the commit and attaches the binaries plus `SHA256SUMS`.

Nothing is tagged by hand; the tag comes from `VERSION`. `workflow_dispatch` runs
the same pipeline with an optional version override, and with **publish** unchecked
it builds and smoke tests without pushing or tagging anything.

The smoke test is runnable locally against any server you have a token for:

```bash
scripts/smoke.sh http://127.0.0.1:3000 "$SHELL_MCP_TOKEN"
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
