# data-toolbox-mcp

> DuckDB analysis and containerized Python execution, exposed as a single-binary MCP server. Bring your own LLM client.

`data-toolbox-mcp` lets any MCP client (Claude Desktop, Cursor, ...) load tabular data into a per-workspace DuckDB and run SQL or Python against it inside a Podman sandbox. Nine tools are exposed:

- `load_data(work_dir, workspace_id, file_path, table_name)`
- `query_data(work_dir, workspace_id, sql)` — auto-LIMIT returns `truncated` + `total` (v0.4.0)
- `execute_code(work_dir, workspace_id, language, code)`
- `list_workspaces(work_dir)` — discover prior workspaces across sessions
- `delete_workspace(work_dir, workspace_id, dry_run?)` — irreversible by default; `dry_run: true` shows what would be removed (v0.4.0)
- `describe_runtime()` — what the container ships (python, packages, fonts, network)
- `attach_files(work_dir, workspace_id, paths)` — return `/work` files as inline MCP image / text content
- `load_from_work(work_dir, workspace_id, file_path, table_name)` — table-ize a file already in `/work`
- `describe_workspace(work_dir, workspace_id)` — every table's column schema in the workspace (v0.4.0)

The server is LLM-agnostic: it speaks plain MCP over stdio and never talks to any LLM provider itself.

[日本語版 README](README.ja.md)

## Why this exists

[shell-agent-v2](https://github.com/nlink-jp/util-series/tree/main/shell-agent-v2) bundles a Wails GUI, an LLM client, and a DuckDB + Podman tool layer in one process. When you want the same data tools from a *different* LLM client, you'd have to reach inside that bundle. `data-toolbox-mcp` extracts the tool layer alone and ships it as a reusable MCP server, so any compliant client can use it.

## Features

- **Nine MCP tools** for the load → query → analyze loop. Every tool except `describe_runtime` takes a required `work_dir`: the workspace is `<work_dir>/<workspace_id>/`.
- **workspace_id scoping**: each workspace owns one container and one DuckDB file; state persists across server restarts. ([ADR-0001](docs/en/adr/0001-workspace-id-lifecycle.md))
- **Podman sandbox** with `network=none` by default; CPU / memory / timeout caps configurable. ([ADR-0002](docs/en/adr/0002-podman-engine-choice.md))
- **Python runtime** (`duckdb`, `pandas`, `polars`, `pyarrow` bundled). ([ADR-0003](docs/en/adr/0003-python-only-runtime.md))
- **stdio transport only** — no network exposure, no auth needed. ([ADR-0004](docs/en/adr/0004-stdio-only-transport.md))
- **No registry push** — the runtime Dockerfile is `go:embed`-ed and built locally on first use. ([ADR-0005](docs/en/adr/0005-local-build-image-distribution.md))
- **Single binary, single version**: `serve` / `build-runtime` / `doctor` / `version` subcommands all ship in one binary.
- **Structured tool errors**: every tool error has a stable `code` LLM clients can branch on (`path_not_allowed`, `unsupported_language`, `script_failed`, ...).
- **Defense-in-depth path checks**: the credential blacklist is applied to the path as given and to the place it leads (every link followed, a dangling one by its target), before anything asks whether a file is there — so a symlink cannot jail-break into `~/.ssh`, a symlinked `~/.ssh` cannot slip past, and a path refused this way gets the same answer whether or not a file is there.

## Requirements

- macOS or Linux
- [Podman](https://podman.io/) (rootless). On macOS run `podman machine start` once before using.
- Go 1.25+ to build from source

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
| `version` | Show the binary version (`--version` prints the same string) |

## Tools

| Tool | Arguments | Returns |
|------|-----------|---------|
| `load_data` | `work_dir`, `workspace_id`, `file_path` (host), `table_name` | `{rows_loaded, schema}` |
| `query_data` | `work_dir`, `workspace_id`, `sql` | `{rows, row_count, limit_applied, limit_reached, truncated, total, total_unavailable_reason?}` |
| `execute_code` | `work_dir`, `workspace_id`, `language: "python"`, `code` | `{stdout, stderr, exit_code, host_work_dir}` |
| `list_workspaces` | `work_dir` | `{workspaces: [{id, last_used, container_state, host_work_dir}]}` — only real workspaces under your `work_dir`; other directories are skipped |
| `delete_workspace` | `work_dir`, `workspace_id`, `dry_run?` | `dry_run=false`: `{deleted, workspace_id}`; `dry_run=true`: `{would_delete, container_id, container_state, host_paths, disk_usage_bytes}` |
| `describe_runtime` | — | `{python_version, container_image, packages, fonts, network, mount_points, notes}` |
| `attach_files` | `work_dir`, `workspace_id`, `paths: [string]` (1–16, `/work/...` or relative) | MCP content array: summary text + image / text / metadata blocks per file |
| `load_from_work` | `work_dir`, `workspace_id`, `file_path` (`/work/...`), `table_name` | `{rows_loaded, schema}` |
| `describe_workspace` | `work_dir`, `workspace_id` | `{workspace_id, host_work_dir, container_state, tables: [{name, columns: [{name, type}]}]}` |

`load_data` infers the reader from the file extension (`.csv` → `read_csv_auto`, `.json` / `.jsonl` → `read_json_auto`, `.parquet` → `read_parquet`). `query_data` auto-appends `LIMIT [query] default_row_limit` (default 20000) when the SQL has no `LIMIT`. `execute_code` only accepts `language="python"` in this version (ADR-0003); the runtime container ships with `duckdb`, `pandas`, `polars`, `pyarrow`, `matplotlib`, and `Pillow`, plus `fonts-noto-cjk` so Japanese matplotlib labels render without setup (ADR-0007). Call `describe_runtime` once at session start to inspect what's actually available.

`attach_files` (v0.3.0 / ADR-0008) returns files as MCP image content (PNG / JPG / SVG / GIF / WEBP / BMP) or text content (CSV / JSON / MD / etc.) so MCP clients render them inline; files above `[attach] max_single_size_bytes` (default 10 MiB) or beyond `max_total_size_bytes` (default 20 MiB) downgrade to metadata-only. `load_from_work` (v0.3.0 / ADR-0009) table-izes files that already live in `/work` — typically files written by `execute_code` — without leaving the sandbox.

`describe_workspace` (v0.4.0 / ADR-0010) returns every user table's column schema in one call — pair with `list_workspaces` for cross-session "what's in here?". `query_data` (v0.4.0) result now includes `truncated` and `total`, and a missing-table error carries an actionable hint (available tables + other workspaces). `delete_workspace` accepts `dry_run: true` to show what would be removed without acting.

## Security model (essentials)

- Every call names `work_dir` — the absolute path of a directory **you can read back** — and the workspace is `<work_dir>/<workspace_id>/`. Files `execute_code` writes to `/work` land there, so the `host_work_dir` in a result is a path you can open. `work_dir` is validated before it is trusted: absolute, existing, writable, and never a system location, your home directory itself, a credential directory, or this server's own config directory (`~/.config/data-toolbox-mcp`) — subdirectories included.
- `load_data` reads any file you can read, except the credential and agent-control locations under your home (`~/.ssh`, `~/.aws`, `~/.kube`, `~/.gnupg`, `~/.config/gcloud`, `~/.config/gh`, `~/.netrc`, `~/Library/Keychains`, `~/.claude`, `~/.codex` and the rest of the list gem-agent and lagent use), wherever a link directly inside one of those directories points, and any `.env` file except its templates (`.env.example`, `.env.sample`, `.env.template`, `.env.dist`). They are found under any spelling — another case, a link, the path as given or resolved ([nlink-jp/pathguard](https://github.com/nlink-jp/pathguard) makes that judgement) — and refused whether or not a file is there, with the same answer either way. It is a floor, not a boundary.
- `/work` is writable by the code `execute_code` runs, so a symlink found there is input from the sandbox. The server reaches a workspace's files through an `os.Root` on its work directory, itself opened through a root on `work_dir`: `attach_files` rejects a path that leaves it through a link (and anything that is not a regular file, and a path the same floor refuses — a `.env`, or the file a link in `~/.ssh` leads to — whether or not it exists), the server's own writes (`_upload/`, `_code/`) cannot be redirected by one, and a workspace whose directory is itself a link is refused before it is mounted. Links that stay inside `/work` keep working.
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
- [`docs/en/reference/v0.3.0-plan.md`](docs/en/reference/v0.3.0-plan.md) — v0.3.0 development plan
- [`docs/en/reference/v0.4.0-plan.md`](docs/en/reference/v0.4.0-plan.md) — v0.4.0 development plan
- [`docs/en/adr/`](docs/en/adr/) — ten ADRs (0001–0010): workspace_id, Podman, Python-only, stdio, local-build distribution, workspace mgmt + describe_runtime, container package scope, `attach_files`, `load_from_work`, and v0.4.0 UX polish

## Acknowledgements

The tool surface and the per-workspace DuckDB + container pattern are derived from [shell-agent-v2](https://github.com/nlink-jp/util-series/tree/main/shell-agent-v2). `data-toolbox-mcp` extracts and reshapes those ideas as a standalone MCP server.

## License

[MIT](LICENSE).
