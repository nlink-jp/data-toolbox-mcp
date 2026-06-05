# Phase 1 Development Plan: data-toolbox-mcp

> Status: Draft (Phase 0)
> Date: 2026-06-05

Building on ADR-0001 through ADR-0005 and the architecture document (`architecture.md`) finalized in Phase 0, this plan breaks Phase 1 (Core MVP) work into tracks that can be started in parallel as much as possible.

## 1. Goals (Phase 1 completion criteria)

Transcribed from RFP §4 Phase 1. Phase 1 is complete only when all of the following hold:

- All three tools (`load_data` / `query_data` / `execute_code`) work
- workspace_id-based state management works against a Podman environment
- `allowed_paths` traversal defense + resource limits + forced container-stop fallback are integrated
- The dummy MCP client automated E2E harness covers the main scenarios
- Unit tests + Podman integration tests (skippable with `DATA_TOOLBOX_TEST_PODMAN=1`) pass
- Phase 0 ADRs have been updated to Status: Accepted

## 2. Work breakdown (per track)

### Track A: Repository scaffold + subcommand skeleton

- nlink-jp CONVENTIONS.md-compliant layout (per ADR-0005, single binary + subcommands)
  ```
  _wip/data-toolbox-mcp/
  ├── main.go
  ├── cmd/
  │   ├── root.go          # cobra root
  │   ├── serve.go         # MCP stdio server (default when no argument)
  │   ├── build_runtime.go # build the runtime image
  │   ├── doctor.go        # environment diagnostics
  │   └── version.go       # version display
  ├── runtime/             # source for go:embed (Dockerfile and helpers)
  │   └── Dockerfile
  ├── internal/...
  ├── Makefile             # make build → dist/; make runtime-image wraps build-runtime
  ├── go.mod
  ├── config.example.toml
  ├── README.md
  ├── README.ja.md
  ├── CHANGELOG.md
  ├── AGENTS.md
  └── docs/                # already created in Phase 0
  ```
- Go module initialization (`go mod init github.com/nlink-jp/data-toolbox-mcp`)
- Makefile targets: `build / build-all / test / clean / runtime-image`
- `.gitignore` (only `dist/`, no binary-name patterns — memory `feedback_gitignore_binary_pattern`)
- At this stage the four subcommands are stubs with bare wire-up; the real implementation lives in the other tracks

**DoD**: `make build` produces `dist/data-toolbox-mcp`; `data-toolbox-mcp version` works; `data-toolbox-mcp --help` lists four subcommands; README.md and README.ja.md scaffolds exist.

### Track B: MCP stdio framework

- `internal/transport/stdio.go` — reuse mcp-guardian's `internal/transport/process.go` pattern (`bufio.Scanner(1MB)`)
- `internal/jsonrpc/` — JSON-RPC 2.0 types (reference mcp-guardian's `internal/jsonrpc/`)
- `internal/mcpserver/` — MCP protocol-level handling:
  - `initialize` / `initialized`
  - `tools/list`
  - `tools/call`
- Per the `feedback_mcp_proxy_always_responds` memory, every request must produce a JSON-RPC response

**DoD**: With an empty tool set, a dummy client calling `initialize` → `tools/list` → `tools/call` gets a coherent echo back.

### Track C: workspace + Podman lifecycle manager

- `internal/workspace/manager.go` — `WorkspaceManager` struct with `ensure(workspace_id)`, `release(workspace_id)`
- `internal/workspace/podman.go` — thin wrappers around `podman run / exec / stop / rm / ps` (referencing shell-agent-v2's `internal/sandbox/cli.go`)
- workspace_id validation (`^[a-zA-Z0-9_-]{1,64}$`)
- Disk state layout (`<workspace_dir>/<ws>/{analysis.duckdb, work/}`) creation and detection
- Orphan container detection (filter by label `app=data-toolbox-mcp`)
- Resource-limit flag assembly (`--cpus`, `--memory`, `--network=none`, `--label`)

**DoD**: `ensure("foo")` is idempotent; disk and Podman state sync with in-memory state; integration test (`_test/integration/workspace_test.go`) passes a round-trip.

### Track D: Implementation of the three tools

Start after B + C are ready.

- `internal/tools/load_data.go`
  - `allowed_paths` whitelist + `filepath.EvalSymlinks` check
  - Host → `<workspace_dir>/<ws>/work/_upload/` copy
  - `podman exec` driving Python to load into DuckDB
  - Return value: `{rows_loaded, schema}`
- `internal/tools/query_data.go`
  - Auto-append LIMIT (default 1000) when not present
  - `podman exec` driving Python to run the SQL
  - Return value: JSON array + LIMIT-reached warning
- `internal/tools/execute_code.go`
  - Validate `language == "python"`
  - Write code to `<workspace_dir>/<ws>/work/_code/<uuid>.py`, run with `podman exec python`
  - Forced container stop on timeout
  - Return value: `{stdout, stderr, exit_code}`
- Tool JSON Schemas consolidated in `internal/mcpserver/tool_definitions.go`

**DoD**: Each tool has unit + integration tests. Timeout cases must verify that containers don't leak.

### Track E: Runtime container image + build-runtime subcommand

Per ADR-0005, the Dockerfile is embedded in the binary via `go:embed`, and the build is driven by the `data-toolbox-mcp build-runtime` subcommand.

- `runtime/Dockerfile` (with version pinning, reflecting ADR-0003 / Q5-5 resolution):
  ```dockerfile
  FROM python:3.13-slim
  RUN pip install --no-cache-dir \
        duckdb~=1.1 \
        pandas~=2.2 \
        polars~=1.8 \
        pyarrow~=18.0
  RUN useradd -m -u 1000 toolbox
  USER 1000:1000
  WORKDIR /work
  CMD ["sleep", "infinity"]
  ```
  Pinned versions are re-confirmed and bumped to the latest stable minor at the start of Phase 1.
- `runtime/` is the source directory for `go:embed`. Add helper files (e.g. pip config) here if needed
- `cmd/build_runtime.go` implementation:
  1. Unpack the embedded Dockerfile into a temp directory
  2. Run `podman build -t localhost/data-toolbox-runtime:vX.Y.Z -t localhost/data-toolbox-runtime:latest <tempdir>` via `exec.Command`
  3. Stream progress to stdout (pass podman's stdout through)
  4. Remove the tempdir on completion
- Image tags: `latest` + `vX.Y.Z` (matches the binary's version)
- `make runtime-image` is a developer-facing wrapper that invokes `dist/data-toolbox-mcp build-runtime`
- No registry push (ADR-0005)

**DoD**:
- `dist/data-toolbox-mcp build-runtime` produces the image locally
- Image size is under 700MB
- `podman run --rm localhost/data-toolbox-runtime:latest python -c "import duckdb; print(duckdb.__version__)"` works
- `data-toolbox-mcp doctor` can detect whether the runtime image is present (combined A + E)

### Track F: Dummy MCP client test harness

Start after D.

- `_test/e2e/harness.go` — Go test driver that spawns the MCP server binary and interacts via JSON-RPC
  - Adapts mcp-guardian's `internal/proxy/proxy_test.go` shell-mock structure to handle richer interactions
- `_test/e2e/scenarios/` — per-scenario test files
  - `lifecycle_test.go`: init → load → query → execute → cleanup
  - `errors_test.go`: unsupported_language, path_not_allowed, workspace_id_invalid
  - `timeout_test.go`: timeout → forced container stop → recovery
  - `concurrency_test.go`: concurrent access on the same workspace, second caller rejected

**DoD**: All four scenarios pass; `go test ./_test/e2e/...` runs when `DATA_TOOLBOX_TEST_PODMAN=1` is set.

## 3. Dependencies between tracks

```
A (scaffold) ──┬──▶ B (mcp framework) ──┐
               ├──▶ C (workspace mgr) ──┼──▶ D (3 tools) ──▶ F (e2e harness)
               └──▶ E (runtime image) ──┘
```

- A is the first step. Everything else depends on it
- B / C / E can be started in parallel after A
- D starts when B + C + E are at a "workable" level
- F starts once D is broadly working

## 4. Definition of Done per track

In addition to per-track criteria (above), in common:

- Both Japanese and English documentation updates (README, related ADRs, architecture)
- Per the `feedback_commit_discipline` memory, commit when a track completes (one track ≈ one PR)
- Per the `feedback_make_build` memory, no direct `go build` — `make build` only

## 5. Open questions — resolved (2026-06-05)

The Open Questions raised during Phase 0 documentation are now resolved as follows:

### Q5-1. Podman macOS gvproxy issue scope — Resolved

**Policy**: Default to `network=none`, avoiding the gvproxy path. Real-machine verification happens during Track C / F implementation; if issues arise with non-`network=none` settings, file a separate ADR.

### Q5-2. Streaming for large query results — Resolved

**Policy**: Raise default LIMIT from 1000 to **20000**, configurable via `[query] default_row_limit`. The `query_data` baseline stance is "return what was asked for, as faithfully as the channel allows"; streaming and file-handoff for huge results are the client-side agent's responsibility. Phase 1 does not implement streaming APIs.

### Q5-3. Container image hosting location — Resolved

**Policy**: No registry push. The `data-toolbox-mcp build-runtime` subcommand combined with a `go:embed`-bundled Dockerfile delivers local-build distribution (**ADR-0005** created).

### Q5-4. Allowing additional package installs (`pip install`) — Resolved

**Policy**: Let `network` config naturally split the behavior. With `network=none` (default), in-container `pip install` fails because connections are blocked. Switch `network` to another value (e.g. `bridge`) and LLM-generated code can run `pip install` freely — that choice is the user's responsibility. Phase 1 does not implement fine-grained ACL like "allow only pip."

### Q5-5. DuckDB version pinning — Resolved

**Policy**: Pin to the stable minor at the start of Phase 1 (`duckdb~=1.1` style). Pin `pandas`, `polars`, and `pyarrow` similarly (see Track E Dockerfile). Re-confirm pin values against the latest stable minor at Phase 1 kickoff.

## 6. Reference reuse map

Direct reuse from existing nlink-jp code:

| Purpose | Source | Reuse approach |
|---------|--------|----------------|
| DuckDB Python access design reference | `util-series/shell-agent-v2/app/internal/analysis/engine.go` | Pattern reference (write equivalent inside Python) |
| Podman CLI wrapper design | `util-series/shell-agent-v2/app/internal/sandbox/cli.go` | Structural reference; direct `exec.Command` calls reused |
| /work mount + per-session directory structure | shell-agent-v2 `internal/sessionio` | Direct reference (same layout) |
| stdio 1MB buffering + JSON-RPC | `util-series/mcp-guardian/internal/transport/process.go` | Direct port |
| Shell-mock test pattern | `util-series/mcp-guardian/internal/proxy/proxy_test.go` | Reuse (extend to richer interactions) |
| BurntSushi/toml + env-vars config | `util-series/data-agent/internal/config/config.go` | Direct copy |
| Sandbox integration test structure | `util-series/shell-agent-v2/app/internal/sandbox/integration_test.go` | Reuse |

## 7. Estimated effort (rough)

Relative effort per track (relative comparison, not hours):

- Track A (scaffold): S
- Track B (mcp framework): M (lightened by mcp-guardian reuse)
- Track C (workspace + Podman): L (path-traversal defense, idempotency, orphan detection are quietly heavy)
- Track D (3 tools): M (each tool is an adapter once B + C exist)
- Track E (runtime image): S
- Track F (e2e harness): M-L (depending on scenario coverage)

Review checkpoints: end of A, end of B+C+E, end of D, end of F = Phase 1 complete.

## See also

- ADR-0001: workspace_id scoping and lifecycle
- ADR-0002: Podman without abstraction
- ADR-0003: Python-only runtime
- ADR-0004: stdio-only transport
- `architecture.md`: overall architecture
- `_wip/data-toolbox-mcp/docs/en/data-toolbox-mcp-rfp.md`: full RFP
