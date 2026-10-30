package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func startTestControlSocket(t *testing.T) *daemon {
	t.Helper()
	if err := os.MkdirAll(daemonRunPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	d := newTestDaemon(map[string]*MCPClientConfigV2{})
	d.config.McpProxy.Addr = "127.0.0.1:0"
	tcp, _, err := d.listenDaemonSockets(d.config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = d.socketServer.Close()
		_ = d.socketLn.Close()
		_ = tcp.Close()
		d.cancel()
	})
	return d
}

func assertOwnerControlRequest(t *testing.T) {
	t.Helper()
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{Dial: func(_, _ string) (net.Conn, error) {
			return net.Dial("unix", daemonSocketPath())
		}},
	}
	resp, err := client.Post("http://daemon/config", "application/json", strings.NewReader(`{"mcpServers":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("owner request status = %d, want 400", resp.StatusCode)
	}
}

func TestDaemonControlAllowsOwner(t *testing.T) {
	testDaemonHomeShort(t)
	d := startTestControlSocket(t)
	go d.socketServer.Serve(d.socketLn)
	assertOwnerControlRequest(t)
}

// The privileged run drops a helper to another UID and queues its request
// while the socket is writable, before the HTTP server starts accepting.
func TestDaemonControlRejectsQueuedForeignPeer(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to run a helper as a different UID")
	}
	home, err := os.MkdirTemp("", "mcp-peer-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	if err := os.Chmod(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	d := startTestControlSocket(t)
	for _, dir := range []string{filepath.Join(home, ".local"), daemonRunPath()} {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(daemonSocketPath(), 0o777); err != nil {
		t.Fatal(err)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(home, "helper")
	if err := os.WriteFile(helper, binary, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(helper, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "executed")
	cmd := exec.Command(helper, "-test.run=^TestDaemonForeignPeerHelper$")
	cmd.Env = append(os.Environ(), "MCP_TEST_FOREIGN_SOCKET="+daemonSocketPath(), "MCP_TEST_MARKER="+marker)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534}}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	ready := make([]byte, 1)
	if _, err := io.ReadFull(stdout, ready); err != nil || ready[0] != 'R' {
		t.Fatalf("foreign client did not queue request: ready=%q err=%v", ready, err)
	}
	if err := os.Chmod(daemonSocketPath(), 0o600); err != nil {
		t.Fatal(err)
	}
	go d.socketServer.Serve(d.socketLn)
	output, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Errorf("foreign client: %v; output: %s", err, output)
	}
	assertOwnerControlRequest(t)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("foreign command was executed or marker could not be checked: %v", err)
	}
}

func TestDaemonForeignPeerHelper(t *testing.T) {
	path := os.Getenv("MCP_TEST_FOREIGN_SOCKET")
	if path == "" {
		t.Skip("subprocess helper")
	}
	conn, err := net.DialTimeout("unix", path, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	body, err := json.Marshal(map[string]any{"mcpServers": map[string]any{
		"foreign": map[string]any{"command": "/bin/sh", "args": []string{"-c", "touch " + os.Getenv("MCP_TEST_MARKER")}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(conn, "POST /config HTTP/1.1\r\nHost: daemon\r\nContent-Length: %d\r\n\r\n%s", len(body), body); err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(os.Stdout, "R")
	if _, err := bufio.NewReader(conn).ReadByte(); err == nil {
		t.Fatal("foreign client received an HTTP response")
	} else if e, ok := err.(net.Error); ok && e.Timeout() {
		t.Fatalf("foreign connection was not rejected: %v", err)
	}
}
