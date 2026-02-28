package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"
)

// VyosClient wraps the VyOS REST API.
// Direct port of VyosClient.php.
type VyosClient struct {
	host   string
	apiKey string
	http   *http.Client
}

func NewVyosClient(host, apiKey string) *VyosClient {
	return &VyosClient{
		host:   strings.TrimRight(host, "/"),
		apiKey: apiKey,
		http: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}
}

func NewVyosClientFromEnv() (*VyosClient, error) {
	host := os.Getenv("VYOS_HOST")
	if host == "" {
		host = "https://127.0.0.1"
	}
	apiKey := os.Getenv("VYOS_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("VYOS_API_KEY environment variable is required")
	}
	return NewVyosClient(host, apiKey), nil
}

// request sends a POST with multipart form data (data + key) to the VyOS API.
func (c *VyosClient) request(ctx context.Context, endpoint string, data any) (any, error) {
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal request data: %w", err)
	}

	var body strings.Builder
	w := multipart.NewWriter(&body)
	w.WriteField("data", string(dataJSON))
	w.WriteField("key", c.apiKey)
	w.Close()

	req, err := http.NewRequestWithContext(ctx, "POST", c.host+endpoint, strings.NewReader(body.String()))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("VyOS API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("VyOS API returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	// Try to parse as JSON
	var decoded map[string]any
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		// Not JSON — return raw response string
		return string(respBody), nil
	}

	if success, ok := decoded["success"].(bool); ok && !success {
		if errMsg, ok := decoded["error"].(string); ok {
			return nil, fmt.Errorf("VyOS API error: %s", errMsg)
		}
		if dataMsg, ok := decoded["data"].(string); ok {
			return nil, fmt.Errorf("VyOS API error: %s", dataMsg)
		}
		return nil, fmt.Errorf("VyOS API error: unknown error")
	}

	if d, ok := decoded["data"]; ok {
		return d, nil
	}
	return decoded, nil
}

// --- Config operations → /retrieve ---

func (c *VyosClient) ShowConfig(ctx context.Context, path []string, format string) (any, error) {
	data := map[string]any{"op": "showConfig", "path": path}
	if format == "raw" {
		data["configFormat"] = "raw"
	}
	return c.request(ctx, "/retrieve", data)
}

func (c *VyosClient) ConfigExists(ctx context.Context, path []string) (bool, error) {
	result, err := c.request(ctx, "/retrieve", map[string]any{
		"op":   "exists",
		"path": path,
	})
	if err != nil {
		return false, err
	}
	if b, ok := result.(bool); ok {
		return b, nil
	}
	return result != nil, nil
}

func (c *VyosClient) ReturnValues(ctx context.Context, path []string) (any, error) {
	return c.request(ctx, "/retrieve", map[string]any{
		"op":   "returnValues",
		"path": path,
	})
}

// --- Config changes → /configure ---

func (c *VyosClient) SetConfig(ctx context.Context, path []string) error {
	_, err := c.request(ctx, "/configure", map[string]any{
		"op":   "set",
		"path": path,
	})
	return err
}

func (c *VyosClient) BatchConfigure(ctx context.Context, operations []map[string]any) error {
	_, err := c.request(ctx, "/configure", operations)
	return err
}

func (c *VyosClient) DeleteConfig(ctx context.Context, path []string) error {
	_, err := c.request(ctx, "/configure", map[string]any{
		"op":     "delete",
		"path":   path,
	})
	return err
}

// --- Config persistence → /config-file ---

func (c *VyosClient) Commit(ctx context.Context, comment *string, confirmTimeout *int) error {
	data := map[string]any{"op": "commit"}
	if comment != nil {
		data["comment"] = *comment
	}
	if confirmTimeout != nil {
		data["confirm"] = *confirmTimeout
	}
	_, err := c.request(ctx, "/config-file", data)
	return err
}

func (c *VyosClient) Save(ctx context.Context) error {
	_, err := c.request(ctx, "/config-file", map[string]any{"op": "save"})
	return err
}

// --- Operational → /show, /reset, /generate ---

func (c *VyosClient) Show(ctx context.Context, path []string) (any, error) {
	return c.request(ctx, "/show", map[string]any{"op": "show", "path": path})
}

func (c *VyosClient) Reset(ctx context.Context, path []string) (any, error) {
	return c.request(ctx, "/reset", map[string]any{"op": "reset", "path": path})
}

func (c *VyosClient) Generate(ctx context.Context, path []string) (any, error) {
	return c.request(ctx, "/generate", map[string]any{"op": "generate", "path": path})
}

// --- Diagnostics → /traceroute ---

func (c *VyosClient) Traceroute(ctx context.Context, host string) (any, error) {
	return c.request(ctx, "/traceroute", map[string]any{"op": "traceroute", "host": host})
}
