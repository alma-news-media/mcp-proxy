package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

const (
	daemonSocketName = "darkside-mcp-proxy.sock"
	daemonPIDName    = "darkside-mcp-proxy.pid"
	daemonRunDir     = ".local/run"
)

func daemonRunPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Cannot determine home directory: %v", err)
	}
	return filepath.Join(home, daemonRunDir)
}

func daemonSocketPath() string {
	return filepath.Join(daemonRunPath(), daemonSocketName)
}

func daemonPIDPath() string {
	return filepath.Join(daemonRunPath(), daemonPIDName)
}

// configMergeResponse is the JSON body returned by POST /config on success.
type configMergeResponse struct {
	Addr    string   `json:"addr"`
	Servers []string `json:"servers"`
}

// configConflictResponse is the JSON body returned on 409 Conflict.
type configConflictResponse struct {
	Conflicts []string `json:"conflicts"`
}

// daemon holds all state for a running daemon instance.
type daemon struct {
	config  *Config
	baseURL *url.URL
	mu      sync.Mutex

	httpServer   *http.Server
	socketServer *http.Server
	socketLn     net.Listener

	hSwitch *swappableHandler
	closers []func()

	ctx    context.Context
	cancel context.CancelFunc
}

func startDaemon(config *Config) error {
	d, err := newDaemonForConfig(config)
	if err != nil {
		return err
	}
	return runDaemonUntilSignal(d, config)
}

// newDaemonForConfig prepares PID file, optional initial wiring, and the HTTP server; it does not listen yet.
func newDaemonForConfig(config *Config) (*daemon, error) {
	baseURL, err := url.Parse(config.McpProxy.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse baseURL: %w", err)
	}

	if err := os.MkdirAll(daemonRunPath(), 0o755); err != nil {
		return nil, fmt.Errorf("create run directory: %w", err)
	}

	if isDaemonRunning() {
		return nil, fmt.Errorf("daemon already running: another mcp-proxy instance is active (PID file or control socket)")
	}

	ctx, cancel := context.WithCancel(context.Background())
	d := &daemon{
		config:  config,
		baseURL: baseURL,
		ctx:     ctx,
		cancel:  cancel,
	}

	if err := d.writePIDFile(); err != nil {
		cancel()
		return nil, err
	}

	d.hSwitch = &swappableHandler{}
	d.hSwitch.swap(http.NotFoundHandler())

	if len(config.McpServers) > 0 {
		result, wireErr := wireServers(ctx, config, baseURL)
		if wireErr != nil {
			d.cleanup()
			cancel()
			return nil, fmt.Errorf("wire initial servers: %w", wireErr)
		}
		d.hSwitch.swap(result.handler)
		d.closers = result.closers
	}

	d.httpServer = &http.Server{
		Addr:    config.McpProxy.Addr,
		Handler: d.hSwitch,
	}

	return d, nil
}

func runDaemonUntilSignal(d *daemon, config *Config) error {
	socketPath := daemonSocketPath()
	_ = os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		d.cleanup()
		d.cancel()
		return fmt.Errorf("listen unix socket: %w", err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		ln.Close()
		d.cleanup()
		d.cancel()
		return fmt.Errorf("chmod socket: %w", err)
	}
	d.socketLn = ln

	socketMux := http.NewServeMux()
	socketMux.HandleFunc("POST /config", d.handleConfigMerge)
	d.socketServer = &http.Server{Handler: socketMux}

	errChan := make(chan error, 2)

	go func() {
		log.Printf("Unix socket listening on %s", socketPath)
		if sErr := d.socketServer.Serve(ln); sErr != nil && !errors.Is(sErr, http.ErrServerClosed) {
			errChan <- fmt.Errorf("unix socket server: %w", sErr)
		}
	}()

	tcpLn, err := listenTCPReuseAddr(config.McpProxy.Addr)
	if err != nil {
		_ = d.socketServer.Shutdown(context.Background())
		ln.Close()
		d.cleanup()
		d.cancel()
		return fmt.Errorf("listen tcp: %w", err)
	}

	go func() {
		log.Printf("Starting %s server", config.McpProxy.Type)
		log.Printf("%s server listening on %s", config.McpProxy.Type, tcpLn.Addr().String())
		if hErr := d.httpServer.Serve(tcpLn); hErr != nil && !errors.Is(hErr, http.ErrServerClosed) {
			errChan <- fmt.Errorf("http server: %w", hErr)
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-sigChan:
		log.Println("Shutdown signal received")
	case err := <-errChan:
		log.Printf("Server error: %v", err)
	}

	d.shutdown()
	return nil
}

func (d *daemon) writePIDFile() error {
	pidPath := daemonPIDPath()
	return os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o644)
}

func (d *daemon) cleanup() {
	_ = os.Remove(daemonPIDPath())
	_ = os.Remove(daemonSocketPath())
}

func (d *daemon) shutdown() {
	if d.socketServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), httpServerShutdownTimeout)
		_ = d.socketServer.Shutdown(ctx)
		cancel()
	}
	if d.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), httpServerShutdownTimeout)
		_ = d.httpServer.Shutdown(ctx)
		cancel()
	}

	d.mu.Lock()
	for _, fn := range d.closers {
		fn()
	}
	d.closers = nil
	d.mu.Unlock()

	d.cancel()
	d.cleanup()
	log.Println("Daemon shut down cleanly")
}

func (d *daemon) handleConfigMerge(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	var incoming struct {
		McpServers map[string]*MCPClientConfigV2 `json:"mcpServers"`
	}
	if err := json.Unmarshal(body, &incoming); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(incoming.McpServers) == 0 {
		http.Error(w, "mcpServers is required and must not be empty", http.StatusBadRequest)
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	var conflicts []string
	needsRebuild := false
	for name, incomingDef := range incoming.McpServers {
		existingDef, exists := d.config.McpServers[name]
		if !exists {
			needsRebuild = true
			continue
		}
		if !mcpClientConfigEqual(existingDef, incomingDef) {
			conflicts = append(conflicts, name)
		}
	}

	if len(conflicts) > 0 {
		sort.Strings(conflicts)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(configConflictResponse{Conflicts: conflicts})
		return
	}

	if !needsRebuild {
		d.respondWithServerList(w)
		return
	}

	merged := make(map[string]*MCPClientConfigV2, len(d.config.McpServers)+len(incoming.McpServers))
	for k, v := range d.config.McpServers {
		merged[k] = v
	}
	for k, v := range incoming.McpServers {
		if _, exists := merged[k]; !exists {
			inheritClientDefaults(v, d.config.McpProxy.Options)
			merged[k] = v
		}
	}

	newConfig := &Config{
		McpProxy:   d.config.McpProxy,
		McpServers: merged,
	}
	result, wireErr := wireServers(d.ctx, newConfig, d.baseURL)
	if wireErr != nil {
		log.Printf("Failed to wire servers after merge: %v", wireErr)
		http.Error(w, "failed to wire merged config: "+wireErr.Error(), http.StatusInternalServerError)
		return
	}

	oldClosers := d.closers
	d.hSwitch.swap(result.handler)
	d.closers = result.closers
	d.config = newConfig

	for _, fn := range oldClosers {
		fn()
	}

	d.respondWithServerList(w)
}

func (d *daemon) respondWithServerList(w http.ResponseWriter) {
	servers := make([]string, 0, len(d.config.McpServers))
	for name := range d.config.McpServers {
		servers = append(servers, name)
	}
	sort.Strings(servers)

	addr := d.config.McpProxy.Addr
	if strings.HasPrefix(addr, ":") {
		addr = "localhost" + addr
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(configMergeResponse{
		Addr:    addr,
		Servers: servers,
	})
}

func mcpClientConfigEqual(a, b *MCPClientConfigV2) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return equalMCPClientConfigNonNil(a, b)
}

func equalMCPClientConfigNonNil(a, b *MCPClientConfigV2) bool {
	return a.TransportType == b.TransportType &&
		a.Command == b.Command &&
		a.URL == b.URL &&
		a.Timeout == b.Timeout &&
		reflect.DeepEqual(a.Args, b.Args) &&
		reflect.DeepEqual(a.Env, b.Env) &&
		reflect.DeepEqual(a.Headers, b.Headers) &&
		reflect.DeepEqual(a.Options, b.Options)
}
