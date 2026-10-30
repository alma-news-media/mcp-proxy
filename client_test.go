package main

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestToolFilterControlsDiscoveryAndCalls(t *testing.T) {
	tests := []struct {
		name, filter string
		allowed      map[string]bool
	}{
		{"no filter", "", map[string]bool{"read": true, "write": true}},
		{"empty allow list", `,"toolFilter":{"mode":"allow","list":[]}`, nil},
		{"omitted allow list", `,"toolFilter":{"mode":"allow"}`, nil},
		{"null allow list", `,"toolFilter":{"mode":"allow","list":null}`, nil},
		{"allow selected", `,"toolFilter":{"mode":"ALLOW","list":["read"]}`, map[string]bool{"read": true}},
		{"empty block list", `,"toolFilter":{"mode":"block","list":[]}`, map[string]bool{"read": true, "write": true}},
		{"block selected", `,"toolFilter":{"mode":"block","list":["write"]}`, map[string]bool{"read": true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := filepath.Join(t.TempDir(), "config.json")
			configJSON := fmt.Sprintf(`{"mcpProxy":{"type":"streamable-http"},"mcpServers":{"upstream":{"command":"unused","options":{"authTokens":["test-token"]%s}}}}`, tt.filter)
			if err := os.WriteFile(configPath, []byte(configJSON), 0600); err != nil {
				t.Fatal(err)
			}
			config, err := loadConfigFile(configPath, false)
			if err != nil {
				t.Fatal(err)
			}

			ctx := context.Background()
			calls := make(map[string]int)
			upstream := server.NewMCPServer("upstream", "1.0")
			for _, name := range []string{"read", "write"} {
				upstream.AddTool(mcp.NewTool(name), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					calls[req.Params.Name]++
					return mcp.NewToolResultText("ok"), nil
				})
			}
			upstreamClient, err := client.NewInProcessClient(upstream)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = upstreamClient.Close() })
			if err := upstreamClient.Start(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := upstreamClient.Initialize(ctx, mcp.InitializeRequest{}); err != nil {
				t.Fatal(err)
			}

			clientConfig := config.McpServers["upstream"]
			proxy, err := newMCPServer("upstream", config.McpProxy, clientConfig)
			if err != nil {
				t.Fatal(err)
			}
			proxyClient := &Client{name: "upstream", client: upstreamClient, options: clientConfig.Options}
			if err := proxyClient.addToolsToServer(ctx, proxy.mcpServer); err != nil {
				t.Fatal(err)
			}
			httpServer := httptest.NewServer(chainMiddleware(proxy.handler, buildMiddlewares("upstream", clientConfig.Options)...))
			t.Cleanup(httpServer.Close)
			downstream, err := client.NewStreamableHttpClient(httpServer.URL, transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer test-token"}))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = downstream.Close() })
			if err := downstream.Start(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := downstream.Initialize(ctx, mcp.InitializeRequest{}); err != nil {
				t.Fatal(err)
			}
			listed, err := downstream.ListTools(ctx, mcp.ListToolsRequest{})
			got := make(map[string]bool)
			if err != nil {
				if len(tt.allowed) != 0 || !strings.Contains(err.Error(), "tools not supported") {
					t.Fatalf("list tools: %v", err)
				}
			} else {
				for _, tool := range listed.Tools {
					got[tool.Name] = true
				}
			}
			for _, name := range []string{"read", "write"} {
				if got[name] != tt.allowed[name] {
					t.Errorf("tool %q listed = %v, want %v", name, got[name], tt.allowed[name])
				}
				request := mcp.CallToolRequest{}
				request.Params.Name = name
				result, err := downstream.CallTool(ctx, request)
				if tt.allowed[name] {
					if err != nil || result == nil || result.IsError || calls[name] != 1 {
						t.Errorf("allowed tool %q: result = %v, error = %v, upstream calls = %d", name, result, err, calls[name])
					}
				} else if err == nil || calls[name] != 0 {
					t.Errorf("filtered tool %q: error = %v, upstream calls = %d; want rejection without an upstream call", name, err, calls[name])
				}
			}
		})
	}
}
