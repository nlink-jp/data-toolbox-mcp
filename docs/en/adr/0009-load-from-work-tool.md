# ADR-0009: load_from_work — load sandbox `/work` files directly into DuckDB

- Status: Accepted
- Date: 2026-06-06
- Driver: magi
- Generalises to: none

---

## Context

Real-machine verification on 2026-06-06 (Claude Desktop) exposed a friction:

- `load_data`'s `file_path` is a **host absolute path** and is only accepted if it lies under `allowed_paths` (ADR-0001 / architecture §6.1).
- But the workspace's working area, `<workspace_dir>/<id>/work/`, is **not in `allowed_paths` by default**.
- So a typical workflow — LLM `execute_code` produces `polars.DataFrame.write_csv("/work/derived.csv")`, then wants to `load_data` that derived file as a new table — does not work.
- The LLM's workaround is to call `duckdb.read_csv_auto("/work/derived.csv")` inside `execute_code`, which essentially re-implements `load_data`'s "make it a table" job in Python.

Loading data is the same operation conceptually, but the path was **bifurcated by whether the source was on the host or in the sandbox**.

The shell-agent-v2 sandbox layer (around `internal/sandbox/cli.go`) covered the equivalent scenario with a "load files inside the container directly into DuckDB" feature; we are borrowing that idea.

## Decision

Add a new tool, **`load_from_work`**.

### `load_from_work`

- **Arguments**: `{workspace_id: string, file_path: string, table_name: string}`
  - `file_path` is a **container-absolute path**. It must start with `/work/` (e.g. `"/work/derived.csv"`, `"/work/subdir/data.parquet"`).
  - `table_name` follows the same SQL-identifier rule as `load_data`: `^[a-zA-Z_][a-zA-Z0-9_]*$`.
- **Result**: same shape as `load_data` — `{rows_loaded: int, schema: [{name, type}, ...]}`.
- **Behavior**:
  1. `workspace.ValidateID` checks `workspace_id`.
  2. Verify `file_path` starts with `/work/`; otherwise return a structured `invalid_arguments` error.
  3. Strip the leading `/work` and resolve to a subpath of `<host_work_dir>`.
  4. `filepath.Clean` + prefix check confirms the result is still inside `<host_work_dir>` (path-traversal defense-in-depth).
  5. `Ensure` the workspace.
  6. Choose a reader by extension (same table as load_data):
     - `.csv` / `.tsv` → `read_csv_auto`
     - `.json` / `.jsonl` / `.ndjson` → `read_json_auto`
     - `.parquet` → `read_parquet`
     - otherwise → default to `read_csv_auto`
  7. Run a Python script via `execScript`: `CREATE OR REPLACE TABLE "<table>" AS SELECT * FROM read_xxx_auto('<container_path>')` + `SELECT COUNT(*)` + `DESCRIBE`.
  8. Parse JSON output and return.

### Relationship to `load_data`

- **Separation of concerns**:
  - `load_data` — host fs → workspace: ingest a host file into the sandbox and table-ize (`allowed_paths` check applies).
  - `load_from_work` — sandbox → table: table-ize a file that is already in the sandbox (no `allowed_paths` check, only `/work` subtree).
- The argument shapes match deliberately, so the LLM only has to choose by source location; the cognitive load is small.
- Only the semantics of `file_path` differ (host absolute vs `/work/...`).

### Security implications

- `load_from_work` bypasses `allowed_paths` because the target is **inside the sandbox**.
- Defense-in-depth still prevents escape outside `<host_work_dir>`:
  - Requires `/work/` prefix.
  - Re-checks resolved path against the `<host_work_dir>` prefix.
- `/work/_code/<uuid>.py`-style internal files are readable, but those are user code, so not a real concern.

## Consequences

**Positive:**

- The LLM can compose "`execute_code` writes a CSV → `load_from_work` table-izes it" as a natural division of labor.
- The symmetry between `load_data` (host source) and `load_from_work` (sandbox source) makes the API legible as a data-source-aware pair.
- No new `allowed_paths` setup is needed; "generate and table-ize" works out of the box on a fresh workspace.
- `load_data`'s behavior and security model are unchanged (backward compatible).

**Negative:**

- Tool surface grows 7 → 8 (in combination with ADR-0008, 6 → 8).
- Python-script generation overlaps with `load_data` (DRY). We will factor the reader-pick and script assembly into a shared helper.
- If the LLM misuses the two, an `allowed_paths` misconfig could be sidestepped via `load_from_work`. However the target is always a workspace file, so it's not a data-leak vector toward the outside world.

## Alternatives Considered

### A1: Add `source: "host" | "work"` to `load_data`

- Pros: tool count stays at 6, smaller API surface.
- Cons: one tool with two responsibilities (auth logic + path interpretation); inputSchema becomes conditional ("if `source=host` then `allowed_paths` check, else `/work` prefix check"); easy for the LLM to default wrong.
- Rejected because separation of concerns reads better and matches the ADR-driven philosophy.

### A2: Have `load_data` auto-interpret `/work/...` as a sandbox path

- Pros: no API change.
- Cons: collides with ADR-0003's "host absolute path" contract, can be confused with a literal host directory named `/work`, and the schema/docs need a "`/work` is magic" carve-out.
- Rejected — explicit splitting is simpler to test, document, and teach.

### A3: Auto-extend `allowed_paths` with `<workspace_dir>/*/work` at startup

- Pros: existing `load_data` would just work.
- Cons: `allowed_paths` would change meaning from "user-declared whitelist" to "dynamically extended," weakening ADR-0001's security model and complicating drift detection in tests.
- Rejected — changes to the security model deserve ADR-level discussion; for now a separate route (new tool) is safer.

### A4: Do nothing; keep using `duckdb.read_csv_auto` inside `execute_code`

- Pros: zero work.
- Cons: persistent inconsistency; LLM keeps mixing up "make it a table" and "make it a DataFrame."
- Rejected — sacrifices the user-experience win that the LLM itself rated highly.

## See also

- ADR-0001: workspace_id scoping and lifecycle (`<workspace_dir>/<id>/work/` layout)
- ADR-0003: `load_data`'s host-path contract
- ADR-0006 (v0.2.1 amendment): `host_work_dir` and the artifact-exchange convention
- ADR-0008: the dual problem — return sandbox artifacts as MCP content (`attach_files`)
