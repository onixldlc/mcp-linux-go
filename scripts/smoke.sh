#!/usr/bin/env bash
# MCP-level smoke test, against the binary or the container.
#
#   scripts/smoke.sh http://127.0.0.1:3000 <bearer-token>
set -euo pipefail

BASE="${1:?usage: smoke.sh <base-url> <token>}"
TOKEN="${2:?usage: smoke.sh <base-url> <token>}"

PROTO="2026-07-28"

# 2026-07-28 has no initialize handshake, so every request carries what it used
# to establish: the SEP-2243 headers below plus these three _meta fields. The
# server returns -32020/-32602 if any one is missing.
META="\"_meta\":{\
\"io.modelcontextprotocol/protocolVersion\":\"${PROTO}\",\
\"io.modelcontextprotocol/clientCapabilities\":{},\
\"io.modelcontextprotocol/clientInfo\":{\"name\":\"smoke\",\"version\":\"0\"}}"

# rpc <mcp-method> <body> [tool-name]
rpc() {
  local method="$1" body="$2" name="${3:-}"
  local -a hdr=(
    -H "Authorization: Bearer ${TOKEN}"
    -H "Mcp-Protocol-Version: ${PROTO}"
    -H "Mcp-Method: ${method}"
    -H 'Content-Type: application/json'
    -H 'Accept: application/json, text/event-stream'
  )
  [[ -n "${name}" ]] && hdr+=(-H "Mcp-Name: ${name}")
  curl -fsS -X POST "${BASE}/mcp" "${hdr[@]}" -d "${body}"
}

ok=0
for _ in $(seq 1 30); do
  if curl -fsS "${BASE}/healthz" >/dev/null 2>&1; then
    ok=1
    break
  fi
  sleep 1
done
if [[ "${ok}" != "1" ]]; then
  echo "server never became healthy at ${BASE}"
  exit 1
fi

health="$(curl -fsS "${BASE}/healthz")"
echo "${health}"
grep -q '"ok":true' <<<"${health}"
grep -q "\"protocol_version\":\"${PROTO}\"" <<<"${health}"
grep -q '"stateless":true' <<<"${health}"
echo "healthz reports ${PROTO}, stateless"

code="$(curl -s -o /dev/null -w '%{http_code}' -X POST "${BASE}/mcp")"
if [[ "${code}" != "401" ]]; then
  echo "expected 401 without a token, got ${code}"
  exit 1
fi
echo "unauthenticated calls get 401"

# No 2026 headers here on purpose: this is the path an older client takes.
init="$(curl -fsS -X POST "${BASE}/mcp" \
  -H "Authorization: Bearer ${TOKEN}" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}')"
grep -q '"serverInfo"' <<<"${init}" || {
  echo "initialize did not answer"
  echo "${init}"
  exit 1
}
echo "legacy initialize still answers"

list="$(rpc tools/list "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\",\"params\":{${META}}}")"
for tool in shell_execute read_file write_file upload_file_b64 list_dir system_info; do
  if ! grep -q "\"${tool}\"" <<<"${list}"; then
    echo "tools/list is missing ${tool}"
    echo "${list}"
    exit 1
  fi
done
echo "tools/list advertises all six tools"

out="$(rpc tools/call \
  "{\"jsonrpc\":\"2.0\",\"id\":3,\"method\":\"tools/call\",\"params\":{${META},\"name\":\"shell_execute\",\"arguments\":{\"command\":[\"echo\",\"mcp-ok\"]}}}" \
  shell_execute)"
if ! grep -q 'mcp-ok' <<<"${out}"; then
  echo "shell_execute did not run the command"
  echo "${out}"
  exit 1
fi
echo "shell_execute ran a command"

echo "smoke test passed against ${BASE}"
