# MCP Proxy Server

An MCP proxy that aggregates multiple MCP servers behind a single HTTP entrypoint.

## Features

- Proxy multiple MCP clients: aggregate tools, prompts, and resources from many servers.
- SSE and streamable HTTP: serve via Server‑Sent Events or streamable HTTP.
- Flexible config: supports `stdio`, `sse`, and `streamable-http` client types.

## Documentation

- Configuration: [docs/configuration.md](docs/CONFIGURATION.md)
- Usage: [docs/usage.md](docs/USAGE.md)
- Deployment: [docs/deployment.md](docs/DEPLOYMENT.md)
- Claude config converter: https://tbxark.github.io/mcp-proxy

## Quick Start

### Build from source

```bash
git clone https://github.com/alma-news-media/mcp-proxy.git
cd mcp-proxy
go build -o mcp-proxy .
./mcp-proxy --config path/to/config.json
```

### Install via Go

Releases are tagged automatically with [go-semantic-release](https://github.com/go-semantic-release/semantic-release) from [Conventional Commits](https://www.conventionalcommits.org/) on `master` / `main` (`feat:`, `fix:`, etc.), so `go install` can resolve versions from the module proxy:

```bash
go install github.com/alma-news-media/mcp-proxy@latest
```

Or install a development version from any pushed branch or tagged release:
```bash
go install github.com/alma-news-media/mcp-proxy@<branch|version tag>
```

### Run as a user daemon

`scripts/install-user-daemon.sh` installs mcp-proxy as a persistent background service that starts automatically on login. It supports **macOS** (launchd agent) and **Linux / WSL2** (systemd user unit).

```bash
# after go install or building from source
bash scripts/install-user-daemon.sh
```

What the script does:

1. **Detects the platform** — macOS via `uname -s`, WSL2/Linux via `/proc/version`.
2. **Finds the binary** — checks `$PATH`, then `~/.local/bin` and `/usr/local/bin`. Exits with install instructions if not found.
3. **Creates a minimal config** at `~/.config/mcp-proxy/config.json` (binds to `127.0.0.1:9090`, SSE transport, empty `mcpServers`). Skipped if the file already exists.
4. **Installs the service**:
   - macOS: writes `~/Library/LaunchAgents/media.almanews.mcp-proxy.plist`, then `launchctl load`.
   - Linux/WSL2: writes `~/.config/systemd/user/mcp-proxy.service`, then `systemctl --user enable --now`. Requires systemd user sessions; on WSL2 this needs `[boot] systemd=true` in `/etc/wsl.conf`.

**Idempotency** — re-running the script is safe:
- The **config file is never overwritten** once it exists, so any customisations you have made are preserved.
- The **service definition** (plist or unit file) is always rewritten and the service restarted. This makes re-running the script the correct way to pick up a new binary after `go install`.

After installation, add MCP servers to the running daemon from any workspace config:

```bash
mcp-proxy --add-config path/to/workspace-config.json
```

**Logs** — when run as a user service, logs go to:
- macOS: `~/Library/Logs/mcp-proxy.log`
- Linux/WSL2: `journalctl --user -u mcp-proxy -f`

See [docs/USAGE.md](docs/USAGE.md) for the full `--add-config`, `--daemon`, and logging reference.

### Commit messages

Automated releases expect [Conventional Commits](https://www.conventionalcommits.org/). The analyzer treats `feat` as minor, `fix` and several other types (including `chore`, `ci`, `docs`) as patch—see `.semrelrc`. If you merge PRs with **merge commits**, the subject line on `master` is often `Merge pull request #…`, which is not conventional; prefer **squash merge** (or rebase) so the merged commit message stays `feat: …` / `fix: …`.

A `commit-msg` hook lives in [`.githooks/commit-msg`](.githooks/commit-msg). On a fresh clone, enable it once:

```bash
git config core.hooksPath .githooks
chmod +x .githooks/commit-msg
```

If your environment already sets `core.hooksPath` globally to `.githooks`, you only need the `chmod` when the script is not executable.

## Configuration

See full configuration reference and examples in [docs/configuration.md](docs/CONFIGURATION.md).
An online Claude config converter is available at: https://tbxark.github.io/mcp-proxy


## Usage

Command‑line flags, endpoints, and auth examples are documented in [docs/usage.md](docs/USAGE.md).

## Thanks

- This project was inspired by the [adamwattis/mcp-proxy-server](https://github.com/adamwattis/mcp-proxy-server) project
- If you have any questions about deployment, you can refer to  [《在 Docker 沙箱中运行 MCP Server》](https://miantiao.me/posts/guide-to-running-mcp-server-in-a-sandbox/)([@ccbikai](https://github.com/ccbikai))

## License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.
