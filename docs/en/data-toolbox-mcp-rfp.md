# RFP: data-toolbox-mcp

> Generated: 2026-06-05
> Status: Draft
> Author: magi
> Phase: Planning (Phase 1 of CONVENTIONS.md)

## 1. Problem Statement

`data-toolbox-mcp` extracts the LLM-independent capabilities of `shell-agent-v2` — its DuckDB analysis engine and containerized code execution sandbox — into an MCP server reusable from any MCP client (Claude Desktop, Cursor, etc.) over stdio. A single container with DuckDB built in serves as the shared execution substrate, and the server exposes three MCP tools: `load_data`, `query_data`, and `execute_code`. The target user is an individual developer or data analyst who wants to wire a personal agent on top of an MCP client but does not want to repeatedly stand up a data-analysis sandbox (DuckDB + Python runtime + host-container file plumbing).

## 2. Functional Specification

### Commands / API Surface

Three MCP tools are exposed:

| Tool | Arguments | Return |
|------|-----------|--------|
| `load_data` | `workspace_id: str`, `file_path: str`, `table_name: str` | `{rows_loaded: int, schema: object}` |
| `query_data` | `workspace_id: str`, `sql: str` | JSON array `[{col: val, ...}, ...]` + LIMIT warning (default LIMIT 20000, configurable via `[query] default_row_limit`) |
| `execute_code` | `workspace_id: str`, `language: "python"`, `code: str` | `{stdout: str, stderr: str, exit_code: int}` |

`workspace_id` is a string key explicitly chosen by the LLM/client. As long as the same `workspace_id` is supplied, the container and DuckDB file are reused.

### Input / Output

- **Transport**: MCP stdio (JSON-RPC over stdio)
- **`load_data` file_path**: Host absolute path. Only paths whitelisted under `allowed_paths` config are accepted. The MCP server reads the host file and supplies it to the container's working area.
- **`query_data` output**: JSON array `[{col: val, ...}, ...]`. Default LIMIT 20000 is auto-applied, with an explicit warning emitted when truncation occurs (configurable via `[query] default_row_limit`). The MCP server's baseline stance is to "return what was asked for, as faithfully as the channel allows"; streaming or file-handoff for huge results is left to the client-side agent implementation.
- **`execute_code` output**: stdout, stderr, exit_code.
- **Container-to-host artifacts**: The container's `/work` volume is mounted to `workspace_dir/<workspace_id>/work/` on the host, providing automatic two-way sync. The LLM can write `/work/foo.png` and read it back from the host.

### Configuration

`config.toml` with sectioned layout (per nlink-jp convention), overridable by environment variables.

```toml
[server]
log_level = "info"
log_file = "~/.data-toolbox/logs/server.log"

[workspace]
workspace_dir = "~/.data-toolbox"  # configurable
allowed_paths = ["~/data", "~/Downloads"]

[container]
image = "localhost/data-toolbox-runtime:latest"  # ADR-0005: local build

[container.limits]
cpu = "1.0"
memory = "2GB"
timeout_seconds = 60
network = "none"

[query]
default_row_limit = 20000  # query_data auto-LIMIT default
```

### External Dependencies

**Runtime (when running the MCP server)**:

- Zero external APIs / LLM / cloud — fully local
- Podman socket access (rootless)
- Host filesystem read/write (only `allowed_paths` and `workspace_dir`)

**Build time (when building the container image, on the end user's machine)**:

- Container registry (Docker Hub) for base image (python:3.13-slim) pull
- PyPI for duckdb / pandas / polars / pyarrow
- OS package manager (apt) for base OS packages

Per ADR-0005, this project does not push to any registry. Instead, end users run `make runtime-image` to build the image locally (local-build distribution). The first build needs network and a couple of minutes.

## 3. Design Decisions

**Language: Go**

- Aligns with mcp-guardian / nlk. Aim for zero external dependencies (Podman is driven via `exec.Command`; MCP SDK kept minimal).
- Single-binary distribution, easy cross-compilation.
- Rust was initially considered for performance/safety, but nlink-jp has no Rust precedent, the CONVENTIONS.md / Makefile templates are Go/Python-oriented, and maintainability concerns tipped the decision to Go.

**Container engine: Podman**

- Rootless, daemon-less (no systemd / Docker daemon required)
- On macOS, `podman machine` must be running (see External Constraints)

**Runtime: Python only**

- `duckdb / pandas / polars / pyarrow` bundled
- Scoped strictly to data analysis; Bash / Node / R are explicitly out of scope

**Lifecycle: Explicit `workspace_id` scoping**

- Departs from `shell-agent-v2`'s per-session DuckDB model.
- Pushing state-management explicitness onto the LLM/client buys multi-client coexistence and predictability.

**Relationship to existing nlink-jp tools**:

- **shell-agent-v2**: Could eventually be refactored to delegate to `data-toolbox-mcp`. That decision belongs to shell-agent-v2, not this project.
- **mcp-guardian**: Can be placed in front as a governance proxy (permissions / audit logs).
- **mcp-skeleton**: Reused as a stdio-MCP reference implementation.
- **cclaude**: Container operations Tips referenced.

**Distribution form: single binary + subcommands**

Following the `feedback_single_binary_subcommand` memory (proven by shell-agent-v2), `data-toolbox-mcp` is distributed as one Go binary, with subcommands separating concerns:

- `data-toolbox-mcp serve` — MCP stdio server (main mode; invocation with no arguments behaves the same)
- `data-toolbox-mcp build-runtime` — builds the runtime container image via Podman (the realization of ADR-0005's local-build distribution)
- `data-toolbox-mcp doctor` — environment diagnostics (Podman state, presence of the runtime image, config validation)
- `data-toolbox-mcp version` — show version

The runtime Dockerfile is embedded in the binary via `go:embed`, sparing users from managing a separate file. The MCP server, the build tool, and the setup diagnostic all ship as "one binary, one version," keeping consistency easy to manage.

**Explicitly out of scope**:

- HTTP / SSE transport (Phase 1 is stdio only)
- All LLM integration (the project intentionally stays LLM-agnostic)
- shell-agent-v2's 4-facility memory / System Rules / Global Memory
- Authn / authz (assumes personal machine + stdio; route through mcp-guardian when needed)
- GUI / Web UI
- Non-Python runtimes (R / Node / Bash)

## 4. Development Plan

### Phase 0: Design documentation (before implementation)

Following the "ADR before implementation" principle, complete and review the following before writing code:

- **ADR-0001**: workspace_id model and lifecycle
- **ADR-0002**: Choice of Podman (why a container-engine abstraction is deferred past Phase 1)
- **ADR-0003**: Python-only runtime decision
- **ADR-0004**: stdio-only (why HTTP/SSE is out of scope)
- **Architecture document**: `docs/{ja,en}/reference/architecture.md` (process boundaries, data flow, state transitions)
- **Development plan**: Phase 1-3 TODO breakdown with completion criteria per phase

### Phase 1: Core MVP (Security & Testability as Day-1)

- Podman wrapper (start/stop/exec, visible exit status)
- workspace_id-based state management (container ID + DuckDB file path persistence)
- MCP stdio server skeleton (referencing mcp-skeleton)
- Implementation of all three tools: `load_data` / `query_data` / `execute_code`
- config.toml + env var loading
- Runtime container Dockerfile (python + duckdb + pandas + polars + pyarrow)
- **Security baked in alongside the implementation**:
  - `allowed_paths` path-traversal / symlink defense
  - Resource limits (CPU / memory / timeout, network=none by default)
  - Forced container stop as a fallback on errors
  - Lifecycle management that guarantees no container leaks on failure paths
- **Automated E2E test harness via a dummy MCP client**:
  - JSON-RPC over stdio message-sequence verification
  - Full workspace_id lifecycle paths (init → load → query → execute → cleanup)
  - Error paths, timeouts, forced-container-stop scenarios
- Unit tests + Podman integration tests

**Phase 1 completion criteria**: All ADRs reviewed + test harness passes main scenarios + verified working in a Podman environment.

### Phase 2: UX polish & LLM-driven verification

- Auto LIMIT on `query_data` + large-result warnings
- Structured error messages (LLM-readable cause discrimination)
- Log rotation
- Final-mile real-world E2E on Claude Desktop / Cursor (LLM-driven scenarios that the dummy-client harness cannot reach)

### Phase 3: Release

- README.md / README.ja.md / CHANGELOG.md / AGENTS.md
- Release pipeline (`make build` / `build-all`, cross-compile)
- Umbrella submodule integration (util-series)
- Update nlink-jp/.github profile (per the "catalog sync" memory)

**Independence of phases**:

- Phase 0 is documentation-only and reviewable standalone.
- Phase 1 is an MVP that runs on its own.
- Phase 2 depends on Phase 1 completion.
- Phase 3 (release) depends on Phase 1 + 2.

## 5. Required API Scopes / Permissions

### Runtime (MCP server execution)

**External APIs: None** (zero LLM / cloud).

**Local permission requirements**:

- Host filesystem read (within `allowed_paths`)
- Podman socket access (rootless user permission)
- `workspace_dir` write

### Build time (container image construction)

**Network dependencies**:

- Container registry: base image (python) pull
- PyPI: duckdb / pandas / polars / pyarrow
- OS package mirror (apt / apk): base OS packages

Building happens in the project's release pipeline; end users only consume a pre-built image and need just `podman pull` privileges.

## 6. Series Placement

**Series: util-series**

**Rationale**:

- `shell-agent-v2` already lives in util-series; this project is its LLM-independent derivative, so the same series is the natural placement.
- util-series fundamentally is "pipe-friendly data transformation CLIs," but the "tool-provision" mindset of an MCP server is close enough to fit.
- lab-series suits exploratory work; since this project has a working predecessor (shell-agent-v2), it can sit in util-series from day one.

## 7. External Platform Constraints

### A. MCP protocol constraints

- Bound to MCP spec version (currently 2024-11-05 base). The protocol has no cancellation notification, so long-running cancellation is implemented via child-process kill (per the `MCP no protocol cancel` / `child process exit status` memories).
- stdio transport effectively caps message size — large `query_data` results are constrained via LIMIT.
- JSON Schema expressiveness for tool parameters is limited (avoid complex union types).
- When acting as a proxy/relay, never leave requests unanswered (per the `MCP proxy always responds` memory). This project is not a relay, but the same principle applies: container execution failures must still surface as JSON-RPC errors.

### B. Podman constraints (macOS-specific)

- `podman machine` must be running.
- Known issues: virtiofs socket unavailable, gvproxy port retention, VM OOM, sshd ENV propagation, `sed -i` portability (per the `Podman Machine on macOS` memory).
- Users must set up Podman in advance — instructions belong in the README.

### C. DuckDB constraints

- Memory-resident by default. For large data, configure disk spill via `memory_limit` PRAGMA.
- Only one writer per DuckDB file. This aligns with the workspace_id model (1 workspace = 1 container = 1 DuckDB writer).
- Sharing data across workspaces requires explicit parquet/csv export → `load_data`.

### D. Client-side configuration paths

- Claude Desktop: `claude_desktop_config.json` under `mcpServers`.
- Cursor: `.cursor/mcp.json` or via settings UI.
- This project only documents the binary path and config.toml path; automating client-side setup is out of scope.

---

## Discussion Log

### Naming

- Candidates: `agent-tools-mcp`, `shell-agent-tools`, `data-sandbox-mcp`, `duckbox-mcp`, user-supplied `data-toolbox-mcp`.
- Selected: `data-toolbox-mcp` (neutral, room to extend).

### Use case narrowing

- Scoped to "use from MCP clients like Claude Desktop / Cursor over stdio, on a personal machine."
- This filing decisively pushes HTTP/SSE transport and team hosting out of Phase 1 scope.

### Tool surface design

- Initial concept: "Build DuckDB into the container; container code drives the DB directly."
- Alternatives considered:
  1. Minimalist `execute_code`-only surface (everything via code)
  2. `load_data / query_data / execute_code` 3-tool surface (selected)
  3. `setup_workspace / execute_code` 2-tool surface
- Selected because it strikes a tractable middle for LLMs (implementation still shares the container and runtime internally).

### Lifecycle model

- Alternatives considered:
  1. 1 MCP server = 1 container = 1 DuckDB (lifetime-persistent)
  2. Per-MCP-session create-and-destroy (shell-agent-v2 lineage)
  3. **Explicit `workspace_id` scoping** (selected)
  4. DuckDB persistent on host, container ephemeral
- Selected to balance multi-client coexistence and predictability — trading state-management explicitness on the LLM side for simpler, more durable API.

### Container engine selection

- Alternatives considered:
  1. Docker
  2. **Podman** (selected)
  3. Multi-engine abstraction
  4. subprocess + chroot/jail (no container)
- Selected because rootless, daemon-less, and stronger security boundary.

### Implementation language selection

- Initial pick: Rust (performance / safety).
- Concerns raised: no Rust precedent in nlink-jp, CONVENTIONS.md / Makefile templates not in place, higher maintenance cost.
- Final decision: **Go** (same lineage as mcp-guardian, single binary, lightweight external deps).

### Development plan direction

- Made Phase 0 explicit: "Document ADRs / architecture before writing code."
- Pushing security into a later "Hardening" phase would conflict with the "security at implementation time" principle, so it folds into Phase 1.
- Adding the **dummy MCP client automated E2E harness** to Phase 1 secures testability without requiring an actual LLM in the loop.

### API scopes clarified

- Initially summarized as "no external APIs," but per user feedback, **runtime vs. build-time** dependencies were separated.
- Runtime: none. Build time: registry + PyPI + OS package mirror.

### Configuration items

- Selected: `allowed_paths`, `container_image`, `resource_limits`, `log_level`/`log_file`.
- `workspace_dir` was initially intended to be fixed but, per user feedback, is now configurable.

### Phase 0 Open Questions resolved (2026-06-05)

The Open Questions raised during Phase 0 documentation were resolved as follows:

- **Q5-1 (Podman macOS gvproxy)**: Default to `network=none`, avoiding the gvproxy path. Real-machine verification scheduled in Phase 1.
- **Q5-2 (large query streaming)**: Raise the default LIMIT from 1000 to **20000**, configurable via `[query] default_row_limit`. The MCP server's stance is "return what was asked for, as faithfully as the channel allows"; streaming and file-handoff for huge results are the client-side agent's responsibility.
- **Q5-3 (container image hosting)**: No registry push (ghcr / Docker Hub). Adopt local-build distribution. **A new ADR-0005 was created.**
- **Q5-4 (pip install in container)**: Let `network` config naturally split the behavior — `network=allow` lets the LLM run `pip install` freely inside the container, while `network=none` simply fails the pip connection. No fine-grained per-process ACL in Phase 1.
- **Q5-5 (DuckDB version pinning)**: Pin to the stable minor at the start of Phase 1 (`duckdb~=1.1` style). Pin `pandas`, `polars`, and `pyarrow` similarly.
