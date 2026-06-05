# AGENTS.md — data-toolbox-mcp

Navigation hints for AI agents (Claude Code, Cursor, etc.) working inside this project.

## What this project is

DuckDB + containerized Python execution exposed as a single-binary MCP server (stdio). LLM-independent extraction of shell-agent-v2's tool layer. Phase 1 in progress.

## Build / test

- `make build` — never `go build` directly (writes to `dist/`)
- `make test` — runs all Go unit tests
- `make runtime-image` — builds the Podman runtime image (wraps `data-toolbox-mcp build-runtime`)
- `make build-all` — cross-compile for darwin/linux × arm64/amd64

Direct `go build` is **forbidden** by project convention; the wrapped form sets `-ldflags -X cmd.Version=...` from `git describe`.

## Project structure

| Path | Role | Phase 1 track |
|------|------|---------------|
| `main.go` | Entry point, delegates to cmd.Execute() | A |
| `cmd/` | cobra subcommands (root / serve / build-runtime / doctor / version) | A |
| `runtime/Dockerfile` | Source for the Python runtime container image (embedded via go:embed in Track E) | E |
| `internal/transport/` | MCP stdio JSON-RPC framing | B |
| `internal/jsonrpc/` | JSON-RPC 2.0 types | B |
| `internal/mcpserver/` | MCP protocol (initialize, tools/list, tools/call) | B |
| `internal/workspace/` | workspace_id-scoped Podman + DuckDB lifecycle | C |
| `internal/tools/` | 8 tools: `load_data` / `query_data` / `execute_code` + v0.2.0 `list_workspaces` / `delete_workspace` / `describe_runtime` + v0.3.0 `attach_files` / `load_from_work` | D, v0.2.0, v0.3.0 |
| `internal/tools/load_helpers.go` | Shared `chooseReader` / `validateTableName` / `buildLoadScript` / `runLoadScript` used by `load_data` and `load_from_work` | v0.3.0 |
| `internal/runtime/manifest.go` | Static manifest backing `describe_runtime`; in lock-step with `runtime/Dockerfile` | v0.2.0 |
| `internal/config/` | config.toml + env-var loading | A/C |
| `internal/logging/` | log_file + log_level wiring with startup rotation | Phase 2 |
| `internal/toolerr/` | structured `{code, message, details}` tool errors | Phase 2 |
| `e2e/` | Dummy MCP client E2E harness (build tag `e2e`) | F, v0.2.0 |
| `docs/{en,ja}/` | RFP, ADRs (0001-0009), architecture, phase1-plan, v0.2.0-plan, v0.3.0-plan | Phase 0, v0.2.0, v0.3.0 |

## ADR cheat sheet

- **ADR-0001**: `workspace_id` is the explicit key for container + DuckDB scope. Validate as `^[a-zA-Z0-9_-]{1,64}$`.
- **ADR-0002**: Podman is fixed. No engine abstraction. Call `podman` via `exec.Command`.
- **ADR-0003**: Python is the only supported runtime. Reject `language != "python"` with `unsupported_language`.
- **ADR-0004**: stdio transport only. No HTTP/SSE in Phase 1.
- **ADR-0005**: Local-build distribution. Dockerfile lives at `runtime/Dockerfile` and is embedded via `go:embed` into the binary; `build-runtime` unpacks it and calls `podman build`.
- **ADR-0006**: `list_workspaces` (no args) + `delete_workspace` (workspace_id) + `describe_runtime` (no args). Disk is the truth source for list/delete; describe_runtime returns the `internal/runtime.Default` manifest merged with the live `network` setting.
- **ADR-0007**: Runtime image is `python:3.12-slim` + `fonts-noto-cjk` + matplotlib + Pillow with `Noto Sans CJK JP` first in `font.sans-serif` (matplotlib Agg has no per-glyph fallback). Image budget < 900MB.
- **ADR-0008**: `attach_files` returns workspace `/work` files as MCP image/text/metadata content blocks. Extension-based dispatch, per-file 10 MiB / cumulative 20 MiB caps (configurable via `[attach]`), path-traversal defense-in-depth.
- **ADR-0009**: `load_from_work` table-izes a `/work/<sub>` file directly, bypassing `allowed_paths` (because the file is already in the sandbox). `file_path` must start with `/work/`.

## Gotchas

- macOS users must run `podman machine start` first (see memory `Podman Machine on macOS`).
- `network=none` is the default; this is also how Q5-4 (pip install) gets gated — flipping `network` to `bridge` is the user-facing knob.
- `query_data` auto-applies `LIMIT [query] default_row_limit` (= 20000) when SQL has no LIMIT. Don't strip user-supplied LIMITs.
- All tools accept `workspace_id` as their first argument. Never silently default it.
- For container lifecycle: every container is labeled `app=data-toolbox-mcp`. Orphan detection filters on this label.
- **Dockerfile + manifest sync**: when you change `runtime/Dockerfile`, update `internal/runtime/manifest.go` in the same commit. The e2e manifest-drift test catches name-set mismatches but not silently mis-pinned versions.
- **matplotlib font order matters**: matplotlib 3.10's Agg backend renders all text with the first loadable font in `font.sans-serif`. `Noto Sans CJK JP` MUST be first (it covers Latin glyphs too, so no side effect on English).
- **`attach_files` does not Ensure the workspace**: it only reads from disk (no Podman). Calling it on a workspace_id that was never `Ensure`'d will just report missing files. This is by design — attach is a pure-host operation.
- **`load_from_work` requires `/work/` prefix**: bare relative paths or any other absolute path are rejected with `invalid_arguments`. Internally it strips `/work/` and resolves against `<host_work_dir>` with prefix re-check; never trust the `/work/` prefix alone for security.
- **Two ways to load a file**: `load_data` (host file → workspace via allowed_paths) vs `load_from_work` (sandbox file → table). Choose by where the file currently lives; they share the underlying script engine in `internal/tools/load_helpers.go` so behavior is identical post-load.

## Conventions (organization-wide)

See `../CLAUDE.md` and the organization [CONVENTIONS.md](https://github.com/nlink-jp/.github/blob/main/CONVENTIONS.md).

- Tests are mandatory; design for testability (pure functions, injected deps).
- Small, typed commits (`feat:` / `fix:` / `docs:` / `chore:` / `test:` / `refactor:`).
- README.md and README.ja.md update in the same commit as behavior changes.
- No secrets, no PII, no infra values (GCP project IDs, SA emails, tokens) ever committed.
