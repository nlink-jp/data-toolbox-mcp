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
| `internal/tools/` | `load_data` / `query_data` / `execute_code` implementations | D |
| `internal/config/` | config.toml + env-var loading | A/C |
| `_test/e2e/` | Dummy MCP client E2E harness | F |
| `docs/{en,ja}/` | RFP, ADRs, architecture, Phase 1 plan | Phase 0 |

## ADR cheat sheet

- **ADR-0001**: `workspace_id` is the explicit key for container + DuckDB scope. Validate as `^[a-zA-Z0-9_-]{1,64}$`.
- **ADR-0002**: Podman is fixed. No engine abstraction. Call `podman` via `exec.Command`.
- **ADR-0003**: Python is the only supported runtime. Reject `language != "python"` with `unsupported_language`.
- **ADR-0004**: stdio transport only. No HTTP/SSE in Phase 1.
- **ADR-0005**: Local-build distribution. Dockerfile lives at `runtime/Dockerfile` and is embedded via `go:embed` into the binary; `build-runtime` unpacks it and calls `podman build`.

## Gotchas

- macOS users must run `podman machine start` first (see memory `Podman Machine on macOS`).
- `network=none` is the default; this is also how Q5-4 (pip install) gets gated — flipping `network` to `bridge` is the user-facing knob.
- `query_data` auto-applies `LIMIT [query] default_row_limit` (= 20000) when SQL has no LIMIT. Don't strip user-supplied LIMITs.
- All tools accept `workspace_id` as their first argument. Never silently default it.
- For container lifecycle: every container is labeled `app=data-toolbox-mcp`. Orphan detection filters on this label.

## Conventions (organization-wide)

See `../CLAUDE.md` and the organization [CONVENTIONS.md](https://github.com/nlink-jp/.github/blob/main/CONVENTIONS.md).

- Tests are mandatory; design for testability (pure functions, injected deps).
- Small, typed commits (`feat:` / `fix:` / `docs:` / `chore:` / `test:` / `refactor:`).
- README.md and README.ja.md update in the same commit as behavior changes.
- No secrets, no PII, no infra values (GCP project IDs, SA emails, tokens) ever committed.
