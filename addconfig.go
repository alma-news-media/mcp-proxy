package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func runAddConfig(configPath string, expandEnv bool) {
	cfg, err := loadConfigFile(configPath, expandEnv)
	if err != nil {
		log.Fatalf("Failed to load config from %s: %v", configPath, err)
	}

	if isDaemonRunning() {
		postConfigToDaemon(cfg)
		return
	}

	log.Println("No running daemon detected, starting new daemon")
	if err := startDaemon(cfg); err != nil {
		log.Fatalf("Failed to start daemon: %v", err)
	}
}

func isDaemonRunning() bool {
	if pidAlive() {
		return true
	}
	return socketResponds()
}

func pidAlive() bool {
	data, err := os.ReadFile(daemonPIDPath())
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func socketResponds() bool {
	conn, err := net.DialTimeout("unix", daemonSocketPath(), 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func postConfigToDaemon(cfg *Config) {
	body, err := json.Marshal(cfg)
	if err != nil {
		log.Fatalf("Failed to marshal config: %v", err)
	}

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.DialTimeout("unix", daemonSocketPath(), 5*time.Second)
			},
		},
		Timeout: 30 * time.Second,
	}

	resp, err := client.Post("http://daemon/config", "application/json", strings.NewReader(string(body)))
	if err != nil {
		log.Fatalf("Failed to connect to daemon: %v", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Failed to read daemon response: %v", err)
	}

	if resp.StatusCode == http.StatusConflict {
		var conflict configConflictResponse
		if jErr := json.Unmarshal(respBody, &conflict); jErr == nil {
			log.Fatalf("Config merge conflict: servers %v have different definitions in running daemon", conflict.Conflicts)
		}
		log.Fatalf("Config merge conflict: %s", string(respBody))
	}

	if resp.StatusCode != http.StatusOK {
		log.Fatalf("Daemon returned status %d: %s", resp.StatusCode, string(respBody))
	}

	fmt.Fprintln(os.Stdout, string(respBody))
}
