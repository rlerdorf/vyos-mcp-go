#!/usr/bin/env bash
# Smoke test for vyos-mcp-go after deployment.
# Runs read-only MCP tool calls over the SSH tunnel to verify the server works.
# Usage: make test  (or: ./test.sh [host:port])

set -uo pipefail

ENDPOINT="${1:-http://localhost:8384/mcp}"
PASS=0
FAIL=0
SESSION_ID=""

red()   { printf '\033[31m%s\033[0m' "$1"; }
green() { printf '\033[32m%s\033[0m' "$1"; }
bold()  { printf '\033[1m%s\033[0m' "$1"; }

# MCP JSON-RPC call. Sets $RESPONSE (JSON only, SSE framing stripped).
rpc() {
    local id="$1" method="$2" params="$3"
    local headers=(-H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream')
    if [[ -n "$SESSION_ID" ]]; then
        headers+=(-H "Mcp-Session-Id: $SESSION_ID")
    fi
    local raw
    raw=$(curl -sS -X POST "$ENDPOINT" "${headers[@]}" \
        -d "{\"jsonrpc\":\"2.0\",\"id\":$id,\"method\":\"$method\",\"params\":$params}" 2>&1) || {
        RESPONSE="CURL_ERROR: $raw"
        return 1
    }
    # Strip SSE framing (event: / data: prefixes) to get plain JSON
    RESPONSE=$(echo "$raw" | sed -n 's/^data: //p')
    if [[ -z "$RESPONSE" ]]; then
        RESPONSE="$raw"
    fi
}

# Check result for error
check() {
    local name="$1"
    if echo "$RESPONSE" | grep -q '"error"'; then
        printf "  %-30s %s\n" "$name" "$(red FAIL)"
        echo "    $(echo "$RESPONSE" | grep -o '"message":"[^"]*"' | head -1)"
        FAIL=$((FAIL + 1))
    elif echo "$RESPONSE" | grep -q '"result"'; then
        printf "  %-30s %s\n" "$name" "$(green PASS)"
        PASS=$((PASS + 1))
    else
        printf "  %-30s %s\n" "$name" "$(red FAIL)"
        echo "    Unexpected response: ${RESPONSE:0:120}"
        FAIL=$((FAIL + 1))
    fi
}

# --- Run tests ---

echo ""
bold "VyOS MCP Server Smoke Test"
echo ""
echo "Endpoint: $ENDPOINT"
echo ""

# 1. Initialize — get session ID from response headers
echo "--- Protocol ---"
SESSION_ID=$(curl -sS -i -X POST "$ENDPOINT" \
    -H 'Content-Type: application/json' \
    -H 'Accept: application/json, text/event-stream' \
    -d '{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke-test","version":"1.0"}}}' 2>&1 \
    | grep -i 'mcp-session-id' | head -1 | tr -d '\r' | awk '{print $2}')

if [[ -z "$SESSION_ID" ]]; then
    echo "  $(red 'FAIL: Could not establish MCP session. Is the tunnel running?')"
    echo "  Hint: make tunnel"
    exit 1
fi
printf "  %-30s %s\n" "initialize" "$(green PASS)"
PASS=$((PASS + 1))

# Send initialized notification (required by protocol)
curl -sS -X POST "$ENDPOINT" \
    -H 'Content-Type: application/json' \
    -H "Mcp-Session-Id: $SESSION_ID" \
    -d '{"jsonrpc":"2.0","method":"notifications/initialized"}' >/dev/null 2>&1 || true

# 2. List tools
rpc 1 "tools/list" '{}' || true
TOOL_COUNT=$(echo "$RESPONSE" | grep -o '"name"' | wc -l)
if [[ "$TOOL_COUNT" -gt 0 ]]; then
    printf "  %-30s %s (%d tools)\n" "tools/list" "$(green PASS)" "$TOOL_COUNT"
    PASS=$((PASS + 1))
else
    printf "  %-30s %s\n" "tools/list" "$(red FAIL)"
    FAIL=$((FAIL + 1))
fi

# 3. Tool calls (read-only only)
echo ""
echo "--- Read-Only Tools ---"

call_tool() {
    local name="$1" args="$2" id="$3"
    rpc "$id" "tools/call" "{\"name\":\"$name\",\"arguments\":$args}" || true
    check "$name"
}

call_tool "vyos_system_info"     '{}'                                                   10
call_tool "vyos_health_check"    '{}'                                                   11
call_tool "vyos_interface_stats" '{}'                                                   12
call_tool "vyos_routing_table"   '{}'                                                   13
call_tool "vyos_dhcp_leases"     '{}'                                                   14
call_tool "vyos_show_config"     '{"path":["service","ssh"]}'                           15
call_tool "vyos_config_exists"   '{"path":["service","ssh"]}'                           16
call_tool "vyos_return_values"   '{"path":["service","dns","forwarding","allow-from"]}' 17
call_tool "vyos_show"            '{"path":["system","uptime"]}'                         18
call_tool "vyos_ping"            '{"host":"127.0.0.1","count":1}'                       19
call_tool "vyos_traceroute"      '{"host":"127.0.0.1"}'                                 20

# --- Summary ---
echo ""
TOTAL=$((PASS + FAIL))
if [[ "$FAIL" -eq 0 ]]; then
    green "All $TOTAL tests passed."
    echo ""
else
    echo "$(green "$PASS passed"), $(red "$FAIL failed") out of $TOTAL tests."
fi
echo ""
exit "$FAIL"
