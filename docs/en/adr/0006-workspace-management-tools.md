# ADR-0006: Workspace management + runtime introspection MCP tools (list_workspaces / delete_workspace / describe_runtime)

- Status: Accepted
- Date: 2026-06-05
- Driver: magi
- Generalises to: none

---

## Context

After the v0.1.0 release, real-world usage exposed three pieces of friction:

- LLM clients do not remember `workspace_id` across chat sessions. There is no way to recall "which workspace held last week's analysis."
- ADR-0001 deferred "TTL-based workspace garbage collection" to Phase 2, but in the meantime there is not even a manual way to delete a workspace.
- To inspect existing workspaces a human has to `ls ~/.data-toolbox/` on the host, which is unreachable from sandboxed MCP clients (Claude Desktop, etc.).
- **The LLM does not know what the container ships with.** Is matplotlib in there? Is Pillow usable? What fonts are installed? Does network reach? It can only find out by speculatively running `execute_code` and watching for ImportError or connection failures. This is especially painful under `network=none` (no room for trial-and-error retries).

In other words: LLM-driven operation can neither **discover or tidy workspaces** nor **discover container capabilities**.

## Decision

Add three MCP tools:

### `list_workspaces`

- **Arguments**: none
- **Result**:
  ```json
  {
    "workspaces": [
      {
        "id": "samples",
        "last_used": "2026-06-05T14:30:00Z",
        "container_state": "running"
      }
    ]
  }
  ```
- **Source of truth**: disk (the presence of `<workspace_dir>/<id>/`)
- **`last_used`**: `os.Stat().ModTime()` of `<workspace_dir>/<id>/work/analysis.duckdb`; falls back to the parent directory's ModTime when the DB file is absent
- **`container_state`**: result of `podman ps -a --filter name=data-toolbox-mcp-<id> --format {{.State}}`, normalized to `"running"` / `"stopped"` / `"absent"`

### `delete_workspace`

- **Arguments**: `{workspace_id: string}`
- **Result**: `{deleted: true, workspace_id: "..."}`
- **Behavior**:
  1. `workspace.ValidateID` validates `workspace_id` syntax (same regex as ADR-0001)
  2. Defense-in-depth: re-verify with `filepath.Clean` that the computed `<workspace_dir>/<workspace_id>` is a direct child of `<workspace_dir>` (double-guard against path traversal)
  3. If the container exists, `podman rm -f` it (idempotent on absence)
  4. Remove from the in-memory `Manager.workspaces` map
  5. `os.RemoveAll(<workspace_dir>/<workspace_id>/)` to wipe disk state completely (DuckDB file + work/ + _upload + _code)

A destructive operation, but MCP clients (Claude Desktop / Cursor) gate every tool call behind explicit user approval — protocol-level consent is already in place. No `confirm: true` argument (it would only degrade UX).

### `describe_runtime`

- **Arguments**: none
- **Result**:
  ```json
  {
    "python_version": "3.12",
    "container_image": "localhost/data-toolbox-runtime:latest",
    "packages": [
      {"name": "duckdb", "version_constraint": "~=1.1"},
      {"name": "pandas", "version_constraint": "~=2.2"},
      {"name": "polars", "version_constraint": "~=1.8"},
      {"name": "pyarrow", "version_constraint": "~=18.0"},
      {"name": "matplotlib", "version_constraint": "~=3.10"},
      {"name": "Pillow", "version_constraint": "~=11.0"}
    ],
    "fonts": ["Noto Sans CJK JP"],
    "network": "none",
    "mount_points": {"/work": "host workspace work directory; container can read/write here"},
    "notes": [
      "matplotlibrc preconfigured with Noto Sans CJK JP fallback; Japanese labels render without extra setup.",
      "DuckDB file lives at /work/analysis.duckdb inside the container."
    ]
  }
  ```
- **Data source**: compile-time constants (`internal/runtime/manifest.go`). When the Dockerfile changes, the manifest is updated in the same commit (same discipline as ADR-0005's `go:embed` sync responsibility).
- **Only `network` is read from config at request time** (`config.Container.Limits.Network`); package lists and similar static data are struct constants.
- **Accuracy stance**: things we claim are present are guaranteed to work; we do **not** advertise packages that could be reached via add-on installs under `network=bridge` — that's discoverable via `execute_code` if needed.

The intent is that the LLM calls `describe_runtime` **once at session start**; the user approves it once, and subsequent calls live in the LLM's context.

## Consequences

**Positive:**

- LLM can `list_workspaces` to discover and pick a previous workspace → cross-session continuity works.
- `delete_workspace` lets the LLM tidy up unused workspaces → first-line defense against disk bloat.
- `describe_runtime` gives the LLM "matplotlib is available," "Noto Sans CJK JP renders Japanese," "network=none so no pip install" in a single up-front call → no more speculative ImportError, no more "let's try pip install," no more "I'll fall back to English labels because fonts probably aren't installed."
- Future TTL-based GC (Phase 3+ consideration) can be built on top of the workspace tools (e.g. auto-list workspaces unused for 90 days, then propose deletes for review).
- The disk-as-truth model is consistent with `Ensure`'s idempotence (complementing ADR-0001's "survives server restart" promise).

**Negative:**

- The tool surface grows from 3 to 6. Maintenance load for inputSchema definitions and docs increases.
- One `podman ps` call per workspace per `list_workspaces` request → hundreds of milliseconds with many workspaces. Acceptable for typical personal use (<20 workspaces).
- `delete_workspace` is irreversible. Risk of LLM mishaps relies on the client-side user approval gate.
- `describe_runtime`'s manifest can drift from the actual Dockerfile (double-maintenance risk). Mitigation: an e2e test that compares the manifest against `pip list` output inside the actually-built container.

## Alternatives Considered

### A1: Only `list_workspaces`; delete remains manual

- Pros: smaller surface, destructive responsibility separated
- Cons: doesn't close the loop inside LLM-driven use (forces the user back to a terminal)
- Rejected because closing the workspace-tidying loop inside the LLM session is the point of this ADR

### A2: Skip `container_state`, return disk presence only

- Pros: no podman calls, faster
- Cons: cannot distinguish "container already running" / "container stopped" / "absent (never ensured)" → the LLM cannot judge whether it can immediately fire queries
- Rejected because `container_state` is a key decision input for the LLM, and the cost is acceptable

### A3: Extend `list_workspaces` to also return DuckDB tables per workspace

- Pros: one shot for rich info
- Cons: requires opening each DuckDB on demand → expensive, error-path complex, undefined behavior when `container_state="absent"`
- Rejected because it overshoots scope. Table listing is reachable via `query_data` with `SHOW TABLES`.

### A4: Skip `describe_runtime`; cram the info into the tools/list description

- Pros: tool surface doesn't grow
- Cons: tools/list descriptions are read implicitly by the LLM and aren't structured. Updating version info as the description string is the same maintenance pain. And there's no path to dynamic info (`pip list`, etc.) if we ever want it.
- Rejected because structured static data is more usable in LLM prompts. The one-shot cost of `describe_runtime` is small.

### A5: Make `describe_runtime` shell out to `pip list` at request time

- Pros: truly dynamic info, no drift
- Cons: a `podman exec` per call, ~1–2 seconds on average. Also introduces the trap that `pip list` may differ across `network=bridge` user-installed packages, requiring per-workspace caching.
- Rejected because a static manifest + an integration test for drift is simpler and faster.

### A6: Implement TTL-based auto-GC first

- Pros: full automation
- Cons: policy design is heavy (how many days? archive vs delete?). Phase 3-class effort.
- Rejected because shipping manual controls first and gathering usage data is a safer staircase to automation.

## See also

- ADR-0001: workspace_id scoping and lifecycle
- ADR-0004: stdio-only transport
- Memory: `feedback_structured_mcp_tool_errors` (delete failures surface as `{code: "workspace_failed", ...}`)
- Memory: `feedback_mcp_client_validates_input_schema_enum` (delete_workspace takes a plain string `workspace_id`, so enum validation is not in play)
