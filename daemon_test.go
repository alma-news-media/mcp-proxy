package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
)

func newTestDaemon(servers map[string]*MCPClientConfigV2) *daemon {
	ctx, cancel := context.WithCancel(context.Background())
	baseURL, _ := url.Parse("http://localhost:9090")
	hs := &swappableHandler{}
	hs.swap(http.NotFoundHandler())
	return &daemon{
		config: &Config{
			McpProxy: &MCPProxyConfigV2{
				Addr:    ":9090",
				Name:    "test",
				Version: "1.0",
				Type:    MCPServerTypeSSE,
				Options: &OptionsV2{},
			},
			McpServers: servers,
		},
		baseURL: baseURL,
		hSwitch: hs,
		ctx:     ctx,
		cancel:  cancel,
	}
}

func TestHandleConfigMerge_InvalidJSON(t *testing.T) {
	d := newTestDaemon(map[string]*MCPClientConfigV2{})
	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("not json"))
	w := httptest.NewRecorder()

	d.handleConfigMerge(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleConfigMerge_EmptyMcpServers(t *testing.T) {
	d := newTestDaemon(map[string]*MCPClientConfigV2{})
	body := `{"mcpServers": {}}`
	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader(body))
	w := httptest.NewRecorder()

	d.handleConfigMerge(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleConfigMerge_Conflict(t *testing.T) {
	d := newTestDaemon(map[string]*MCPClientConfigV2{
		"github": {
			TransportType: MCPClientTypeStreamable,
			URL:           "https://api.github.com/mcp",
			Headers:       map[string]string{"Authorization": "Bearer aaa"},
		},
	})

	body := `{
		"mcpServers": {
			"github": {
				"transportType": "streamable-http",
				"url": "https://api.github.com/mcp",
				"headers": {"Authorization": "Bearer DIFFERENT"}
			}
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader(body))
	w := httptest.NewRecorder()

	d.handleConfigMerge(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusConflict, w.Body.String())
	}

	var resp configConflictResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse conflict response: %v", err)
	}
	if len(resp.Conflicts) != 1 || resp.Conflicts[0] != "github" {
		t.Errorf("conflicts = %v, want [github]", resp.Conflicts)
	}
}

func requireMergeResponseOK(t *testing.T, w *httptest.ResponseRecorder) configMergeResponse {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	var resp configMergeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	return resp
}

func assertMergeResponseAddrAndServerNames(t *testing.T, resp configMergeResponse, wantAddr string, wantNames []string) {
	t.Helper()
	if resp.Addr != wantAddr {
		t.Errorf("addr = %q, want %q", resp.Addr, wantAddr)
	}
	got := slices.Clone(resp.Servers)
	slices.Sort(got)
	wantSorted := slices.Clone(wantNames)
	slices.Sort(wantSorted)
	if !slices.Equal(got, wantSorted) {
		t.Errorf("servers = %v, want %v", resp.Servers, wantNames)
	}
}

func assertDaemonAddNewServerMergeState(t *testing.T, d *daemon) {
	t.Helper()
	if d.config.McpServers["newsvc"] == nil {
		t.Fatal("expected merged config to include newsvc")
	}
	ns := d.config.McpServers["newsvc"]
	if ns.TransportType != MCPClientTypeStreamable {
		t.Errorf("newsvc transportType = %q, want streamable-http", ns.TransportType)
	}
	if ns.URL != "https://127.0.0.1:9/newsvc" {
		t.Errorf("newsvc URL = %q", ns.URL)
	}
	if ns.Headers["Authorization"] != "Bearer newsvc" {
		t.Errorf("newsvc headers = %v", ns.Headers)
	}
	if d.config.McpServers["github"].URL != "https://127.0.0.1:9/existing" {
		t.Errorf("github URL mutated: %q", d.config.McpServers["github"].URL)
	}
}

func TestHandleConfigMerge_IdenticalNoOp(t *testing.T) {
	d := newTestDaemon(map[string]*MCPClientConfigV2{
		"github": {
			TransportType: MCPClientTypeStreamable,
			URL:           "https://api.github.com/mcp",
			Headers:       map[string]string{"Authorization": "Bearer tok"},
		},
	})

	body := `{
		"mcpServers": {
			"github": {
				"transportType": "streamable-http",
				"url": "https://api.github.com/mcp",
				"headers": {"Authorization": "Bearer tok"}
			}
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader(body))
	w := httptest.NewRecorder()

	d.handleConfigMerge(w, req)

	resp := requireMergeResponseOK(t, w)
	assertMergeResponseAddrAndServerNames(t, resp, "localhost:9090", []string{"github"})
}

func TestHandleConfigMerge_AddNewServer(t *testing.T) {
	// Unreachable URLs; default PanicIfInvalid is false so wireServers still completes after failed handshakes.
	d := newTestDaemon(map[string]*MCPClientConfigV2{
		"github": {
			TransportType: MCPClientTypeStreamable,
			URL:           "https://127.0.0.1:9/existing",
			Headers:       map[string]string{"Authorization": "Bearer existing"},
			Options:       &OptionsV2{}, // prepareServerJobs reads Options.Disabled
		},
	})

	body := `{
		"mcpServers": {
			"newsvc": {
				"transportType": "streamable-http",
				"url": "https://127.0.0.1:9/newsvc",
				"headers": {"Authorization": "Bearer newsvc"}
			}
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader(body))
	w := httptest.NewRecorder()

	d.handleConfigMerge(w, req)

	resp := requireMergeResponseOK(t, w)
	assertMergeResponseAddrAndServerNames(t, resp, "localhost:9090", []string{"github", "newsvc"})
	assertDaemonAddNewServerMergeState(t, d)
}

func TestHandleConfigMerge_MultipleConflicts(t *testing.T) {
	d := newTestDaemon(map[string]*MCPClientConfigV2{
		"github": {URL: "https://a.example.com"},
		"jira":   {URL: "https://b.example.com"},
	})

	body := `{
		"mcpServers": {
			"github": {"url": "https://DIFFERENT.example.com"},
			"jira":   {"url": "https://ALSO-DIFFERENT.example.com"}
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader(body))
	w := httptest.NewRecorder()

	d.handleConfigMerge(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusConflict)
	}

	var resp configConflictResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse conflict response: %v", err)
	}
	if len(resp.Conflicts) != 2 {
		t.Errorf("expected 2 conflicts, got %v", resp.Conflicts)
	}
	// Should be sorted
	if resp.Conflicts[0] != "github" || resp.Conflicts[1] != "jira" {
		t.Errorf("conflicts should be sorted: got %v", resp.Conflicts)
	}
}

func TestRespondWithServerList_AddrNormalization(t *testing.T) {
	tests := []struct {
		addr     string
		wantAddr string
	}{
		{":9090", "localhost:9090"},
		{"0.0.0.0:8080", "0.0.0.0:8080"},
		{"localhost:3000", "localhost:3000"},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			d := newTestDaemon(map[string]*MCPClientConfigV2{
				"a": {URL: "https://a.example.com"},
				"b": {URL: "https://b.example.com"},
			})
			d.config.McpProxy.Addr = tt.addr

			w := httptest.NewRecorder()
			d.respondWithServerList(w)

			var resp configMergeResponse
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("failed to parse response: %v; body: %s", err, w.Body.String())
			}
			if resp.Addr != tt.wantAddr {
				t.Errorf("addr = %q, want %q", resp.Addr, tt.wantAddr)
			}
			if len(resp.Servers) != 2 {
				t.Errorf("servers count = %d, want 2", len(resp.Servers))
			}
			// Should be sorted
			if resp.Servers[0] != "a" || resp.Servers[1] != "b" {
				t.Errorf("servers should be sorted: got %v", resp.Servers)
			}
		})
	}
}

func TestHandlerSwitch(t *testing.T) {
	hs := &swappableHandler{}
	hs.swap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("v1"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	hs.ServeHTTP(w, req)
	if w.Body.String() != "v1" {
		t.Errorf("body = %q, want %q", w.Body.String(), "v1")
	}

	hs.swap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("v2"))
	}))

	w = httptest.NewRecorder()
	hs.ServeHTTP(w, req)
	if w.Body.String() != "v2" {
		t.Errorf("body = %q after swap, want %q", w.Body.String(), "v2")
	}
}
