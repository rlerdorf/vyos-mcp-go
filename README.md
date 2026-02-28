# vyos-mcp-go

A native Go [MCP](https://modelcontextprotocol.io/) server for [VyOS](https://vyos.io/) routers. Compiles to a single static binary, runs directly on the router as a systemd service, and exposes 18 tools over Streamable HTTP transport.

## Why

- **Zero runtime dependencies** -- one static binary, no PHP/Python/Node
- **No REST API needed** -- calls VyOS CLI tools directly, no API key or HTTPS configuration required
- **Persistent daemon** -- runs on the router itself, sessions survive across client reconnects
- **Survives upgrades** -- binary lives in `/config/`, which persists across VyOS image upgrades
- **Low latency** -- direct CLI execution, no network stack involved

## Architecture

```
Workstation                          VyOS Router
+-----------+    SSH tunnel          +-------------------------+
| MCP Client|---(port 8384)-------->| vyos-mcp-go daemon      |
| (Claude,  |  http://localhost:8384| listening 127.0.0.1:8384|
|  etc.)    |                       | Streamable HTTP on /mcp |
+-----------+                       |         |               |
                                    |         v               |
                                    |  VyOS CLI tools          |
                                    |  (vyatta-op-cmd-wrapper, |
                                    |   cli-shell-api, etc.)   |
                                    +-------------------------+
```

The daemon binds to `127.0.0.1` only -- it is not exposed to the network. Access it from your workstation through an SSH tunnel.

## Security

### Why localhost-only binding matters

The MCP server binds to `127.0.0.1:8384` and **must not** be exposed on a network interface. This is critical because:

- **The MCP protocol has no authentication.** Any client that can reach the HTTP endpoint can call any tool -- including `vyos_set_config`, `vyos_delete_config`, and `vyos_commit`. That's full read/write access to your router's configuration with no credentials required.
- **This runs on your router.** A compromised router means a compromised network. Firewall rules, NAT, DNS, DHCP -- all controlled through these tools.
- **The daemon runs with root privileges.** It needs access to VyOS config session tools which require root. Exposing the MCP endpoint would give unauthenticated remote root-level config access.

By binding to localhost, the only way to reach the server is through an SSH tunnel, which provides authentication (SSH keys), encryption, and access control that MCP itself lacks.

**Never** change the listen address to `0.0.0.0` or a LAN IP. If you need remote access, always use an SSH tunnel.

### Other measures

- The SSH tunnel inherits your existing SSH key authentication and encryption
- The systemd service runs with `NoNewPrivileges=yes` and `PrivateTmp=yes`
- Config-modifying operations are serialized with a mutex to prevent race conditions

## Prerequisites

- VyOS 1.4+ (tested on 1.5 rolling)
- SSH access to the router with sudo (key-based recommended)
- Go 1.26+ on your build machine (or let `ensure-go.sh` download it automatically)

**Note:** Unlike the REST API approach, this server does **not** require the VyOS HTTPS API to be enabled. It calls VyOS CLI tools directly.

## Quick Start

```bash
git clone https://github.com/rlerdorf/vyos-mcp-go.git
cd vyos-mcp-go

# Build (cross-compiles a static linux/amd64 binary)
make build

# Deploy to router
# Requires: SSH host "router" configured in ~/.ssh/config
make deploy

# Open an SSH tunnel (in a separate terminal or as a background service)
make tunnel

# Verify it works
make test
```

### SSH Config

The Makefile assumes an SSH host named `router`. Add this to `~/.ssh/config`:

```
Host router
    HostName 192.168.1.1    # your router's LAN IP
    User vyos               # or your VyOS username
    IdentityFile ~/.ssh/id_ed25519
```

Override the hostname in the Makefile with `ROUTER=your-host`.

## Deployment Details

### What gets deployed

| File | Location on Router | Purpose |
|------|-------------------|---------|
| `vyos-mcp-go` | `/config/user-data/vyos-mcp-go` | Static binary |
| `mcp-server.service` | `/config/user-data/mcp-server.service` | systemd unit |

That's it -- no config files, no API keys, no environment variables.

### Surviving reboots

Add the following to `/config/scripts/vyos-postconfig-bootup.script` on the router so the service starts automatically after every boot (including VyOS upgrades):

```bash
# MCP server
ln -sf /config/user-data/mcp-server.service /etc/systemd/system/mcp-server.service
systemctl daemon-reload
systemctl enable mcp-server
systemctl restart mcp-server
```

## MCP Client Configuration

### Claude Code

Add to your project's `.mcp.json`:

```json
{
  "mcpServers": {
    "vyos": {
      "type": "http",
      "url": "http://localhost:8384/mcp"
    }
  }
}
```

Requires an active SSH tunnel (see below).

### Other MCP clients

Any client that supports Streamable HTTP transport can connect to `http://localhost:8384/mcp` (with the tunnel active).

## SSH Tunnel

### Manual

```bash
ssh -L 8384:localhost:8384 -N router
```

### Persistent (systemd user service)

Create `~/.config/systemd/user/vyos-mcp-tunnel.service`:

```ini
[Unit]
Description=SSH tunnel to VyOS MCP server

[Service]
ExecStart=/usr/bin/ssh -N -L 8384:localhost:8384 -o ServerAliveInterval=30 -o ServerAliveCountMax=3 router
Restart=on-failure
RestartSec=10

[Install]
WantedBy=default.target
```

Then:

```bash
systemctl --user daemon-reload
systemctl --user enable --now vyos-mcp-tunnel
```

## Tools

18 tools organized by category:

### Configuration

| Tool | Description |
|------|-------------|
| `vyos_show_config` | Retrieve VyOS configuration at a path (JSON or raw format) |
| `vyos_set_config` | Set a configuration value |
| `vyos_batch_config` | Set or delete multiple values atomically |
| `vyos_delete_config` | Delete a configuration node |
| `vyos_config_exists` | Check if a configuration path exists |
| `vyos_return_values` | Get values at a configuration path |
| `vyos_commit` | Commit pending changes |
| `vyos_save_config` | Save running configuration to startup config |

### Operational

| Tool | Description |
|------|-------------|
| `vyos_show` | Run an operational show command |
| `vyos_reset` | Run a reset command |
| `vyos_generate` | Run a generate command |

### Diagnostics

| Tool | Description |
|------|-------------|
| `vyos_ping` | Ping a host from the router (uses mtr for latency data) |
| `vyos_traceroute` | Traceroute to a host (mtr with JSON output) |
| `vyos_dhcp_leases` | Show DHCP server leases |

### Monitoring

| Tool | Description |
|------|-------------|
| `vyos_system_info` | System version and build info |
| `vyos_interface_stats` | Interface statistics |
| `vyos_routing_table` | IP routing table |
| `vyos_health_check` | Combined check: version, uptime, CPU, memory, storage |

## Build System

The Makefile uses `ensure-go.sh` to automatically manage the Go toolchain:

- If your system Go is >= 1.26, it uses that
- Otherwise it downloads the latest Go from go.dev into `.goroot/` (cached, SHA256-verified)

```bash
make build      # Cross-compile static binary
make deploy     # Build + deploy + restart
make tunnel     # SSH tunnel to router
make test       # Quick handshake test
make clean      # Remove binary
make clean-go   # Remove downloaded Go toolchain
make clean-all  # Both
```

Override defaults:

```bash
make deploy ROUTER=my-router DEPLOY_DIR=/config/scripts
```

## Project Structure

```
vyos-mcp-go/
  main.go              HTTP server, Streamable HTTP handler, graceful shutdown
  client.go            VyOS CLI client (calls vyatta-op-cmd-wrapper, cli-shell-api, etc.)
  tools.go             18 MCP tool registrations
  mcp-server.service   systemd unit file
  ensure-go.sh         Auto-downloads Go toolchain if needed
  Makefile             Build, deploy, tunnel, test targets
  go.mod / go.sum      Go module files
```

## How It Works

### VyOS CLI Integration

The server creates a VyOS config session at startup (via `cli-shell-api getSessionEnv` + `setupSession`) and uses VyOS CLI tools directly:

| Operation | CLI Tool |
|-----------|----------|
| Show config | `/bin/cli-shell-api showConfig` |
| Config exists | `/bin/cli-shell-api existsActive` |
| Return values | `/bin/cli-shell-api returnActiveValues` |
| Set config | `/opt/vyatta/sbin/my_set` |
| Delete config | `/opt/vyatta/sbin/my_delete` |
| Commit | `/opt/vyatta/sbin/my_commit` |
| Save | `/usr/libexec/vyos/vyos-save-config.py` |
| Show (operational) | `/opt/vyatta/bin/vyatta-op-cmd-wrapper show` |
| Reset / Generate | `/opt/vyatta/bin/vyatta-op-cmd-wrapper reset/generate` |
| Traceroute | `/usr/libexec/vyos/op_mode/mtr_execute.py` |
| Config to JSON | `/usr/bin/vyos-config-to-json` |

This is the same approach the VyOS HTTP API server uses internally -- it shells out to these exact tools. By calling them directly, we skip the HTTPS/API-key/nginx layer entirely.

### MCP Transport

Uses the official [Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk)'s `StreamableHTTPHandler` on a single `/mcp` endpoint. This is the recommended transport (replacing the deprecated SSE transport), providing bidirectional JSON-RPC over HTTP with server-sent events for streaming responses.

## License

MIT
