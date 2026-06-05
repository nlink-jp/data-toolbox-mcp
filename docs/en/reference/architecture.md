# Architecture: data-toolbox-mcp

> Status: Draft (Phase 0)
> Date: 2026-06-05

This document describes the overall architecture of `data-toolbox-mcp`, building on ADR-0001 through ADR-0005 (Phase 0) plus ADR-0006 / ADR-0007 (added in v0.2.0), ADR-0008 / ADR-0009 (added in v0.3.0), and ADR-0010 (added in v0.4.0). Details settled during implementation (library picks, function names, the final JSON Schema for tools) are out of scope here.

## 0. Binary layout — single binary + subcommands

`data-toolbox-mcp` is one Go binary that switches function via subcommands (per the `feedback_single_binary_subcommand` memory pattern):

| Subcommand | Purpose |
|------------|---------|
| `serve` (or no argument) | Start the MCP stdio server (the remainder of this document targets serve mode) |
| `build-runtime` | Unpack the `go:embed`-bundled Dockerfile and run `podman build` to produce the runtime image (ADR-0005) |
| `doctor` | Environment diagnostics: Podman presence, `podman machine` state, runtime image presence, config validity |
| `version` | Show version |

This packages the MCP server, build tool, and diagnostic into a single binary, simplifying distribution and consistency management.

## 1. Overview

```
┌──────────────────────┐
│ MCP Client           │  Claude Desktop / Cursor / Cline / ...
│ (LLM + UI)           │
└──────────┬───────────┘
           │ stdio (JSON-RPC over stdio)
           ▼
┌──────────────────────────────────────────────┐
│ data-toolbox-mcp server (Go)                 │
│                                              │
│  ┌─────────────┐  ┌────────────────────────┐ │
│  │ Transport   │  │ Tool Dispatcher        │ │
│  │ stdio       │─▶│  load_data             │ │
│  │ JSON-RPC    │  │  query_data            │ │
│  └─────────────┘  │  execute_code          │ │
│                   └────────┬───────────────┘ │
│                            │                 │
│  ┌─────────────────────────▼──────────────┐  │
│  │ Workspace Manager                      │  │
│  │  - in-memory map: workspace_id → ref   │  │
│  │  - disk state: <workspace_dir>/<ws>/   │  │
│  └────────┬─────────────────────┬─────────┘  │
└───────────┼─────────────────────┼────────────┘
            │ podman exec/run     │ host fs (allowed_paths)
            ▼                     ▼
┌──────────────────────┐  ┌─────────────────────┐
│ Podman Container     │  │ Host filesystem     │
│ python:3.13-slim     │  │  allowed_paths/...  │
│ + duckdb,pandas,...  │  │  workspace_dir/...  │
│                      │  │                     │
│ /work ──────────────────▶ workspace/<ws>/work/│
│ analysis.duckdb ────────▶ workspace/<ws>/analy│
└──────────────────────┘  └─────────────────────┘
```

Primary data flows:

- **load_data**: MCP server reads a host file (only inside `allowed_paths`) and asks Python inside the container to load it into DuckDB
- **query_data**: Python inside the container runs SQL and returns a JSON array
- **execute_code**: Python inside the container runs arbitrary code and returns stdout/stderr/exit_code

## 2. Process boundaries (trust)

| Entity | Trust level | Rationale |
|--------|-------------|-----------|
| MCP client (LLM) | Semi-trusted | User-chosen software, but LLM-generated code is unpredictable |
| MCP server process | Trusted | Built and signed by this project |
| Podman container | Semi-trusted | Runs LLM-generated code inside |
| Host filesystem inside `allowed_paths` | Trusted | Explicitly opt-in by the user |
| Host filesystem outside `allowed_paths` | Trusted but blocked | Server denies at the boundary |
| `workspace_dir/<ws>/` | Trusted | Owned and managed by the server |

Guards:

- LLM → MCP server: stdio only; coarse schema check before JSON-RPC dispatch
- MCP server → host fs: `allowed_paths` whitelist + re-check after symlink resolution
- MCP server → container: `podman exec` only, with input-size limits
- Container → host: only `/work` and the DuckDB file are mounted; `network=none` blocks external traffic

## 3. Data flow (happy path)

### 3.1 load_data(workspace_id, file_path, table_name)

```
1. MCP server validates workspace_id ([a-zA-Z0-9_-]{1,64})
2. MCP server checks file_path against allowed_paths (after symlink resolution)
3. MCP server asks Workspace Manager to ensure(workspace_id)
   - not in in-memory map → `podman container ls`; if missing, `podman run`; if present, attach
4. MCP server copies the host file_path to <workspace_dir>/<ws>/work/_upload/<fname>
5. MCP server runs `podman exec` to instruct Python to "duckdb: CREATE TABLE table_name AS SELECT * FROM read_csv_auto('/work/_upload/<fname>')"
6. Python inserts into the DuckDB file and returns {rows, schema} on stdout as JSON
7. MCP server parses the JSON and returns {rows_loaded, schema} to the MCP client
```

### 3.2 query_data(workspace_id, sql)

```
1. MCP server validates and ensures workspace_id (same as load_data)
2. MCP server auto-appends LIMIT 20000 if no LIMIT is present (outer LIMIT wrap). The default is configurable via `[query] default_row_limit`
3. MCP server `podman exec`s Python to run the SQL
4. Python executes the SQL against DuckDB and returns a JSON array on stdout
5. If the row count hit the LIMIT, MCP server issues an additional `SELECT COUNT(*) FROM (user_sql) sub` to fetch the true total (v0.4.0 / ADR-0010).
6. Returns {rows, row_count, limit_applied, limit_reached, truncated, total}. If not truncated, total = row_count; if truncated, total comes from the internal COUNT(). If the COUNT() itself times out, total is null and total_unavailable_reason is set.
7. If the error mentions "Table ... does not exist" (CatalogException), MCP server augments the structured `script_failed` error's `details` with missing_table / available_tables_in_this_workspace / other_workspaces hints from SHOW TABLES + Manager.List() (v0.4.0).
```

### 3.3 execute_code(workspace_id, language, code)

```
1. MCP server checks language == "python" (else unsupported_language error)
2. MCP server validates and ensures workspace_id
3. MCP server writes the code to <workspace_dir>/<ws>/work/_code/<uuid>.py
4. MCP server starts `python /work/_code/<uuid>.py` via `podman exec`
5. Python runs the code, returns stdout/stderr/exit_code
6. If finished within the timeout, return; else kill the container
7. Temp code files are retained (for debugging, with TTL cleanup planned in Phase 2)
8. Result includes host_work_dir = <workspace_dir>/<ws>/work/ (added in v0.2.1) so the LLM can surface where its generated artifacts live on the host.
```

### 3.4 list_workspaces() — v0.2.0 (ADR-0006)

```
1. MCP server walks workspace_dir via os.ReadDir
2. Each entry is passed through workspace.ValidateID; non-workspace entries are skipped
3. For each candidate:
   - last_used: mtime of <workspace_dir>/<id>/work/analysis.duckdb
                (falls back to the directory mtime if the DB file is absent)
   - container_state: `podman ps -a --filter name=data-toolbox-mcp-<id> --format {{.State}}`
                      normalized to "running" / "stopped" / "absent"
4. Returns {workspaces: [{id, last_used, container_state, host_work_dir}]}
   - host_work_dir = filepath.Join(workspace_dir, id, "work")
```

No `Ensure` needed (disk + podman only). No side effects on containers.

In v0.2.1 the per-item `host_work_dir` field was added (ADR-0006 amendment) so the LLM knows where `/work/foo.png` lands on the host without having to ask separately.

### 3.5 delete_workspace(workspace_id, dry_run?) — v0.2.0 + v0.4.0 (ADR-0006 + ADR-0010)

```
1. MCP server validates workspace_id via workspace.ValidateID
2. Defense-in-depth: re-verify via filepath.Clean that the computed
   <workspace_dir>/<id> is a direct child of <workspace_dir>
3. dry_run = true (v0.4.0):
   - podman.FindByName + ContainerState
   - Compute host_paths and disk_usage_bytes (walked from the directory tree)
   - Return {would_delete, container_id, container_state, host_paths, disk_usage_bytes} without deleting anything
4. dry_run = false (default, existing behavior):
   - podman.FindByName looks up the container
   - If present, podman rm -f
   - Remove from in-memory Manager.workspaces map
   - os.RemoveAll(<workspace_dir>/<id>/) wipes the disk state
   - Return {deleted: true, workspace_id}
```

dry_run = false is irreversible. Layered with the MCP client's user-approval gate, dry_run = true lets the LLM show "this is what would be deleted" first.

### 3.6 describe_runtime() — v0.2.0 (ADR-0006)

```
1. MCP server reads static constants from internal/runtime/manifest.go
   (python_version / packages / fonts / mount_points / notes)
2. Reads config.Container.Limits.Network at request time and composes
3. Returns {python_version, container_image, packages, fonts, network, mount_points, notes}
```

No `Ensure`, no Podman call — pure static data + one config read. Intended to be called once by the LLM at session start.

**Manifest truth**: when the Dockerfile changes, `internal/runtime/manifest.go` is updated in the same commit (same discipline as ADR-0005's `go:embed` sync responsibility). Drift is caught by an e2e test that compares the manifest against `pip list` inside the actual container.

### 3.7 attach_files(workspace_id, paths) — v0.3.0 (ADR-0008)

```
1. MCP server validates workspace_id (no Ensure; disk-only check)
2. For each path:
   - Resolve "/work/<sub>" or "<sub>" to <host_work_dir>/<sub>
   - filepath.Clean + <host_work_dir> prefix check (path-traversal defense)
   - os.Stat for existence + size
3. Dispatch by extension:
   - .png / .jpg / .svg / ... → image content block (base64 + mimeType)
   - .csv / .json / .md / ... → text content block (head + tail ellipsis on overflow)
   - Otherwise → metadata-only text block (host path + size + sha256)
4. Files larger than max_single_size_bytes (per-file) or beyond max_total_size_bytes (cumulative)
   are downgraded to metadata-only.
5. Build the MCP content array and return it as mcpserver.RawResult.
6. handleToolsCall uses the RawResult's content as-is (regular tools are
   JSON-marshaled into a single text block).
```

No Ensure (no Podman exec; host-side reads only). The use case is letting the LLM hand back plots generated by `execute_code` without needing the user to set up "connected folders" in Claude Desktop.

### 3.8 load_from_work(workspace_id, file_path, table_name) — v0.3.0 (ADR-0009)

```
1. MCP server validates workspace_id
2. file_path must start with "/work/" (otherwise invalid_arguments)
3. Strip the /work prefix and resolve to <host_work_dir>/<sub>
4. filepath.Clean + <host_work_dir> prefix check (path-traversal defense)
5. Ensure the workspace
6. Pick a reader by extension (same table as load_data: csv / json / parquet)
7. Run a Python script via execScript:
   CREATE OR REPLACE TABLE "<table>" AS SELECT * FROM read_xxx_auto('<container_path>')
   SELECT COUNT(*) + DESCRIBE to fetch row count and schema
8. Return {rows_loaded, schema} (same shape as load_data)
```

Contrast with `load_data`: `load_data` is host → sandbox ingest (with `allowed_paths` check); `load_from_work` is sandbox → table (only under `/work`, no `allowed_paths` involvement).

Implementation factors the reader-pick and script assembly into a shared helper with `load_data` for DRY.

### 3.9 describe_workspace(workspace_id) — v0.4.0 (ADR-0010)

```
1. MCP server validates workspace_id and Ensures the workspace
2. podman exec runs Python: SHOW TABLES + DESCRIBE per table
3. Python emits {tables: [...]} as JSON
4. Returns {workspace_id, host_work_dir, container_state, tables: [{name, columns: [{name, type}]}]}
```

Symmetric counterpart to `list_workspaces`: the latter lists the workspaces, this one lists the contents of one workspace. Row counts (`row_count`) are excluded in v0.4.0 — SELECT COUNT across all tables is heavy; revisit via a separate ADR if demand surfaces.

## 4. State model

### 4.1 In-memory (within the server process)

```go
type WorkspaceManager struct {
    mu         sync.Mutex
    workspaces map[string]*Workspace  // key: workspace_id
}

type Workspace struct {
    ID          string
    ContainerID string  // Podman container ID
    LastUsed    time.Time
    InUse       bool    // concurrency guard per workspace
}
```

### 4.2 Disk (persistent)

```
<workspace_dir>/
└── <workspace_id>/
    ├── analysis.duckdb       # DuckDB data file (mounted at /work/analysis.duckdb inside the container)
    └── work/                 # mounted at /work inside the container
        ├── _upload/          # host files copied in by load_data
        ├── _code/            # transient .py files written by execute_code
        └── (user artifacts)  # plot.png, etc.
```

### 4.3 In-memory ⇄ disk sync points

Per the `feedback_in_memory_disk_sync` memory, all state syncing **goes through the WorkspaceManager**:

- `ensure(workspace_id)` will:
  1. Look up the in-memory map
  2. If absent, check `<workspace_dir>/<workspace_id>/` on disk
  3. If the directory exists, reattach the DuckDB file and ask Podman for the ContainerID
  4. If the directory is absent, create it and `podman run` a new container
- The server does not eagerly load existing disk state at startup (lazy: only touched at ensure time)
- Whether to stop containers when the server stops or "leave them running and reattach on next ensure" is toggled by `[container] stop_on_exit = true|false` (default true, conservative)

## 5. Error & lifecycle

### 5.1 MCP protocol principle

Per the `feedback_mcp_proxy_always_responds` memory, **MCP requests are never left unanswered**. Container failures, timeouts, Podman errors — everything returns as a JSON-RPC error response.

### 5.2 Timeouts

- `[container.limits] timeout_seconds` sets a per-tool timeout
- On timeout: `podman kill <container>` terminates the container → next ensure for that workspace restarts it
- Per the `feedback_mcp_no_protocol_cancel` / `feedback_child_process_exit_status` memories, child-process kill is the only interruption mechanism and the exit status must surface

### 5.3 Container crash

- If `podman exec` errors because the container disappeared, remove from in-memory map and return a JSON-RPC error
- The next call for the same workspace_id triggers ensure, auto-recovering

### 5.4 MCP disconnect

- When stdio EOFs, perform a graceful shutdown
- Stop containers per config (or leave them running)
- In-flight requests are interrupted without waiting for completion (cleaned up by timeout)

## 6. Security model

Per the `feedback_security_first` memory, this is integrated from Day-1 in Phase 1.

### 6.1 Host file access

- `allowed_paths` whitelist
- Path resolution:
  1. Make the input `file_path` absolute via `filepath.Abs`
  2. Resolve symlinks with `filepath.EvalSymlinks`
  3. Check whether the resolved path is prefixed by any of `allowed_paths`
  4. If not, return `path_not_allowed`
- This blocks evasion via `/Users/me/symlink-to-secret`-style trickery

### 6.2 workspace_id validation

- Regex `^[a-zA-Z0-9_-]{1,64}$` defends against directory traversal
- Same workspace_id is embedded in the container name, so it must match Podman naming rules

### 6.3 Container runtime limits

| Item | Default | Config key |
|------|---------|-----------|
| CPU | 1.0 | `[container.limits] cpu` |
| Memory | 2GB | `[container.limits] memory` |
| Timeout | 60s | `[container.limits] timeout_seconds` |
| Network | none | `[container.limits] network` |
| Read-only fs | false | `[container.limits] read_only` (Phase 2 consideration) |
| Query row limit | 20000 | `[query] default_row_limit` |

**Relationship between network and pip install (Phase 0 Q5-4 resolution)**: With `network=none` (default), the container blocks outbound connections, so `pip install` called inside `execute_code` simply fails (the natural behavior). Switching `network` to another value lets LLM-generated code run `pip install` freely, but that choice is the responsibility of the user editing the config. Phase 1 does not implement fine-grained ACL like "allow only pip."

### 6.4 Container leak prevention

- Every container is labeled `app=data-toolbox-mcp`
- On server start and stop, `podman ps --filter label=app=data-toolbox-mcp` discovers orphans
- Orphan containers are either reattached at ensure time or force-removed and restarted

## 7. Testability

Per the ADR-0004 strategy and the "dummy MCP client automated E2E harness" (RFP §4 Phase 1), tests are organized in two layers.

### 7.1 Unit tests

- One per package (transport / workspace / tools / config)
- Lift DuckDB access into pure functions wherever possible, keeping them mockable
- Podman-related code uses `exec.Command` directly without interface abstraction; tests swap PATH to a mock binary

### 7.2 Integration tests

- Real-Podman integration tests live under `_test/integration/`
- Run only when `DATA_TOOLBOX_TEST_PODMAN=1` is set (CI skip-friendly)
- Pattern after shell-agent-v2's `internal/sandbox/integration_test.go`

### 7.3 Automated E2E test harness (dummy MCP client)

- A dummy-MCP-client driver lives in `_test/e2e/`
- Build the binary → spawn → send JSON-RPC tool calls → verify responses
- Repurpose the shell-mock structure from mcp-guardian's `internal/proxy/proxy_test.go`
- Scenarios covered:
  - Full workspace lifecycle (init → load → query → execute → cleanup)
  - Error paths (unsupported_language, path_not_allowed, workspace_id_invalid)
  - Timeout → forced container stop → next-ensure recovery
  - Concurrent-access rejection on the second caller

## 8. Out of scope (Phase 1)

Aligned with RFP §3 / Design Decisions "explicitly out of scope":

- HTTP / SSE transport (ADR-0004)
- All LLM integration features (the project stays LLM-agnostic)
- shell-agent-v2's 4-facility memory / System Rules / Global Memory
- Auth / authz (assumes personal machine + stdio; route through mcp-guardian when needed)
- GUI / Web UI
- Non-Python runtimes (R / Node / Bash; ADR-0003)
- TTL-based workspace garbage collection (Phase 2)
- Fine-grained ACL for package installs (when `network` is not `none`, LLM-generated code can run `pip install`; we don't add anything beyond that in Phase 1 — see §6.3)
- URL fetch (accepting `https://`, `s3://`, etc. in `load_data`) — Phase 2 consideration

## See also

- ADR-0001: workspace_id scoping and lifecycle
- ADR-0002: Podman as the container engine without abstraction
- ADR-0003: Python-only runtime
- ADR-0004: stdio-only transport
- ADR-0005: Local-build distribution of the runtime container image
- `_wip/data-toolbox-mcp/docs/en/data-toolbox-mcp-rfp.md` (full RFP)
- `_wip/data-toolbox-mcp/docs/en/reference/phase1-plan.md` (Phase 1 development plan)
