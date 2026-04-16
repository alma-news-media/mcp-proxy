package main

import (
	"testing"
	"time"

	"github.com/tbxark/optional-go"
)

type mcpClientConfigEqualCase struct {
	name string
	a, b *MCPClientConfigV2
	want bool
}

func runMcpClientConfigEqualCases(t *testing.T, cases []mcpClientConfigEqualCase) {
	t.Helper()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := mcpClientConfigEqual(tt.a, tt.b); got != tt.want {
				t.Errorf("mcpClientConfigEqual() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMcpClientConfigEqual_NilAndIdentity(t *testing.T) {
	base := &MCPClientConfigV2{
		TransportType: MCPClientTypeStreamable,
		URL:           "https://api.example.com/mcp",
		Headers:       map[string]string{"Authorization": "Bearer tok"},
	}
	runMcpClientConfigEqualCases(t, []mcpClientConfigEqualCase{
		{name: "both nil", a: nil, b: nil, want: true},
		{name: "one nil", a: base, b: nil, want: false},
		{name: "same pointer", a: base, b: base, want: true},
		{
			name: "identical values",
			a:    base,
			b: &MCPClientConfigV2{
				TransportType: MCPClientTypeStreamable,
				URL:           "https://api.example.com/mcp",
				Headers:       map[string]string{"Authorization": "Bearer tok"},
			},
			want: true,
		},
	})
}

func TestMcpClientConfigEqual_StreamableFields(t *testing.T) {
	base := &MCPClientConfigV2{
		TransportType: MCPClientTypeStreamable,
		URL:           "https://api.example.com/mcp",
		Headers:       map[string]string{"Authorization": "Bearer tok"},
	}
	runMcpClientConfigEqualCases(t, []mcpClientConfigEqualCase{
		{
			name: "different URL",
			a:    base,
			b: &MCPClientConfigV2{
				TransportType: MCPClientTypeStreamable,
				URL:           "https://other.example.com/mcp",
				Headers:       map[string]string{"Authorization": "Bearer tok"},
			},
			want: false,
		},
		{
			name: "different transport type",
			a:    base,
			b: &MCPClientConfigV2{
				TransportType: MCPClientTypeSSE,
				URL:           "https://api.example.com/mcp",
				Headers:       map[string]string{"Authorization": "Bearer tok"},
			},
			want: false,
		},
		{
			name: "different headers",
			a:    base,
			b: &MCPClientConfigV2{
				TransportType: MCPClientTypeStreamable,
				URL:           "https://api.example.com/mcp",
				Headers:       map[string]string{"Authorization": "Bearer different"},
			},
			want: false,
		},
	})
}

func TestMcpClientConfigEqual_Stdio(t *testing.T) {
	runMcpClientConfigEqualCases(t, []mcpClientConfigEqualCase{
		{
			name: "stdio identical",
			a: &MCPClientConfigV2{
				TransportType: MCPClientTypeStdio,
				Command:       "node",
				Args:          []string{"server.js", "--port=3000"},
				Env:           map[string]string{"NODE_ENV": "production"},
			},
			b: &MCPClientConfigV2{
				TransportType: MCPClientTypeStdio,
				Command:       "node",
				Args:          []string{"server.js", "--port=3000"},
				Env:           map[string]string{"NODE_ENV": "production"},
			},
			want: true,
		},
		{
			name: "stdio different args",
			a: &MCPClientConfigV2{
				TransportType: MCPClientTypeStdio,
				Command:       "node",
				Args:          []string{"server.js"},
			},
			b: &MCPClientConfigV2{
				TransportType: MCPClientTypeStdio,
				Command:       "node",
				Args:          []string{"server.js", "--verbose"},
			},
			want: false,
		},
	})
}

func TestMcpClientConfigEqual_TimeoutAndOptions(t *testing.T) {
	runMcpClientConfigEqualCases(t, []mcpClientConfigEqualCase{
		{
			name: "different timeout",
			a: &MCPClientConfigV2{
				URL:     "https://api.example.com/mcp",
				Timeout: 5 * time.Second,
			},
			b: &MCPClientConfigV2{
				URL:     "https://api.example.com/mcp",
				Timeout: 10 * time.Second,
			},
			want: false,
		},
		{
			name: "same options",
			a: &MCPClientConfigV2{
				URL:     "https://api.example.com/mcp",
				Options: &OptionsV2{Disabled: true},
			},
			b: &MCPClientConfigV2{
				URL:     "https://api.example.com/mcp",
				Options: &OptionsV2{Disabled: true},
			},
			want: true,
		},
		{
			name: "different options",
			a: &MCPClientConfigV2{
				URL:     "https://api.example.com/mcp",
				Options: &OptionsV2{Disabled: true},
			},
			b: &MCPClientConfigV2{
				URL:     "https://api.example.com/mcp",
				Options: &OptionsV2{Disabled: false},
			},
			want: false,
		},
	})
}

func TestInheritClientDefaults(t *testing.T) {
	proxyOpts := &OptionsV2{
		AuthTokens:     []string{"proxy-token"},
		PanicIfInvalid: optional.NewField(true),
		LogEnabled:     optional.NewField(true),
	}

	t.Run("nil options gets all defaults", func(t *testing.T) {
		client := &MCPClientConfigV2{URL: "https://example.com"}
		inheritClientDefaults(client, proxyOpts)

		if client.Options == nil {
			t.Fatal("Options should be initialized")
		}
		if len(client.Options.AuthTokens) != 1 || client.Options.AuthTokens[0] != "proxy-token" {
			t.Errorf("AuthTokens = %v, want [proxy-token]", client.Options.AuthTokens)
		}
		if !client.Options.PanicIfInvalid.OrElse(false) {
			t.Error("PanicIfInvalid should inherit true from proxy")
		}
		if !client.Options.LogEnabled.OrElse(false) {
			t.Error("LogEnabled should inherit true from proxy")
		}
	})

	t.Run("existing options not overwritten", func(t *testing.T) {
		client := &MCPClientConfigV2{
			URL: "https://example.com",
			Options: &OptionsV2{
				AuthTokens:     []string{"client-token"},
				PanicIfInvalid: optional.NewField(false),
				LogEnabled:     optional.NewField(false),
			},
		}
		inheritClientDefaults(client, proxyOpts)

		if client.Options.AuthTokens[0] != "client-token" {
			t.Errorf("AuthTokens should keep client value, got %v", client.Options.AuthTokens)
		}
		if client.Options.PanicIfInvalid.OrElse(true) {
			t.Error("PanicIfInvalid should keep client value false")
		}
		if client.Options.LogEnabled.OrElse(true) {
			t.Error("LogEnabled should keep client value false")
		}
	})
}

func TestApplyConfigDefaults(t *testing.T) {
	t.Run("nil mcpProxy returns error", func(t *testing.T) {
		conf := &FullConfig{}
		_, err := applyConfigDefaults(conf)
		if err == nil {
			t.Error("expected error for nil mcpProxy")
		}
	})

	t.Run("nil mcpServers becomes empty map", func(t *testing.T) {
		conf := &FullConfig{
			McpProxy: &MCPProxyConfigV2{Addr: ":9090"},
		}
		cfg, err := applyConfigDefaults(conf)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.McpServers == nil {
			t.Error("McpServers should be initialized to empty map")
		}
		if len(cfg.McpServers) != 0 {
			t.Errorf("McpServers should be empty, got %d entries", len(cfg.McpServers))
		}
	})

	t.Run("empty type defaults to SSE", func(t *testing.T) {
		conf := &FullConfig{
			McpProxy: &MCPProxyConfigV2{Addr: ":9090"},
		}
		cfg, err := applyConfigDefaults(conf)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.McpProxy.Type != MCPServerTypeSSE {
			t.Errorf("Type = %q, want %q", cfg.McpProxy.Type, MCPServerTypeSSE)
		}
	})

	t.Run("explicit type preserved", func(t *testing.T) {
		conf := &FullConfig{
			McpProxy: &MCPProxyConfigV2{
				Addr: ":9090",
				Type: MCPServerTypeStreamable,
			},
		}
		cfg, err := applyConfigDefaults(conf)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.McpProxy.Type != MCPServerTypeStreamable {
			t.Errorf("Type = %q, want %q", cfg.McpProxy.Type, MCPServerTypeStreamable)
		}
	})

	t.Run("client options inherit proxy defaults", func(t *testing.T) {
		conf := &FullConfig{
			McpProxy: &MCPProxyConfigV2{
				Addr:    ":9090",
				Options: &OptionsV2{AuthTokens: []string{"shared-token"}},
			},
			McpServers: map[string]*MCPClientConfigV2{
				"github": {URL: "https://github.example.com"},
			},
		}
		cfg, err := applyConfigDefaults(conf)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gh := cfg.McpServers["github"]
		if gh.Options == nil || gh.Options.AuthTokens[0] != "shared-token" {
			t.Errorf("github should inherit proxy auth token, got %v", gh.Options)
		}
	})
}
