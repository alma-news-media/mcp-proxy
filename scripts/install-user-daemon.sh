#!/usr/bin/env bash
# Installs mcp-proxy as a user-level daemon service.
# Supports: macOS (launchd) and Linux/WSL (systemd --user).
set -euo pipefail

LABEL="media.almanews.mcp-proxy"
SERVICE="mcp-proxy"
CONFIG_DIR="${HOME}/.config/mcp-proxy"
CONFIG_FILE="${CONFIG_DIR}/config.json"

info() { echo "  $*"; }
ok()   { echo "✓ $*"; }
die()  { echo "✗ $*" >&2; exit 1; }

detect_platform() {
    case "$(uname -s)" in
        Darwin) echo "macos" ;;
        Linux)
            if grep -qi microsoft /proc/version 2>/dev/null; then
                echo "wsl"
            else
                echo "linux"
            fi
            ;;
        *) echo "unsupported" ;;
    esac
}

find_binary() {
    if command -v mcp-proxy &>/dev/null; then
        command -v mcp-proxy
        return
    fi
    local candidates=("${HOME}/.local/bin/mcp-proxy" "/usr/local/bin/mcp-proxy")
    for f in "${candidates[@]}"; do
        [[ -x "$f" ]] && { echo "$f"; return; }
    done
    return 1
}

write_config() {
    if [[ -f "$CONFIG_FILE" ]]; then
        info "Config already exists at $CONFIG_FILE — skipping."
        return
    fi
    mkdir -p "$CONFIG_DIR"
    chmod 700 "$CONFIG_DIR"
    (
      umask 077
      cat > "$CONFIG_FILE" <<'EOF'
{
  "mcpProxy": {
    "baseURL": "http://127.0.0.1:9090",
    "addr": "127.0.0.1:9090",
    "name": "MCP Proxy",
    "type": "sse",
    "options": {
      "panicIfInvalid": false,
      "logEnabled": false
    }
  },
  "mcpServers": {}
}
EOF
    )
    ok "Created minimal config at $CONFIG_FILE"
}

install_macos() {
    local binary="$1"
    local agents_dir="${HOME}/Library/LaunchAgents"
    local plist="${agents_dir}/${LABEL}.plist"
    local log_dir="${HOME}/Library/Logs"

    mkdir -p "$agents_dir" "$log_dir"

    if launchctl list "$LABEL" &>/dev/null; then
        info "Stopping existing service..."
        launchctl unload "$plist" 2>/dev/null || true
    fi

    cat > "$plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>${LABEL}</string>
    <key>ProgramArguments</key>
    <array>
        <string>${binary}</string>
        <string>--config</string>
        <string>${CONFIG_FILE}</string>
        <string>--daemon</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>${log_dir}/mcp-proxy.log</string>
    <key>StandardErrorPath</key>
    <string>${log_dir}/mcp-proxy.log</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>${PATH}</string>
    </dict>
</dict>
</plist>
EOF
    ok "Installed launchd plist at $plist"
    launchctl load "$plist"
    ok "Service started"
    echo
    info "Logs:  tail -f $log_dir/mcp-proxy.log"
    info "Stop:  launchctl unload $plist"
    info "Start: launchctl load $plist"
    info "PATH:  captured at install time — re-run installer after adding new tools to PATH"
}

install_linux() {
    local binary="$1"
    local unit_dir="${HOME}/.config/systemd/user"
    local unit="${unit_dir}/${SERVICE}.service"

    if ! systemctl --user list-units &>/dev/null; then
        die "systemd --user is not available.
On WSL2, enable systemd by adding the following to /etc/wsl.conf:
  [boot]
  systemd=true
Then restart WSL: wsl --shutdown"
    fi

    local env_file="${CONFIG_DIR}/env"
    mkdir -p "$CONFIG_DIR" "$unit_dir"
    printf 'PATH=%s\n' "$PATH" > "$env_file"
    ok "Wrote service environment to $env_file"

    cat > "$unit" <<EOF
[Unit]
Description=MCP Proxy daemon
After=network.target

[Service]
Type=simple
ExecStart=${binary} --config ${CONFIG_FILE} --daemon
Restart=on-failure
RestartSec=5s
EnvironmentFile=-${CONFIG_DIR}/env

[Install]
WantedBy=default.target
EOF
    ok "Installed unit file at $unit"
    systemctl --user daemon-reload

    if systemctl --user is-active --quiet "$SERVICE"; then
        systemctl --user restart "$SERVICE"
        ok "Service restarted"
    else
        systemctl --user enable --now "$SERVICE"
        ok "Service enabled and started"
    fi
    echo
    info "Status: systemctl --user status $SERVICE"
    info "Logs:   journalctl --user -u $SERVICE -f"
    info "Stop:   systemctl --user stop $SERVICE"
    info "PATH:   captured at install time — re-run installer after adding new tools to PATH"
}

main() {
    echo "=== mcp-proxy user daemon installer ==="
    echo

    local platform
    platform="$(detect_platform)"
    info "Platform: $platform"

    [[ "$platform" == "unsupported" ]] && \
        die "Unsupported platform. This script supports macOS and Linux (including WSL2)."

    local binary
    if ! binary="$(find_binary)"; then
        die "mcp-proxy not found in PATH or common locations.
Install it with:
  go install github.com/alma-news-media/mcp-proxy@latest
Then add \$(go env GOPATH)/bin or ~/.local/bin to your PATH."
    fi
    ok "Found mcp-proxy: $binary"

    write_config

    case "$platform" in
        macos)     install_macos "$binary" ;;
        wsl|linux) install_linux "$binary" ;;
    esac

    echo
    echo "=== Done ==="
    echo
    info "Add MCP servers: mcp-proxy --add-config <workspace-config.json>"
}

main "$@"
