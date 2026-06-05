# MCP Client Setup

> Added in Phase 2. Instructions for wiring `data-toolbox-mcp` into the major
> MCP clients (Claude Desktop, Cursor, ...).

## Prerequisites

Before configuring a client, make sure:

1. `make build` produced the binary at `dist/data-toolbox-mcp`
2. `dist/data-toolbox-mcp build-runtime` produced `localhost/data-toolbox-runtime:latest`
3. `dist/data-toolbox-mcp doctor` reports `[OK]` on every check
4. (macOS) `podman machine start` is up

If `doctor` reports `[FAIL]`, follow the printed hint before proceeding.

## How configuration works (shared)

Every MCP client needs three pieces of information:

- the **absolute path** to the binary (don't rely on `PATH` resolution)
- the subcommand `serve` (optional; bare invocation is equivalent)
- optionally `--config /path/to/config.toml`

For a config template, see `config.example.toml`. Standard locations:

- `~/.config/data-toolbox-mcp/config.toml` (recommended)
- `./config.toml` in the current directory
- Or pass `--config /explicit/path.toml`

The server runs without a config file, but `allowed_paths` defaults to empty so `load_data` rejects everything. To use the data-analysis flow, set up a config first.

## Claude Desktop

### Config file location

macOS:

```text
~/Library/Application Support/Claude/claude_desktop_config.json
```

### Example

```json
{
  "mcpServers": {
    "data-toolbox": {
      "command": "/Users/you/path/to/data-toolbox-mcp/dist/data-toolbox-mcp",
      "args": ["serve"]
    }
  }
}
```

With an explicit config:

```json
{
  "mcpServers": {
    "data-toolbox": {
      "command": "/Users/you/path/to/data-toolbox-mcp/dist/data-toolbox-mcp",
      "args": ["serve", "--config", "/Users/you/.config/data-toolbox-mcp/config.toml"]
    }
  }
}
```

Restart Claude Desktop after editing the config so it picks up the new server.

### Verification

Ask Claude "What tools are exposed by `data-toolbox`?" — it should mention `load_data`, `query_data`, and `execute_code`.

## Cursor

### Config file location

`~/.cursor/mcp.json` (or per-project `<project>/.cursor/mcp.json`).

### Example

```json
{
  "mcpServers": {
    "data-toolbox": {
      "command": "/Users/you/path/to/data-toolbox-mcp/dist/data-toolbox-mcp",
      "args": ["serve"]
    }
  }
}
```

Restart Cursor and the MCP server appears.

## Troubleshooting

### `doctor` says podman is missing

PATH issue or Podman not installed. Run `which podman`. On macOS install with `brew install podman`, then `podman machine init && podman machine start`.

### macOS: `podman machine: no running machine`

```sh
podman machine start
```

Confirm `podman-machine-default* CURRENTLY_UP` and re-run `doctor`.

### `runtime image: NOT present`

```sh
dist/data-toolbox-mcp build-runtime
```

The first build takes 1–2 minutes and needs network for `pip install`. Re-run `doctor` afterward.

### Claude Desktop doesn't see the MCP server

- Verify `command` is an **absolute path** (no relative paths, no `~`)
- Validate the JSON (no trailing commas, balanced braces)
- Check `~/Library/Logs/Claude/mcp-server-data-toolbox.log` for the server's stderr at startup

### Every `load_data` returns `path_not_allowed`

`[workspace] allowed_paths` is empty in your `config.toml`, or `--config` isn't being passed. Run `dist/data-toolbox-mcp doctor` to see which config path is actually used.

### Want `pip install` inside `execute_code`

Flip `[container.limits] network` from `none` to `bridge` in `config.toml`. The container can then reach the outside world (Phase 0 Q5-4 resolution). Note: this lets all LLM-generated code reach the network, not just `pip`.

### Container leaks

```sh
podman ps -a --filter label=app=data-toolbox-mcp
```

shows orphans; `podman rm -f <name>` cleans them up. With `[container] stop_on_exit = true` (default), graceful shutdown cleans them automatically; SIGKILL leaks.

## See also

- [config.example.toml](../../../config.example.toml) — config template
- [architecture.md](architecture.md) — overall architecture
- [ADR-0001](../adr/0001-workspace-id-lifecycle.md) — workspace_id model
- [ADR-0005](../adr/0005-local-build-image-distribution.md) — local-build distribution
