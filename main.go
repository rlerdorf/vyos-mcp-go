package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	log.SetOutput(os.Stderr)

	addr := flag.String("addr", "127.0.0.1:8384", "listen address")
	flag.Parse()

	client, err := NewVyosClientFromEnv()
	if err != nil {
		log.Fatal(err)
	}

	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    "VyOS Router",
		Version: "2.0.0",
	}, nil)
	registerTools(mcpServer, client)

	handler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return mcpServer },
		nil,
	)

	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)

	srv := &http.Server{Addr: *addr, Handler: mux}

	// Graceful shutdown on SIGTERM/SIGINT
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	log.Printf("VyOS MCP server listening on %s", *addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
