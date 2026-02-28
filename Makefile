# Makefile for vyos-mcp-go
#
# Usage:
#   make              - Cross-compile static binary for linux/amd64
#   make deploy       - Build + scp to router + restart service
#   make tunnel       - Open SSH tunnel to MCP server on router
#   make test         - Curl the MCP endpoint to verify it responds
#   make clean        - Remove built binary
#   make clean-go     - Remove downloaded Go toolchain
#   make clean-all    - Remove binary and Go toolchain

MIN_GO_VERSION ?= 1.26.0
BINARY         ?= vyos-mcp-go
ROUTER         ?= router
DEPLOY_DIR     ?= /config/user-data

resolve_go = $(shell ./ensure-go.sh $(MIN_GO_VERSION))

.PHONY: all build deploy deploy-init tunnel test clean clean-go clean-all

all: build

build:
	$(eval GO := $(resolve_go))
	@test -n "$(GO)" || { echo "error: ensure-go.sh failed to resolve a Go binary" >&2; exit 1; }
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -ldflags="-s -w" -o $(BINARY) .

# First-time deployment: creates .env with credentials
# Set VYOS_API_KEY in your environment or pass it to make:
#   make deploy-init VYOS_API_KEY=your-api-key VYOS_LAN_IP=192.168.1.1
VYOS_LAN_IP ?= 192.168.1.1
deploy-init: build
	scp $(BINARY) mcp-server.service $(ROUTER):$(DEPLOY_DIR)/
	@test -n "$(VYOS_API_KEY)" || { echo "error: VYOS_API_KEY is required. Usage: make deploy-init VYOS_API_KEY=your-key VYOS_LAN_IP=your-router-lan-ip" >&2; exit 1; }
	ssh $(ROUTER) "sudo chmod 600 $(DEPLOY_DIR)/.env 2>/dev/null; test -f $(DEPLOY_DIR)/.env || (echo 'VYOS_HOST=https://$(VYOS_LAN_IP)' > $(DEPLOY_DIR)/.env && echo 'VYOS_API_KEY=$(VYOS_API_KEY)' >> $(DEPLOY_DIR)/.env && chmod 600 $(DEPLOY_DIR)/.env)"
	ssh $(ROUTER) "sudo ln -sf $(DEPLOY_DIR)/mcp-server.service /etc/systemd/system/ && sudo systemctl daemon-reload && sudo systemctl restart mcp-server"
	@echo "Deployed. Check status: ssh $(ROUTER) 'sudo systemctl status mcp-server'"

# Regular deployment (assumes .env already exists)
deploy: build
	scp $(BINARY) mcp-server.service $(ROUTER):$(DEPLOY_DIR)/
	ssh $(ROUTER) "sudo ln -sf $(DEPLOY_DIR)/mcp-server.service /etc/systemd/system/ && sudo systemctl daemon-reload && sudo systemctl restart mcp-server"
	@echo "Deployed. Check status: ssh $(ROUTER) 'sudo systemctl status mcp-server'"

tunnel:
	ssh -L 8384:localhost:8384 -N $(ROUTER)

test:
	@echo "Testing MCP endpoint..."
	@curl -s -X POST http://localhost:8384/mcp \
		-H 'Content-Type: application/json' \
		-H 'Accept: application/json, text/event-stream' \
		-d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}' \
		|| echo "Failed - is the tunnel running?"

clean:
	rm -f $(BINARY)

clean-go:
	rm -rf .goroot

clean-all: clean clean-go
