package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- Input types ---

type showConfigInput struct {
	Path   []string `json:"path,omitempty" jsonschema:"Configuration path components"`
	Format string   `json:"format,omitempty" jsonschema:"Output format"`
}

type pathInput struct {
	Path []string `json:"path" jsonschema:"Configuration path components"`
}

type commitInput struct {
	Comment        *string `json:"comment,omitempty" jsonschema:"Optional commit comment"`
	ConfirmTimeout *int    `json:"confirmTimeout,omitempty" jsonschema:"Minutes before auto-rollback if not confirmed"`
}

type hostInput struct {
	Host string `json:"host" jsonschema:"Hostname or IP address"`
}

type pingInput struct {
	Host  string `json:"host" jsonschema:"Hostname or IP to ping"`
	Count int    `json:"count,omitempty" jsonschema:"Number of pings"`
}

// --- Helpers ---

func textResult(v any) (*mcp.CallToolResult, any, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("marshal result: %w", err)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(data)}},
	}, nil, nil
}

func textMsg(msg string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}, nil, nil
}

// registerTools adds all 18 VyOS MCP tools to the server.
func registerTools(s *mcp.Server, client *VyosClient) {
	// --- Config queries ---

	mcp.AddTool(s, &mcp.Tool{
		Name:        "vyos_show_config",
		Description: "Retrieve VyOS configuration at a path",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input showConfigInput) (*mcp.CallToolResult, any, error) {
		if input.Path == nil {
			input.Path = []string{}
		}
		if input.Format == "" {
			input.Format = "json"
		}
		result, err := client.ShowConfig(ctx, input.Path, input.Format)
		if err != nil {
			return nil, nil, err
		}
		return textResult(result)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "vyos_set_config",
		Description: "Set a VyOS configuration value",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input pathInput) (*mcp.CallToolResult, any, error) {
		if err := client.SetConfig(ctx, input.Path); err != nil {
			return nil, nil, err
		}
		return textMsg("Configuration set successfully")
	})

	// vyos_batch_config uses raw AddTool for complex array-of-objects schema
	s.AddTool(&mcp.Tool{
		Name:        "vyos_batch_config",
		Description: "Set or delete multiple configuration values atomically",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"operations": {
					"type": "array",
					"description": "Array of operations, each with \"op\" (set/delete) and \"path\"",
					"items": {
						"type": "object",
						"properties": {
							"op": {"type": "string", "enum": ["set", "delete"]},
							"path": {"type": "array", "items": {"type": "string"}}
						},
						"required": ["op", "path"]
					}
				}
			},
			"required": ["operations"]
		}`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args struct {
			Operations []map[string]any `json:"operations"`
		}
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
				IsError: true,
			}, nil
		}
		if err := client.BatchConfigure(ctx, args.Operations); err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
				IsError: true,
			}, nil
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{
				Text: fmt.Sprintf("Batch configuration applied successfully (%d operations)", len(args.Operations)),
			}},
		}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "vyos_delete_config",
		Description: "Delete a VyOS configuration node",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input pathInput) (*mcp.CallToolResult, any, error) {
		if err := client.DeleteConfig(ctx, input.Path); err != nil {
			return nil, nil, err
		}
		return textMsg("Configuration deleted successfully")
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "vyos_config_exists",
		Description: "Check if a configuration path exists",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input pathInput) (*mcp.CallToolResult, any, error) {
		exists, err := client.ConfigExists(ctx, input.Path)
		if err != nil {
			return nil, nil, err
		}
		return textResult(map[string]bool{"exists": exists})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "vyos_return_values",
		Description: "Get values at a configuration path",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input pathInput) (*mcp.CallToolResult, any, error) {
		result, err := client.ReturnValues(ctx, input.Path)
		if err != nil {
			return nil, nil, err
		}
		return textResult(result)
	})

	// --- Config persistence ---

	mcp.AddTool(s, &mcp.Tool{
		Name:        "vyos_commit",
		Description: "Commit pending configuration changes",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input commitInput) (*mcp.CallToolResult, any, error) {
		if err := client.Commit(ctx, input.Comment, input.ConfirmTimeout); err != nil {
			return nil, nil, err
		}
		return textMsg("Configuration committed successfully")
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "vyos_save_config",
		Description: "Save running configuration to startup config",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, any, error) {
		if err := client.Save(ctx); err != nil {
			return nil, nil, err
		}
		return textMsg("Configuration saved successfully")
	})

	// --- Operational commands ---

	mcp.AddTool(s, &mcp.Tool{
		Name:        "vyos_show",
		Description: "Run an operational show command",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input pathInput) (*mcp.CallToolResult, any, error) {
		result, err := client.Show(ctx, input.Path)
		if err != nil {
			return nil, nil, err
		}
		return textResult(result)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "vyos_reset",
		Description: "Run a reset command",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input pathInput) (*mcp.CallToolResult, any, error) {
		result, err := client.Reset(ctx, input.Path)
		if err != nil {
			return nil, nil, err
		}
		return textResult(result)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "vyos_generate",
		Description: "Run a generate command",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input pathInput) (*mcp.CallToolResult, any, error) {
		result, err := client.Generate(ctx, input.Path)
		if err != nil {
			return nil, nil, err
		}
		return textResult(result)
	})

	// --- Convenience tools ---

	mcp.AddTool(s, &mcp.Tool{
		Name:        "vyos_system_info",
		Description: "Get system version, uptime, and resource usage",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, any, error) {
		result, err := client.Show(ctx, []string{"version"})
		if err != nil {
			return nil, nil, err
		}
		return textResult(result)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "vyos_ping",
		Description: "Ping a host from the router",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input pingInput) (*mcp.CallToolResult, any, error) {
		count := input.Count
		if count <= 0 {
			count = 5
		}
		result, err := client.Ping(ctx, input.Host, count)
		if err != nil {
			return nil, nil, err
		}
		return textResult(result)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "vyos_traceroute",
		Description: "Traceroute to a host from the router",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input hostInput) (*mcp.CallToolResult, any, error) {
		result, err := client.Traceroute(ctx, input.Host)
		if err != nil {
			return nil, nil, err
		}
		return textResult(result)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "vyos_interface_stats",
		Description: "Show interface statistics",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, any, error) {
		result, err := client.Show(ctx, []string{"interfaces"})
		if err != nil {
			return nil, nil, err
		}
		return textResult(result)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "vyos_routing_table",
		Description: "Show routing table",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, any, error) {
		result, err := client.Show(ctx, []string{"ip", "route"})
		if err != nil {
			return nil, nil, err
		}
		return textResult(result)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "vyos_dhcp_leases",
		Description: "Show DHCP server leases",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, any, error) {
		result, err := client.Show(ctx, []string{"dhcp", "server", "leases"})
		if err != nil {
			return nil, nil, err
		}
		return textResult(result)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "vyos_health_check",
		Description: "System health check: CPU, memory, storage, uptime",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, any, error) {
		checks := map[string][]string{
			"version": {"version"},
			"uptime":  {"system", "uptime"},
			"cpu":     {"system", "cpu"},
			"memory":  {"system", "memory"},
			"storage": {"system", "storage"},
		}
		results := make(map[string]any)
		var mu sync.Mutex
		var wg sync.WaitGroup
		for label, path := range checks {
			wg.Add(1)
			go func() {
				defer wg.Done()
				result, err := client.Show(ctx, path)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					results[label] = fmt.Sprintf("Error: %s", err.Error())
				} else {
					results[label] = result
				}
			}()
		}
		wg.Wait()
		return textResult(results)
	})
}
