package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- Annotation helpers ---

func boolPtr(b bool) *bool { return &b }

func readOnly(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:        title,
		ReadOnlyHint: true,
		OpenWorldHint: boolPtr(false),
	}
}

func writeOp(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    false,
		DestructiveHint: boolPtr(false),
		IdempotentHint:  true,
		OpenWorldHint:   boolPtr(false),
	}
}

func destructiveOp(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    false,
		DestructiveHint: boolPtr(true),
		IdempotentHint:  false,
		OpenWorldHint:   boolPtr(false),
	}
}

// --- Input types ---

type showConfigInput struct {
	Path   []string `json:"path,omitempty" jsonschema:"Configuration path components to retrieve. Each element is one level of the config tree. Example: ['interfaces', 'ethernet', 'eth0'] retrieves the eth0 interface config. Omit for the full configuration tree."`
	Format string   `json:"format,omitempty" jsonschema:"Output format. Use 'json' for structured JSON data (default); any other value returns the raw cli-shell-api showConfig output."`
}

type pathInput struct {
	Path []string `json:"path" jsonschema:"Path components where each element is one level of a VyOS hierarchy. For configuration tools this represents a config tree path; for operational tools it represents a command hierarchy. Example: ['interfaces', 'ethernet', 'eth0'] refers to the eth0 interface node."`
}

type commitInput struct {
	Comment        *string `json:"comment,omitempty" jsonschema:"Optional human-readable comment describing the configuration change. Stored in the commit log for auditing."`
	ConfirmTimeout *int    `json:"confirmTimeout,omitempty" jsonschema:"If set, the commit will auto-rollback after this many minutes unless confirmed with vyos_confirm. Use this as a safety net when making potentially disruptive changes like firewall or interface modifications."`
}

type hostInput struct {
	Host string `json:"host" jsonschema:"Target hostname or IP address (IPv4 or IPv6) to probe from the router."`
}

type pingInput struct {
	Host  string `json:"host" jsonschema:"Target hostname or IP address (IPv4 or IPv6) to ping from the router."`
	Count int    `json:"count,omitempty" jsonschema:"Number of ICMP echo requests to send. Defaults to 5 if omitted. Very large values may result in long waits before the command completes."`
}

// --- Result helpers ---

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

// registerTools adds all VyOS MCP tools to the server.
func registerTools(s *mcp.Server, client *VyosClient) {
	// --- Config queries ---

	mcp.AddTool(s, &mcp.Tool{
		Name: "vyos_show_config",
		Description: "Retrieve the VyOS router's running configuration at a given path. " +
			"Use this to inspect current settings before making changes, verify applied configuration, " +
			"or explore the configuration tree. Returns the full config tree if no path is specified. " +
			"Output is JSON by default, or VyOS set-style commands if format is \"commands\".",
		Annotations: readOnly("Show Configuration"),
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
		Name: "vyos_set_config",
		Description: "Set a VyOS configuration node in the candidate configuration. " +
			"The change is staged but not yet active — you must call vyos_commit to apply it. " +
			"For nodes that take a value, the path includes the value as the final element; " +
			"for valueless flag nodes (e.g., 'disable'), the path ends at the node name. " +
			"Idempotent: setting the same value twice has no effect.",
		Annotations: writeOp("Set Configuration"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, input pathInput) (*mcp.CallToolResult, any, error) {
		if err := client.SetConfig(ctx, input.Path); err != nil {
			return nil, nil, err
		}
		return textMsg("Configuration set successfully")
	})

	// vyos_batch_config uses raw AddTool for complex array-of-objects schema
	s.AddTool(&mcp.Tool{
		Name: "vyos_batch_config",
		Description: "Set or delete multiple configuration values in a single atomic operation. " +
			"Use this instead of multiple vyos_set_config/vyos_delete_config calls when making related changes " +
			"that should succeed or fail together. Changes are staged in the candidate configuration — " +
			"you must call vyos_commit to apply them. Each operation specifies \"set\" or \"delete\" and a path array.",
		Annotations: destructiveOp("Batch Configuration"),
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"operations": {
					"type": "array",
					"description": "Array of configuration operations to apply atomically. Each operation has an \"op\" field (\"set\" or \"delete\") and a \"path\" field (array of path components). Example: [{\"op\": \"set\", \"path\": [\"interfaces\", \"ethernet\", \"eth0\", \"description\", \"WAN\"]}, {\"op\": \"delete\", \"path\": [\"interfaces\", \"ethernet\", \"eth1\", \"disable\"]}]",
					"items": {
						"type": "object",
						"properties": {
							"op": {"type": "string", "enum": ["set", "delete"], "description": "The operation type: \"set\" to create or update a config node, \"delete\" to remove it."},
							"path": {"type": "array", "items": {"type": "string"}, "description": "Configuration path components. Each element is one level of the VyOS config tree; include the value as the final element when the node requires one."}
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
		Name: "vyos_delete_config",
		Description: "Delete a VyOS configuration node from the candidate configuration. " +
			"Removes the specified config path and all its children. The change is staged — " +
			"you must call vyos_commit to apply it. Use vyos_config_exists first to verify " +
			"the path exists if unsure. Idempotent: deleting a non-existent path is a no-op.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Delete Configuration",
			ReadOnlyHint:    false,
			DestructiveHint: boolPtr(true),
			IdempotentHint:  true,
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input pathInput) (*mcp.CallToolResult, any, error) {
		if err := client.DeleteConfig(ctx, input.Path); err != nil {
			return nil, nil, err
		}
		return textMsg("Configuration deleted successfully")
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "vyos_config_exists",
		Description: "Check whether a configuration path exists in the active running configuration. " +
			"Returns {\"exists\": true} if the path is present, {\"exists\": false} otherwise. " +
			"Use this to verify configuration state before making changes or to confirm that " +
			"a previous commit was applied correctly.",
		Annotations: readOnly("Check Configuration Exists"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, input pathInput) (*mcp.CallToolResult, any, error) {
		exists, err := client.ConfigExists(ctx, input.Path)
		if err != nil {
			return nil, nil, err
		}
		return textResult(map[string]bool{"exists": exists})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "vyos_return_values",
		Description: "Retrieve the values at a multi-value configuration path. " +
			"Some VyOS config nodes hold multiple values (e.g., DNS nameservers, NTP servers). " +
			"This returns all values as a string array. For single-value nodes, use vyos_show_config instead. " +
			"Returns an empty array if the path has no values.",
		Annotations: readOnly("Get Configuration Values"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, input pathInput) (*mcp.CallToolResult, any, error) {
		result, err := client.ReturnValues(ctx, input.Path)
		if err != nil {
			return nil, nil, err
		}
		return textResult(result)
	})

	// --- Config persistence ---

	mcp.AddTool(s, &mcp.Tool{
		Name: "vyos_commit",
		Description: "Commit pending configuration changes to make them active on the router. " +
			"All changes made via vyos_set_config, vyos_delete_config, or vyos_batch_config are staged " +
			"in a candidate configuration until committed. Use confirmTimeout for a safety net: " +
			"the router will auto-rollback to the previous config after the timeout unless vyos_confirm is called. " +
			"This is critical for remote changes that could cause connectivity loss. " +
			"After committing, use vyos_save_config to persist changes across reboots.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Commit Configuration",
			ReadOnlyHint:    false,
			DestructiveHint: boolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input commitInput) (*mcp.CallToolResult, any, error) {
		if err := client.Commit(ctx, input.Comment, input.ConfirmTimeout); err != nil {
			return nil, nil, err
		}
		return textMsg("Configuration committed successfully")
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "vyos_confirm",
		Description: "Confirm a pending commit-confirm operation, cancelling the scheduled auto-rollback. " +
			"This must be called after a vyos_commit with confirmTimeout before the timeout expires. " +
			"If not called in time, the router automatically reverts to the previous configuration. " +
			"Only needed when commit was made with a confirmTimeout value.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Confirm Commit",
			ReadOnlyHint:    false,
			DestructiveHint: boolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, any, error) {
		if err := client.Confirm(ctx); err != nil {
			return nil, nil, err
		}
		return textMsg("Commit confirmed successfully")
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "vyos_save_config",
		Description: "Save the running configuration to the startup configuration file (/config/config.boot). " +
			"Without saving, committed changes are lost on reboot. Call this after vyos_commit " +
			"once you've verified the changes are working correctly.",
		Annotations: writeOp("Save Configuration"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, any, error) {
		if err := client.Save(ctx); err != nil {
			return nil, nil, err
		}
		return textMsg("Configuration saved successfully")
	})

	// --- Operational commands ---

	mcp.AddTool(s, &mcp.Tool{
		Name: "vyos_show",
		Description: "Run a VyOS operational-mode show command and return its output. " +
			"This executes read-only operational commands (not configuration commands). " +
			"The path represents the show command hierarchy — for example, " +
			"[\"interfaces\"] runs \"show interfaces\", [\"ip\", \"route\"] runs \"show ip route\". " +
			"Returns the command output as a string. Use this for any operational query " +
			"not covered by a dedicated tool.",
		Annotations: readOnly("Run Show Command"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, input pathInput) (*mcp.CallToolResult, any, error) {
		result, err := client.Show(ctx, input.Path)
		if err != nil {
			return nil, nil, err
		}
		return textResult(result)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "vyos_reset",
		Description: "Run a VyOS operational-mode reset command. " +
			"Resets operational state such as connections, counters, or peers — " +
			"for example, [\"ip\", \"bgp\", \"neighbor\", \"192.168.1.1\"] resets a BGP session. " +
			"This does NOT modify the saved configuration, but does affect running state. " +
			"Use with caution as it can disrupt active sessions.",
		Annotations: destructiveOp("Run Reset Command"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, input pathInput) (*mcp.CallToolResult, any, error) {
		result, err := client.Reset(ctx, input.Path)
		if err != nil {
			return nil, nil, err
		}
		return textResult(result)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "vyos_generate",
		Description: "Run a VyOS operational-mode generate command. " +
			"Used to generate cryptographic keys, PKI certificates, and other artifacts — " +
			"for example, [\"pki\", \"key-pair\"] generates a new key pair. " +
			"Returns the command output containing the generated data, which may include " +
			"sensitive cryptographic material.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Run Generate Command",
			ReadOnlyHint:    false,
			DestructiveHint: boolPtr(true),
			IdempotentHint:  false,
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input pathInput) (*mcp.CallToolResult, any, error) {
		result, err := client.Generate(ctx, input.Path)
		if err != nil {
			return nil, nil, err
		}
		return textResult(result)
	})

	// --- Convenience tools ---

	mcp.AddTool(s, &mcp.Tool{
		Name: "vyos_system_info",
		Description: "Retrieve the VyOS system version, hardware platform, build date, and architecture. " +
			"Returns structured version information including the VyOS release string, " +
			"underlying Linux kernel version, and system type. " +
			"Use this to verify the router software version or check system identity.",
		Annotations: readOnly("Get System Information"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, any, error) {
		result, err := client.Show(ctx, []string{"version"})
		if err != nil {
			return nil, nil, err
		}
		return textResult(result)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "vyos_ping",
		Description: "Send ICMP echo requests to a host from the router and return the results. " +
			"Use this to test network reachability from the router's perspective, " +
			"diagnose routing issues, or verify connectivity to upstream providers. " +
			"Returns per-packet results and summary statistics (loss percentage, min/avg/max RTT).",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Ping Host",
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
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
		Name: "vyos_traceroute",
		Description: "Trace the network path from the router to a destination host, showing each hop. " +
			"Use this to diagnose routing issues, identify where packets are being dropped, " +
			"or understand the network path to a destination. Returns a list of intermediate " +
			"routers with their IP addresses and round-trip times.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Traceroute to Host",
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input hostInput) (*mcp.CallToolResult, any, error) {
		result, err := client.Traceroute(ctx, input.Host)
		if err != nil {
			return nil, nil, err
		}
		return textResult(result)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "vyos_interface_stats",
		Description: "Retrieve status and traffic statistics for all network interfaces on the router. " +
			"Returns interface names, link state (up/down), IP addresses, and packet/byte counters " +
			"for both TX and RX. Use this to monitor interface health, check for errors, " +
			"or verify link status after configuration changes.",
		Annotations: readOnly("Show Interface Statistics"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, any, error) {
		result, err := client.Show(ctx, []string{"interfaces"})
		if err != nil {
			return nil, nil, err
		}
		return textResult(result)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "vyos_routing_table",
		Description: "Retrieve the IPv4 routing table from the router's forwarding information base. " +
			"Shows all active routes including connected, static, and dynamically learned routes " +
			"with their next-hop addresses, outgoing interfaces, and route metrics. " +
			"Use this to verify routing decisions, debug connectivity issues, or check route propagation.",
		Annotations: readOnly("Show Routing Table"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, any, error) {
		result, err := client.Show(ctx, []string{"ip", "route"})
		if err != nil {
			return nil, nil, err
		}
		return textResult(result)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "vyos_dhcp_leases",
		Description: "List all active DHCP leases issued by the router's DHCP server. " +
			"Returns lease details including assigned IP addresses, MAC addresses, hostnames, " +
			"lease expiration times, and the DHCP pool each lease belongs to. " +
			"Use this to identify connected devices, troubleshoot IP assignment issues, " +
			"or audit network clients.",
		Annotations: readOnly("Show DHCP Leases"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, any, error) {
		result, err := client.Show(ctx, []string{"dhcp", "server", "leases"})
		if err != nil {
			return nil, nil, err
		}
		return textResult(result)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "vyos_health_check",
		Description: "Run a comprehensive health check of the router, returning CPU usage, " +
			"memory utilization, storage capacity, system uptime, and software version in a single call. " +
			"Use this for routine monitoring, alerting on resource exhaustion, or as a quick " +
			"system overview before making configuration changes. Returns a JSON object with " +
			"keys: version, uptime, cpu, memory, storage.",
		Annotations: readOnly("System Health Check"),
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
