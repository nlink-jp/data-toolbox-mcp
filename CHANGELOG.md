# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
