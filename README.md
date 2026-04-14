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

### Commit messages

Automated releases expect [Conventional Commits](https://www.conventionalcommits.org/). A `commit-msg` hook lives in [`.githooks/commit-msg`](.githooks/commit-msg). With `core.hooksPath` set to `.githooks` (this team’s usual setup), Git runs it on every commit with no separate install step.

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
