# data-toolbox-mcp

> DuckDB analysis and containerized Python execution, exposed as a single-binary MCP server. Bring your own LLM client.

`data-toolbox-mcp` lets any MCP client (Claude Desktop, Cursor, ...) load tabular data into a per-workspace DuckDB and run SQL or Python against it inside a Podman sandbox. Six tools are exposed:

- `load_data(workspace_id, file_path, table_name)`
- `query_data(workspace_id, sql)`
- `execute_code(workspace_id, language, code)`
- `list_workspaces()` — discover prior workspaces across sessions
- `delete_workspace(workspace_id)` — tear a workspace down completely
- `describe_runtime()` — what the container ships (python, packages, fonts, network)

The server is LLM-agnostic: it speaks plain MCP over stdio and never talks to any LLM provider itself.

[日本語版 README](README.ja.md)

## Why this exists

[shell-agent-v2](https://github.com/nlink-jp/util-series/tree/main/shell-agent-v2) bundles a Wails GUI, an LLM client, and a DuckDB + Podman tool layer in one process. When you want the same data tools from a *different* LLM client, you'd have to reach inside that bundle. `data-toolbox-mcp` extracts the tool layer alone and ships it as a reusable MCP server, so any compliant client can use it.

## Features

- **Three MCP tools** for the load → query → analyze loop.
- **workspace_id scoping**: each workspace owns one container and one DuckDB file; state persists across server restarts. ([ADR-0001](docs/en/adr/0001-workspace-id-lifecycle.md))
- **Podman sandbox** with `network=none` by default; CPU / memory / timeout caps configurable. ([ADR-0002](docs/en/adr/0002-podman-engine-choice.md))
- **Python runtime** (`duckdb`, `pandas`, `polars`, `pyarrow` bundled). ([ADR-0003](docs/en/adr/0003-python-only-runtime.md))
- **stdio transport only** — no network exposure, no auth needed. ([ADR-0004](docs/en/adr/0004-stdio-only-transport.md))
- **No registry push** — the runtime Dockerfile is `go:embed`-ed and built locally on first use. ([ADR-0005](docs/en/adr/0005-local-build-image-distribution.md))
- **Single binary, single version**: `serve` / `build-runtime` / `doctor` / `version` subcommands all ship in one binary.
- **Structured tool errors**: every tool error has a stable `code` LLM clients can branch on (`path_not_allowed`, `unsupported_language`, `script_failed`, ...).
- **Defense-in-depth path checks**: `allowed_paths` is enforced after `EvalSymlinks` on both sides, blocking symlink jail-breaks.

## Requirements

- macOS or Linux
- [Podman](https://podman.io/) (rootless). On macOS run `podman machine start` once before using.
- Go 1.23+ to build from source

## Quick start

```sh
# 1. Build the binary (signed with Developer ID on macOS if the cert is in your keychain)
make build

# 2. Build the runtime container image (first time only, ~2 min)
dist/data-toolbox-mcp build-runtime

# 3. Verify the environment
dist/data-toolbox-mcp doctor

# 4. Wire it into your MCP client (Claude Desktop config example)
cat >> ~/Library/Application\ Support/Claude/claude_desktop_config.json <<'JSON'
{
  "mcpServers": {
    "data-toolbox": {
      "command": "/absolute/path/to/dist/data-toolbox-mcp",
      "args": ["serve", "--config", "/Users/you/.config/data-toolbox-mcp/config.toml"]
    }
  }
}
JSON
```

A minimal `config.toml`:

```toml
[workspace]
workspace_dir = "~/.data-toolbox"
allowed_paths = ["~/data", "~/Downloads"]

[container]
image        = "localhost/data-toolbox-runtime:latest"
stop_on_exit = true

[container.limits]
cpu             = "1.0"
memory          = "2GB"
timeout_seconds = 60
network         = "none"

[query]
default_row_limit = 20000
```

See [`config.example.toml`](config.example.toml) for the full schema. Full client setup — Claude Desktop, Cursor, troubleshooting — is in [`docs/en/reference/client-setup.md`](docs/en/reference/client-setup.md).

## Subcommands

| Command | Purpose |
|---------|---------|
| `serve` (default) | Start the MCP stdio server |
| `build-runtime` | Unpack the embedded Dockerfile and `podman build` the runtime image |
| `doctor` | Diagnose Podman, podman machine (macOS), runtime image, and config |
| `version` | Show the binary version |

## Tools

| Tool | Arguments | Returns |
|------|-----------|---------|
| `load_data` | `workspace_id`, `file_path`, `table_name` | `{rows_loaded, schema}` |
| `query_data` | `workspace_id`, `sql` | `{rows, row_count, limit_applied, limit_reached}` |
| `execute_code` | `workspace_id`, `language: "python"`, `code` | `{stdout, stderr, exit_code}` |
| `list_workspaces` | — | `{workspaces: [{id, last_used, container_state}]}` |
| `delete_workspace` | `workspace_id` | `{deleted, workspace_id}` |
| `describe_runtime` | — | `{python_version, container_image, packages, fonts, network, mount_points, notes}` |

`load_data` infers the reader from the file extension (`.csv` → `read_csv_auto`, `.json` / `.jsonl` → `read_json_auto`, `.parquet` → `read_parquet`). `query_data` auto-appends `LIMIT [query] default_row_limit` (default 20000) when the SQL has no `LIMIT`. `execute_code` only accepts `language="python"` in this version (ADR-0003); the runtime container ships with `duckdb`, `pandas`, `polars`, `pyarrow`, `matplotlib`, and `Pillow`, plus `fonts-noto-cjk` so Japanese matplotlib labels render without setup (ADR-0007). Call `describe_runtime` once at session start to inspect what's actually available.

## Security model (essentials)

- `allowed_paths` is enforced for every file `load_data` is asked to read. The path is made absolute and `EvalSymlinks`-resolved before being compared with the `EvalSymlinks`-resolved allowed entries.
- The container runs with `network=none` by default. To enable network access (and thus in-container `pip install`), set `[container.limits] network = "bridge"` — there is intentionally no finer-grained ACL.
- The container runs as a non-root user (UID 1000 from the runtime Dockerfile). On rootless Podman the host user is mapped to that UID via `--userns keep-id:uid=1000,gid=1000`.
- Per-tool timeouts are enforced via `context.WithTimeout`; on expiry the `podman exec` child is killed and the MCP request still returns (no hung calls).
- Tool errors are returned as structured JSON inside the MCP content block; LLM clients can branch on the `code` slug.

Full model: [`docs/en/reference/architecture.md`](docs/en/reference/architecture.md) §6.

## Sample data

`samples/` ships with three small datasets — `sales.csv` (40 rows), `products.json` (10 rows), `logs.jsonl` (41 rows) — and `samples/README.md` walks through a graded end-to-end verification (load → SQL → JOIN → window functions → quantiles → pandas → polars → workspace isolation → security boundaries).

## Documentation

- [`docs/en/data-toolbox-mcp-rfp.md`](docs/en/data-toolbox-mcp-rfp.md) — the original RFP
- [`docs/en/reference/architecture.md`](docs/en/reference/architecture.md) — overall architecture
- [`docs/en/reference/phase1-plan.md`](docs/en/reference/phase1-plan.md) — Phase 1 (v0.1.0) development plan
- [`docs/en/reference/v0.2.0-plan.md`](docs/en/reference/v0.2.0-plan.md) — v0.2.0 development plan
- [`docs/en/reference/client-setup.md`](docs/en/reference/client-setup.md) — Claude Desktop / Cursor setup
- [`docs/en/adr/`](docs/en/adr/) — seven ADRs covering workspace_id, Podman, Python-only, stdio, local-build distribution, workspace mgmt + describe_runtime, and container package scope

## Acknowledgements

The tool surface and the per-workspace DuckDB + container pattern are derived from [shell-agent-v2](https://github.com/nlink-jp/util-series/tree/main/shell-agent-v2). `data-toolbox-mcp` extracts and reshapes those ideas as a standalone MCP server.

## License

[MIT](LICENSE).
