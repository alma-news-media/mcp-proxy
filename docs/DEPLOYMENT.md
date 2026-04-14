# Deployment

## Install (recommended)

Tagged releases are published from this repository:

```bash
go install github.com/alma-news-media/mcp-proxy@latest
mcp-proxy --config path/to/config.json
```

## Build from a clone

```bash
git clone https://github.com/alma-news-media/mcp-proxy.git
cd mcp-proxy
go build -o mcp-proxy .
./mcp-proxy --config path/to/config.json
```

## Security Notes

- Prefer `authTokens` per downstream server; only use the `mcpProxy` default when appropriate.
- If a downstream server cannot set headers, you can embed a token in the route key (e.g. `fetch/<token>`) and route via that path.
- Set `options.panicIfInvalid: true` for critical servers to fail fast on misconfiguration.
