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

.PHONY: all build deploy tunnel test clean clean-go clean-all

all: build

build:
	$(eval GO := $(resolve_go))
	@test -n "$(GO)" || { echo "error: ensure-go.sh failed to resolve a Go binary" >&2; exit 1; }
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -ldflags="-s -w" -o $(BINARY) .

deploy: build
	ssh $(ROUTER) "sudo systemctl stop mcp-server 2>/dev/null || true"
	scp $(BINARY) mcp-server.service $(ROUTER):$(DEPLOY_DIR)/
	ssh $(ROUTER) "sudo ln -sf $(DEPLOY_DIR)/mcp-server.service /etc/systemd/system/ && sudo systemctl daemon-reload && sudo systemctl restart mcp-server"
	@echo "Deployed. Check status: ssh $(ROUTER) 'sudo systemctl status mcp-server'"

tunnel:
	ssh -L 8384:localhost:8384 -N $(ROUTER)

test:
	@./test.sh

clean:
	rm -f $(BINARY)

clean-go:
	rm -rf .goroot

clean-all: clean clean-go
