# Usage

## CLI

```text
-config string         path to config file or a http(s) url (default "config.json")
-expand-env            expand environment variables in config file (default true)
-http-headers string   optional headers for config URL: 'Key1:Value1;Key2:Value2'
-http-timeout int      timeout (seconds) for remote config fetch (default 10)
-insecure              skip TLS verification for remote config
-daemon                run in daemon mode (HTTP listener + Unix control socket + PID file); use with -config
-add-config path       merge mcpServers from JSON file into a running daemon, or start the daemon from that file if none is running
-version               print version and exit
-help                  print help and exit
```

## Run modes

### Standalone (default)

```bash
mcp-proxy --config path/to/config.json
```

Loads the full config, connects to each upstream MCP server, and serves the aggregated proxy on `mcpProxy.addr`. This process blocks until SIGINT/SIGTERM.

### Daemon

```bash
mcp-proxy --config path/to/config.json --daemon
```

Runs the same HTTP server as standalone, plus a **control plane** on a Unix domain socket so other processes can merge additional `mcpServers` without restarting the TCP listener. State is stored under the user home directory:

| Artifact | Path |
|----------|------|
| Control socket | `~/.local/run/darkside-mcp-proxy.sock` |
| PID file | `~/.local/run/darkside-mcp-proxy.pid` |

### Add config (`--add-config`)

```bash
mcp-proxy --add-config path/to/partial-or-full-config.json
```

- If a **daemon is already running** (PID file + live process, or socket responds), the tool loads the given file, takes its `mcpServers` map, and **POST**s it to the daemon. The HTTP listener is updated in place (routes swapped) when new server names are added.
- If **no daemon is running**, the tool starts one using the loaded config (equivalent to `--daemon` with that file).

The config file passed to `--add-config` must be valid JSON with a non-empty `mcpServers` object (only `mcpServers` is merged into the daemon; `mcpProxy` from the initial daemon start still applies).

**Merge rules:**

- **New server name** — definition is merged in and routes are rebuilt.
- **Same name, identical definition** (same transport, command, URL, args, env, headers, etc.) — no-op; daemon responds with the current server list.
- **Same name, different definition** — HTTP **409 Conflict** with body `{"conflicts":["server-name",...]}`.

On success, **`--add-config` prints a single JSON object to stdout** (used by wrappers such as `run-mcp-proxy`):

```json
{
  "addr": "localhost:9090",
  "servers": ["alpha", "beta"]
}
```

- `addr` — value from `mcpProxy.addr`, with a leading `:` expanded to `localhost:port` for clients.
- `servers` — sorted list of all MCP server keys currently registered on the daemon.

## Endpoints

Given `mcpProxy.baseURL = https://mcp.example.com` and a server key `fetch`:

- For `type: sse`: `https://mcp.example.com/fetch/sse`
- For `type: streamable-http`: `https://mcp.example.com/fetch/mcp`

## Auth

If `options.authTokens` is set for a server, requests must include a bearer token:

```
Authorization: <token>
```

If your client cannot set headers, embed the token in the route key (e.g. `fetch/<token>`) and call that path instead.

