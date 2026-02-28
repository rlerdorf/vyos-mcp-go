# VyOS Go MCP Server

Go-based MCP server that runs directly on the VyOS router as a persistent daemon. Calls VyOS CLI tools directly -- no REST API, no API key, no HTTPS needed.

## Build

```bash
make build    # Cross-compiles static linux/amd64 binary
make deploy   # Build + scp to router + restart systemd service
```

Uses `ensure-go.sh` to auto-download Go 1.26+ if the system Go is too old. The toolchain is cached in `.goroot/`.

## Architecture

- `main.go` -- HTTP server entry point, Streamable HTTP transport on `/mcp`
- `client.go` -- VyOS CLI client (calls cli-shell-api, my_set, my_delete, etc. directly)
- `tools.go` -- 18 MCP tool registrations

The client creates a VyOS config session at startup via `cli-shell-api getSessionEnv` + `setupSession`, then uses CLI tools directly. This is the same approach the VyOS HTTP API uses internally.

## On-router deployment

Binary and service file live in `/config/user-data/` (persists across VyOS upgrades).

```
/config/user-data/vyos-mcp-go          # Static binary
/config/user-data/mcp-server.service   # systemd unit
```

Daemon binds to `127.0.0.1:8384` (localhost only). Access from workstation via SSH tunnel.

## Testing

```bash
make tunnel   # ssh -L 8384:localhost:8384 -N router
make test     # curl the /mcp endpoint
```

## Adding a new tool

Add input struct + handler in `tools.go`:

```go
mcp.AddTool(s, &mcp.Tool{
    Name:        "vyos_my_tool",
    Description: "What it does",
}, func(ctx context.Context, req *mcp.CallToolRequest, input myInput) (*mcp.CallToolResult, any, error) {
    result, err := client.Show(ctx, []string{"some", "command"})
    if err != nil {
        return nil, nil, err
    }
    return textResult(result)
})
```
