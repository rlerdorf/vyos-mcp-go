# vyos-mcp-go

A native Go [MCP](https://modelcontextprotocol.io/) server for [VyOS](https://vyos.io/) routers. Compiles to a single static binary, runs directly on the router as a systemd service, and exposes 18 tools over Streamable HTTP transport.

## Why

- **Zero runtime dependencies** -- one static binary, no PHP/Python/Node
- **Persistent daemon** -- runs on the router itself, sessions survive across client reconnects
- **Survives upgrades** -- binary and config live in `/config/`, which persists across VyOS image upgrades
- **Low latency** -- the MCP server talks to the VyOS REST API over localhost instead of crossing the network

## Architecture

```
Workstation                          VyOS Router
+-----------+    SSH tunnel          +-------------------------+
| MCP Client|---(port 8384)-------->| vyos-mcp-go daemon      |
| (Claude,  |  http://localhost:8384| listening 127.0.0.1:8384|
|  etc.)    |                       | Streamable HTTP on /mcp |
+-----------+                       |         |               |
                                    |         v               |
                                    |  VyOS REST API          |
                                    |  https://<lan-ip>       |
                                    +-------------------------+
```

The daemon binds to `127.0.0.1` only -- it is not exposed to the network. Access it from your workstation through an SSH tunnel.

## Security

### Why localhost-only binding matters

The MCP server binds to `127.0.0.1:8384` and **must not** be exposed on a network interface. This is critical because:

- **The MCP protocol has no authentication.** Any client that can reach the HTTP endpoint can call any tool -- including `vyos_set_config`, `vyos_delete_config`, and `vyos_commit`. That's full read/write access to your router's configuration with no credentials required.
- **This runs on your router.** A compromised router means a compromised network. Firewall rules, NAT, DNS, DHCP -- all controlled through these tools.
- **The VyOS API key is baked into the daemon.** The server holds the API key in memory and uses it for every request. Exposing the MCP endpoint is equivalent to exposing the API key itself.

By binding to localhost, the only way to reach the server is through an SSH tunnel, which provides authentication (SSH keys), encryption, and access control that MCP itself lacks.

**Never** change the listen address to `0.0.0.0` or a LAN IP. If you need remote access, always use an SSH tunnel.

### Other measures

- API credentials are stored in a separate `.env` file with `chmod 600`
- The SSH tunnel inherits your existing SSH key authentication and encryption
- The VyOS REST API uses HTTPS (TLS verification is disabled for self-signed certs, which is standard for VyOS)

## Prerequisites

- VyOS 1.4+ with the [REST API enabled](https://docs.vyos.io/en/latest/configuration/service/https.html)
- SSH access to the router (key-based recommended)
- Go 1.26+ on your build machine (or let `ensure-go.sh` download it automatically)

### Enabling the VyOS REST API

If the API isn't already configured:

```
configure
set service https api keys id my-key key 'YOUR-SECRET-KEY-HERE'
set service https listen-address <your-lan-ip>
commit
save
```

Note the API key and LAN IP -- you'll need them for deployment.

## Quick Start

```bash
git clone https://github.com/youruser/vyos-mcp-go.git
cd vyos-mcp-go

# Build (cross-compiles a static linux/amd64 binary)
make build

# First-time deploy to router
# Requires: SSH host "router" configured in ~/.ssh/config
make deploy-init VYOS_API_KEY='your-api-key' VYOS_LAN_IP='192.168.1.1'

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
| `.env` | `/config/user-data/.env` | API credentials (chmod 600) |

### Environment variables

The `.env` file on the router contains:

```
VYOS_HOST=https://192.168.1.1
VYOS_API_KEY=your-api-key-here
```

`VYOS_HOST` must point to the IP where the VyOS REST API listens (typically the LAN interface IP, not `127.0.0.1`, since the API binds to the configured `listen-address`).

### Subsequent deployments

After the initial deploy, use `make deploy` -- it skips `.env` creation and just updates the binary and service file:

```bash
make deploy
```

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
| `vyos_show_config` | Retrieve VyOS configuration at a path |
| `vyos_set_config` | Set a configuration value |
| `vyos_batch_config` | Set or delete multiple values atomically |
| `vyos_delete_config` | Delete a configuration node |
| `vyos_config_exists` | Check if a configuration path exists |
| `vyos_return_values` | Get values at a configuration path |
| `vyos_commit` | Commit pending changes (with optional comment and confirm timeout) |
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
| `vyos_ping` | Ping a host from the router (uses traceroute/mtr -- VyOS has no ping API) |
| `vyos_traceroute` | Traceroute to a host |
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
  client.go            VyOS REST API client (multipart form POST)
  tools.go             18 MCP tool registrations
  mcp-server.service   systemd unit file
  ensure-go.sh         Auto-downloads Go toolchain if needed
  Makefile             Build, deploy, tunnel, test targets
  go.mod / go.sum      Go module files
```

## How It Works

### VyOS REST API

The VyOS REST API uses POST requests with multipart form data:

- `data` field: JSON with operation details (`op`, `path`, etc.)
- `key` field: API key string

The `client.go` file wraps all [VyOS API endpoints](https://docs.vyos.io/en/latest/configuration/service/https.html): `/retrieve`, `/configure`, `/config-file`, `/show`, `/generate`, `/reset`, `/traceroute`.

### MCP Transport

Uses the official [Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk)'s `StreamableHTTPHandler` on a single `/mcp` endpoint. This is the recommended transport (replacing the deprecated SSE transport), providing bidirectional JSON-RPC over HTTP with server-sent events for streaming responses.

## License

MIT
