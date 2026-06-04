//go:build darwin || linux

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// processExecutableBaseName returns the base name of the executable for pid.
func processExecutableBaseName(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	if exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid)); err == nil {
		return filepath.Base(exe), true
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "comm=")
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	name := strings.TrimSpace(string(out))
	if name == "" {
		return "", false
	}
	return filepath.Base(name), true
}

// isMcpProxyDaemonProcess reports whether pid is a live process running this mcp-proxy binary.
func isMcpProxyDaemonProcess(pid int) bool {
	if !daemonProcessAlive(pid) {
		return false
	}
	base, ok := processExecutableBaseName(pid)
	if !ok {
		return false
	}
	return strings.Contains(strings.ToLower(base), "mcp-proxy")
}
