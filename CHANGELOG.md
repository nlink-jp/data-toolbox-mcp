# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.1] - 2026-06-05

Surface the on-host path of `/work` to the LLM so generated artifacts (PNG plots, exported CSVs, etc.) are handed back via a filesystem reference instead of base64.

### Added

- `host_work_dir` field in `execute_code` result and in each `list_workspaces` item. Value is `filepath.Join(workspace_dir, workspace_id, "work")`, the absolute host path that mirrors the container's `/work` mount.
- Expanded `describe_runtime` notes with the artifact-exchange convention ("anything you write to `/work/<name>` appears on the host at `<workspace_dir>/<workspace_id>/work/<name>` ... do NOT base64-encode and embed in the response") and a userns / uid 1000 note.
- Updated `describe_runtime` `mount_points["/work"]` description to point at the artifact-exchange notes.
- ADR-0006 was revised in place with a v0.2.1 amendment section explaining the change. ADR-0006 Revisions line records the amendment.
- `architecture.md` §3.3 (execute_code) and §3.4 (list_workspaces) updated to include `host_work_dir` in the documented return shapes.

### Why

Real-machine verification on 2026-06-05 (Claude Desktop, v0.2.0) revealed the LLM was attempting to **base64-encode generated PNG plots into the response** because it did not know the on-host location of files it wrote to `/work/`. The fix is purely informational (no runtime behavior change): tell the LLM where things land on the host, statically via `describe_runtime` notes and dynamically via per-call `host_work_dir` fields.

### Backward compatibility

Strictly additive: older clients ignore the new `host_work_dir` field. No tool arguments changed.

## [0.2.0] - 2026-06-05

Workspace management, runtime introspection, and plotting support.

### Added

- **ADR-0006**: three new MCP tools (`list_workspaces`, `delete_workspace`, `describe_runtime`).
  - `list_workspaces`: lists every workspace with on-disk state and its current `container_state` (`running` / `stopped` / `absent`). LLM clients can recover state across chat sessions and pick up where they left off.
  - `delete_workspace`: stops the container (if any) and wipes a workspace's on-disk state. Irreversible. Defense-in-depth: validates the id syntax and re-verifies the computed path is a direct child of `workspace_dir`.
  - `describe_runtime`: returns the static manifest of the runtime container (python version, pip packages, fonts, mount points, notes) merged with the live `network` setting from config. Intended to be called once at session start so the LLM knows what it can `import` and whether the network is reachable.
- **ADR-0007**: runtime container expanded to support plotting.
  - Switched base image to `python:3.12-slim` (proven stable in shell-agent-v2).
  - Added `fonts-noto-cjk` and `ca-certificates` via apt.
  - Added `matplotlib~=3.10` and `Pillow~=11.0` via pip.
  - Shipped `/etc/matplotlib/matplotlibrc` with `font.sans-serif: Noto Sans CJK JP, DejaVu Sans, Arial, Liberation Sans` (CJK first — matplotlib's Agg backend doesn't do per-glyph fallback, so the CJK font must be first to render Japanese without `UserWarning`).
  - `MATPLOTLIBRC` env var pinned in the container.
- `internal/runtime/manifest.go`: the static manifest backing `describe_runtime`. Maintained in lock-step with `runtime/Dockerfile` (same-commit discipline).
- `internal/workspace.WorkspaceInfo` + `Manager.List` + `Manager.Delete` + `PodmanClient.ContainerState`.
- E2E tests: `TestE2E_v020_WorkspaceLifecycle`, `TestE2E_v020_JapaneseMatplotlib`, `TestE2E_v020_ManifestDrift` (`describe_runtime` vs `pip list` inside the actually-built container).
- `samples/README.md`: Stage 8 (list/delete) and Stage 9 (describe_runtime + Japanese plot) verification prompts.

### Changed

- Runtime container image size grew from 692MB to 882MB (under the 900MB budget set in ADR-0007).
- Tool surface: 3 → 6 (`load_data` / `query_data` / `execute_code` / `list_workspaces` / `delete_workspace` / `describe_runtime`).
- `cmd/build_runtime_test.go` now asserts presence of `python:3.12-slim`, `matplotlib`, `Pillow`, `fonts-noto-cjk`, `ca-certificates`, `Noto Sans CJK JP`, and `MATPLOTLIBRC` in the embedded Dockerfile.

### Notes

- Real-machine verification on 2026-06-05 confirmed Japanese labels render via `execute_code` under `network=none` with `-W error::UserWarning` (strictest check).
- Out of scope for v0.2.0 (deferred to later ADRs): scipy, scikit-learn, seaborn, plotly, graphviz, openpyxl, fonts-noto-cjk-extra, TTL-based workspace GC.

## [0.1.0] - 2026-06-05

Initial public release.

### Added

- Phase 0: RFP, ADR-0001 through ADR-0005, architecture document, and Phase 1 development plan (under `docs/`).
- Phase 1 Track A: repository scaffold with single-binary + subcommand structure (`serve` / `build-runtime` / `doctor` / `version`).
- Phase 1 Track B: MCP stdio framework (`internal/transport`, `internal/jsonrpc`, `internal/mcpserver`). Supports `initialize`, `notifications/initialized`, `tools/list`, `tools/call`.
- Phase 1 Track C: workspace + Podman lifecycle manager (`internal/workspace`). workspace_id validation, idempotent `Ensure`, container reattachment across server restarts, label-based orphan detection, and `keep-id` userns mapping. Config loader at `internal/config` rejects unknown TOML keys.
- Phase 1 Track D: three MCP tools (`internal/tools`):
  - `load_data` — copies a host file into the workspace's `_upload/` directory and creates/replaces a DuckDB table.
  - `query_data` — runs SQL with an auto-applied `LIMIT 20000` (configurable) and JSON-array output.
  - `execute_code` — runs Python inside the workspace container. `language="python"` only.
- Phase 1 Track E: `build-runtime` subcommand. The Dockerfile is `go:embed`-ed and unpacked at build time. `doctor` reports Podman state, runtime image presence, and config defaults.
- Phase 1 Track F: dummy MCP client end-to-end test harness under `e2e/`. Build-tagged with `//go:build e2e`; run via `go test -tags e2e -v ./e2e/...` (requires `DATA_TOOLBOX_TEST_PODMAN=1`). Four scenarios: full lifecycle, error paths, timeout enforcement, sequential workspace isolation.

### Notes

- The DuckDB file lives inside the workspace's `work/` directory and is exposed to the container through the single `/work` mount; no separate file bind-mount.
- Allowed-paths defense resolves symlinks on both the input path and the allowed-paths entries before comparing, so symlink jail-breaks are rejected.

### Phase 2

- **Structured tool errors** (`internal/toolerr`). Tool errors now travel as `{"code":"...","message":"...","details":{...}}` JSON inside the MCP content block, with `isError:true`. Codes are stable slugs for client branching: `invalid_arguments`, `missing_argument`, `invalid_workspace_id`, `invalid_table_name`, `path_not_allowed`, `unsupported_language`, `workspace_failed`, `container_failed`, `script_output_parse`, `script_failed`. Unstructured errors still fall back to plain text.
- **log_file + log_level wired through** (`internal/logging`). Setting `[server] log_file` causes the server to write to that path *and* stderr. The file rotates on startup, keeping `KeepGenerations=5` generations (`server.log` → `server.log.1` → ... → `.5` drops). Levels: `debug` / `info` / `warn` / `error`.
- **`doctor` enhancements**: on macOS the command now parses `podman machine list --format json` and reports whether a machine is running with an actionable hint when it isn't. It also looks for `config.toml` in the search locations and reports parse errors as `[FAIL]` rather than silently using defaults.
- **Client setup documentation** for Claude Desktop and Cursor (`docs/{en,ja}/reference/client-setup.md`), including troubleshooting for the common `podman machine`, runtime image, allowed_paths, and pip install scenarios.
- **Sample data** under `samples/` (sales.csv 40 rows / products.json 10 rows / logs.jsonl 41 rows) plus a graded `samples/README.md` (Stage 1–7) for end-to-end verification.
- **Real-machine verification via Claude Desktop completed (2026-06-05)**: 11 test cases covering load (CSV/JSON/JSONL), SQL aggregation, JOIN, window functions, quantiles, pandas + polars analysis via `execute_code`, plus three security-boundary negative cases (cross-workspace catalog access, path traversal, language enum). All cases produced the expected results.

### Notes from real-machine verification

- Claude Desktop pre-validates `inputSchema.enum` on the client side: `execute_code` with `language="bash"` is rejected as `invalid_enum_value` before the request reaches the server, so the server-side `unsupported_language` path is not exercised in this flow. The server-side check remains as defense-in-depth for clients that do not pre-validate.
- DuckDB-side errors (e.g. table does not exist in a fresh workspace) surface via `script_failed` with the `CatalogException` message in `details.stderr`; this matches the structured-error contract.
