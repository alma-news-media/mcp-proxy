package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
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
	daemonSocketName      = "darkside-mcp-proxy.sock"
	daemonPIDName         = "darkside-mcp-proxy.pid"
	daemonStartupLockName = "darkside-mcp-proxy.startup.lock"
	daemonRunDir          = ".local/run"
)

var errStartupLockBusy = errors.New("daemon startup lock held")

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

func daemonStartupLockPath() string {
	return filepath.Join(daemonRunPath(), daemonStartupLockName)
}

// startupLock serializes daemon startup so only one process probes and unlinks stale runtime files.
type startupLock struct {
	file *os.File
}

func acquireStartupLock() (*startupLock, error) {
	if err := os.MkdirAll(daemonRunPath(), 0o755); err != nil {
		return nil, fmt.Errorf("create run directory: %w", err)
	}
	f, err := os.OpenFile(daemonStartupLockPath(), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil, errStartupLockBusy
		}
		return nil, err
	}
	return &startupLock{file: f}, nil
}

func releaseStartupLock(lock *startupLock) {
	if lock == nil || lock.file == nil {
		return
	}
	path := lock.file.Name()
	_ = lock.file.Close()
	_ = os.Remove(path)
	lock.file = nil
}

// acquireDaemonRuntimeForStartup takes the startup lock and removes stale runtime files.
// On failure it releases the lock and cancels the daemon context.
func acquireDaemonRuntimeForStartup(d *daemon) (*startupLock, error) {
	lock, err := acquireStartupLock()
	if err != nil {
		if errors.Is(err, errStartupLockBusy) {
			return nil, fmt.Errorf("daemon already running: another mcp-proxy instance is starting or active")
		}
		return nil, fmt.Errorf("acquire startup lock: %w", err)
	}
	if err := prepareDaemonRuntimeBeforeBind(); err != nil {
		releaseStartupLock(lock)
		d.cancel()
		return nil, err
	}
	return lock, nil
}

// readDaemonPIDFromFile returns the PID from the daemon PID file. If the file is
// missing or does not contain a positive integer, it returns (0, nil). A non-nil
// error indicates an unexpected failure reading the file.
func readDaemonPIDFromFile() (int, error) {
	data, err := os.ReadFile(daemonPIDPath())
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, nil
	}
	return pid, nil
}

// daemonProcessAlive reports whether pid refers to a running process (non-destructive check).
func daemonProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// prepareDaemonRuntimeBeforeBind ensures no other daemon owns the PID/socket paths.
// It removes stale socket and PID files when the control plane is down and the stored PID
// does not refer to a live mcp-proxy process. The caller must hold the startup lock.
func prepareDaemonRuntimeBeforeBind() error {
	if daemonControlPlaneHealthy(context.Background()) {
		return fmt.Errorf("daemon already running: another mcp-proxy instance is active (control socket)")
	}
	oldPID, err := readDaemonPIDFromFile()
	if err != nil {
		return fmt.Errorf("read daemon PID file: %w", err)
	}
	if oldPID != 0 && isMcpProxyDaemonProcess(oldPID) {
		return fmt.Errorf("daemon already running: another mcp-proxy instance is active (PID %d)", oldPID)
	}
	if err := removePathIgnoringNotExist(daemonSocketPath()); err != nil {
		return fmt.Errorf("remove stale daemon socket: %w", err)
	}
	if err := removePathIgnoringNotExist(daemonPIDPath()); err != nil {
		return fmt.Errorf("remove stale daemon PID file: %w", err)
	}
	return nil
}

// removePathIgnoringNotExist unlinks path; missing files are not an error.
func removePathIgnoringNotExist(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
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

// newDaemonForConfig prepares optional initial wiring and the HTTP server; it does not listen or write the PID file yet.
func newDaemonForConfig(config *Config) (*daemon, error) {
	baseURL, err := url.Parse(config.McpProxy.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse baseURL: %w", err)
	}

	if err := os.MkdirAll(daemonRunPath(), 0o755); err != nil {
		return nil, fmt.Errorf("create run directory: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	d := &daemon{
		config:  config,
		baseURL: baseURL,
		ctx:     ctx,
		cancel:  cancel,
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

// listenDaemonSockets binds the Unix control socket and TCP proxy listener.
func (d *daemon) listenDaemonSockets(config *Config) (tcpLn net.Listener, socketPath string, err error) {
	socketPath = daemonSocketPath()
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		// Do not remove PID/socket paths: bind failed; cleanup could delete another daemon's files.
		d.cancel()
		return nil, "", fmt.Errorf("listen unix socket: %w", err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		ln.Close()
		d.cancel()
		return nil, "", fmt.Errorf("chmod socket: %w", err)
	}
	d.socketLn = ln

	socketMux := http.NewServeMux()
	socketMux.HandleFunc("POST /config", d.handleConfigMerge)
	d.socketServer = &http.Server{Handler: socketMux}

	tcpLn, err = listenTCPReuseAddr(config.McpProxy.Addr)
	if err != nil {
		_ = d.socketServer.Shutdown(context.Background())
		ln.Close()
		d.cleanup()
		d.cancel()
		return nil, "", fmt.Errorf("listen tcp: %w", err)
	}
	return tcpLn, socketPath, nil
}

func abortDaemonListenSetup(d *daemon) {
	if d.socketServer != nil {
		_ = d.socketServer.Shutdown(context.Background())
	}
	if d.socketLn != nil {
		_ = d.socketLn.Close()
	}
	d.cleanup()
	d.cancel()
}

func runDaemonUntilSignal(d *daemon, config *Config) error {
	startupLock, err := acquireDaemonRuntimeForStartup(d)
	if err != nil {
		return err
	}
	defer releaseStartupLock(startupLock)

	runtimeOwned := false
	defer func() {
		if runtimeOwned {
			d.cleanup()
		}
	}()

	tcpLn, socketPath, err := d.listenDaemonSockets(config)
	if err != nil {
		return err
	}

	if err := d.writePIDFile(); err != nil {
		abortDaemonListenSetup(d)
		return fmt.Errorf("write daemon PID file: %w", err)
	}
	releaseStartupLock(startupLock)
	runtimeOwned = true

	runErr := d.serveDaemonUntilSignal(config, tcpLn, socketPath)
	runtimeOwned = false
	return runErr
}

func (d *daemon) serveDaemonUntilSignal(config *Config, tcpLn net.Listener, socketPath string) error {
	errChan := make(chan error, 2)

	go func() {
		log.Printf("Unix socket listening on %s", socketPath)
		if sErr := d.socketServer.Serve(d.socketLn); sErr != nil && !errors.Is(sErr, http.ErrServerClosed) {
			errChan <- fmt.Errorf("unix socket server: %w", sErr)
		}
	}()

	go func() {
		log.Printf("Starting %s server", config.McpProxy.Type)
		log.Printf("%s server listening on %s", config.McpProxy.Type, tcpLn.Addr().String())
		if hErr := d.httpServer.Serve(tcpLn); hErr != nil && !errors.Is(hErr, http.ErrServerClosed) {
			errChan <- fmt.Errorf("http server: %w", hErr)
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	var runErr error
	select {
	case <-sigChan:
		log.Println("Shutdown signal received")
	case err := <-errChan:
		log.Printf("Server error: %v", err)
		runErr = err
	}

	d.shutdown()
	return runErr
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

type configMergeIncoming struct {
	McpServers map[string]*MCPClientConfigV2 `json:"mcpServers"`
}

// parseConfigMergeIncoming unmarshals and validates a POST /config body. On failure it writes
// the HTTP response and returns (nil, false).
func parseConfigMergeIncoming(body []byte, w http.ResponseWriter) (*configMergeIncoming, bool) {
	var incoming configMergeIncoming
	if err := json.Unmarshal(body, &incoming); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return nil, false
	}
	if len(incoming.McpServers) == 0 {
		http.Error(w, "mcpServers is required and must not be empty", http.StatusBadRequest)
		return nil, false
	}
	for k, v := range incoming.McpServers {
		if v == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("mcpServers entry %q is null", k),
			})
			return nil, false
		}
	}
	return &incoming, true
}

// detectMergeConflicts scans incoming servers against existing ones.
// It returns a sorted list of conflicting names and whether new servers
// require a full re-wire of the daemon.
func detectMergeConflicts(
	existing, incoming map[string]*MCPClientConfigV2,
) (conflicts []string, needsRebuild bool) {
	for name, incomingDef := range incoming {
		existingDef, exists := existing[name]
		if !exists {
			needsRebuild = true
			continue
		}
		if !mcpClientConfigEqual(existingDef, incomingDef) {
			conflicts = append(conflicts, name)
		}
	}
	return
}

// buildMergedServerMap returns a new map with all existing servers plus any
// incoming servers that are not already present (new servers inherit proxy defaults).
func buildMergedServerMap(
	existing, incoming map[string]*MCPClientConfigV2,
	proxyOpts *OptionsV2,
) map[string]*MCPClientConfigV2 {
	merged := make(map[string]*MCPClientConfigV2, len(existing)+len(incoming))
	maps.Copy(merged, existing)
	for k, v := range incoming {
		if _, exists := merged[k]; !exists {
			inheritClientDefaults(v, proxyOpts)
			merged[k] = v
		}
	}
	return merged
}

// filterToRegisteredServers returns a new Config that only includes servers
// present in the registered set. Servers that failed to wire silently
// (PanicIfInvalid=false) are excluded and logged so callers never write a
// proxy URL that would return 404.
func filterToRegisteredServers(candidate *Config, registered map[string]bool) *Config {
	routed := make(map[string]*MCPClientConfigV2, len(registered))
	for name, cfg := range candidate.McpServers {
		if registered[name] {
			routed[name] = cfg
		} else {
			log.Printf("<%s> Server was not registered (connection failed); excluded from daemon config", name)
		}
	}
	return &Config{McpProxy: candidate.McpProxy, McpServers: routed}
}

// applyWireResult atomically installs result into the daemon, closes old
// connections, and stores newConfig as the current configuration.
func (d *daemon) applyWireResult(result *wireResult, newConfig *Config) {
	oldClosers := d.closers
	d.hSwitch.swap(result.handler)
	d.closers = result.closers
	d.config = newConfig
	for _, fn := range oldClosers {
		fn()
	}
}

func (d *daemon) handleConfigMerge(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	incoming, ok := parseConfigMergeIncoming(body, w)
	if !ok {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	conflicts, needsRebuild := detectMergeConflicts(d.config.McpServers, incoming.McpServers)
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

	merged := buildMergedServerMap(d.config.McpServers, incoming.McpServers, d.config.McpProxy.Options)
	newConfig := &Config{McpProxy: d.config.McpProxy, McpServers: merged}
	result, wireErr := wireServers(d.ctx, newConfig, d.baseURL)
	if wireErr != nil {
		log.Printf("Failed to wire servers after merge: %v", wireErr)
		http.Error(w, "failed to wire merged config: "+wireErr.Error(), http.StatusInternalServerError)
		return
	}

	d.applyWireResult(result, filterToRegisteredServers(newConfig, result.registered))
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
