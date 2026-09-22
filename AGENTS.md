# AGENTS.md — data-toolbox-mcp

Navigation hints for AI agents (Claude Code, Cursor, etc.) working inside this project.

## What this project is

DuckDB + containerized Python execution exposed as a single-binary MCP server (stdio). LLM-independent extraction of shell-agent-v2's tool layer. Phase 1 in progress.

## Build / test

- `make build` — never `go build` directly (writes to `dist/`)
- `make test` — runs all Go unit tests
- `make test-linux` — the same suite inside a Linux container (podman/docker)
- `make runtime-image` — builds the Podman runtime image (wraps `data-toolbox-mcp build-runtime`)
- `make build-all` — cross-compile for darwin/linux × arm64/amd64
- `make verify-release` — gate: .notarized marker + freshness (run before upload)

Direct `go build` is **forbidden** by project convention; the wrapped form sets `-ldflags -X cmd.Version=...` from `git describe`.

## Project structure

| Path | Role | Phase 1 track |
|------|------|---------------|
| `main.go` | Entry point, delegates to cmd.Execute() | A |
| `cmd/` | cobra subcommands (root / serve / build-runtime / doctor / version; `--version` flag too — the org homebrew formula tests it) | A |
| `runtime/Dockerfile` | Source for the Python runtime container image (embedded via go:embed in Track E) | E |
| `internal/transport/` | MCP stdio JSON-RPC framing | B |
| `internal/jsonrpc/` | JSON-RPC 2.0 types | B |
| `internal/mcpserver/` | MCP protocol (initialize, tools/list, tools/call); `SetInstructions` fills the initialize `instructions` field | B |
| `internal/tools/instructions.go` | `tools.Instructions`, the initialize-time hint (purpose, work-dir contract, "call describe_runtime") | — |
| `internal/workspace/` | workspace_id-scoped Podman + DuckDB lifecycle | C |
| `internal/tools/` | 9 tools: `load_data` / `query_data` / `execute_code` + v0.2.0 `list_workspaces` / `delete_workspace` / `describe_runtime` + v0.3.0 `attach_files` / `load_from_work` + v0.4.0 `describe_workspace` | D, v0.2.0, v0.3.0, v0.4.0 |
| `internal/tools/load_helpers.go` | Shared `chooseReader` / `validateTableName` / `buildLoadScript` / `runLoadScript` used by `load_data` and `load_from_work` | v0.3.0 |
| `internal/tools/describe_workspace.go` | v0.4.0 `describe_workspace`: SHOW TABLES + DESCRIBE per table in one script | v0.4.0 |
| `internal/runtime/manifest.go` | Static manifest backing `describe_runtime`; in lock-step with `runtime/Dockerfile` | v0.2.0 |
| `internal/config/` | config.toml + env-var loading | A/C |
| `internal/logging/` | log_file + log_level wiring with startup rotation | Phase 2 |
| `internal/toolerr/` | structured `{code, message, details}` tool errors | Phase 2 |
| `e2e/` | Dummy MCP client E2E harness (build tag `e2e`) | F, v0.2.0 |
| `docs/{en,ja}/` | RFP, ADRs (0001-0010), architecture, phase1-plan, v0.2.0-plan, v0.3.0-plan, v0.4.0-plan | Phase 0, v0.2.0, v0.3.0, v0.4.0 |

## ADR cheat sheet

- **ADR-0001**: `workspace_id` is the explicit key for container + DuckDB scope. Validate as `^[a-zA-Z0-9_-]{1,64}$`.
- **ADR-0002**: Podman is fixed. No engine abstraction. Call `podman` via `exec.Command`.
- **ADR-0003**: Python is the only supported runtime. Reject `language != "python"` with `unsupported_language`.
- **ADR-0004**: stdio transport only. No HTTP/SSE in Phase 1.
- **ADR-0005**: Local-build distribution. Dockerfile lives at `runtime/Dockerfile` and is embedded via `go:embed` into the binary; `build-runtime` unpacks it and calls `podman build`.
- **ADR-0006**: `list_workspaces` (no args) + `delete_workspace` (workspace_id) + `describe_runtime` (no args). Disk is the truth source for list/delete; describe_runtime returns the `internal/runtime.Default` manifest merged with the live `network` setting.
- **ADR-0007**: Runtime image is `python:3.12-slim` + `fonts-noto-cjk` + matplotlib + Pillow with `Noto Sans CJK JP` first in `font.sans-serif` (matplotlib Agg has no per-glyph fallback). Image budget < 900MB.
- **ADR-0008**: `attach_files` returns workspace `/work` files as MCP image/text/metadata content blocks. Extension-based dispatch, per-file 10 MiB / cumulative 20 MiB caps (configurable via `[attach]`), path-traversal defense-in-depth.
- **ADR-0009**: `load_from_work` table-izes a `/work/<sub>` file directly (the file is already in the sandbox). `file_path` must start with `/work/`.
- **ADR-0011**: Every tool except `describe_runtime` takes a required `work_dir`; the workspace is `<work_dir>/<workspace_id>/`, so `host_work_dir` is a path the caller can open. `workspace_dir` and `allowed_paths` are removed — `load_data` is guarded by a fixed credential blacklist, and the container name carries a digest of the work dir.
- **ADR-0010** (v0.4.0): UX polish — `describe_workspace` (table+columns), `query_data` returns `truncated/total` + table-not-found hint in `details`, `delete_workspace` accepts `dry_run: true` for preview, four tool descriptions gain a one-line hint.

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
- **Two ways to load a file**: `load_data` (host file → workspace, blacklist-guarded) vs `load_from_work` (sandbox file → table). Choose by where the file currently lives; they share the underlying script engine in `internal/tools/load_helpers.go` so behavior is identical post-load.
- **`query_data.total` runs an extra COUNT only on truncation**: when `truncated=true`, an additional `SELECT COUNT(*) FROM (user_sql) sub` runs to fill `total`. Non-truncated queries pay nothing extra. If the COUNT itself times out, `total: null` + `total_unavailable_reason: "count_timed_out"`.
- **A workspace is the pair (work_dir, id), and each spelling of that pair has one owner**: `workspaceKey` for the manager's cache (reached only through `lookup` / `remember` / `forget`), `containerName` for the podman name, `exactName` for the `ps --filter`. Release and Delete used to evict by bare id and Delete looked for the pre-ADR-0011 name, so a deleted workspace kept answering with a dead container until the server restarted. `TestWorkspaceIdentityIsSpelledOnce` fails on a second spelling; its positive control is `TestIdentityCheckFindsEachSecondSpelling`.
- **Podman's `name=` filter is an unanchored regex**: `name=data-toolbox-mcp-gamma` also matches `…-gamma2-…` and the same id under another work directory (measured on podman 6.1.2). Two hits come back as a two-line "ID". Always go through `exactName`. The unit fake in `identity_test.go` matches the way podman does; the older canned-reply fake in `manager_test.go` cannot see a wrong name.
- **Real-podman tests are opt-in**: `DATA_TOOLBOX_TEST_PODMAN=1 DATA_TOOLBOX_TEST_IMAGE=localhost/data-toolbox-runtime:latest go test -run TestIntegration ./internal/workspace/` (about a minute). They are the only check that the name filter means what `exactName` says.
- **Workspace files are reached through an `os.Root`, never by joining a path**: `/work` is writable by sandboxed code, so `_upload`, `_code` and any file under them may be a symlink it left behind. `attach_files` read the host file a link pointed at (past the credential blacklist), and `load_data` / `execute_code` wrote through one. Reads go through `root.Stat` / `root.ReadFile`, writes through `writeInWork`, and the root itself comes from `openWorkRoot`, which reaches `<id>/work` through a root on `work_dir` — a caller that names a `work_dir` inside another workspace's `/work` lets sandboxed code plant `<work_dir>/<id>` too, which is also why `Manager.Ensure` (`makeWorkDir`) refuses a linked workspace directory before podman mounts it. `attach_files` rejects what is not a regular file: a FIFO named `x.txt` would block the read, and the server answers one request at a time. `TestWorkspaceFilesGoThroughARoot` is an allow-list of what `internal/tools` may use from `os` and `path/filepath` (alias-aware; `os.OpenRoot` only in `openWorkRoot`, `os.Open` only in `stageUpload`, whose HOST source file `ResolveInput` guards). A link planted at `/work/_code` or `/work/_upload` makes the workspace refuse to run until it is removed on the host — failing closed is intended, and the error says what is in the way. `attach_files` still does not Ensure: a never-ensured workspace reports `missing`.
- **The initialize instructions are a claim about the tools, and tests hold them to it**: `tools.Instructions` (`internal/tools/instructions.go`) is set by `newServer` in `cmd/serve.go`, the only place the `cmd` package constructs an `mcpserver.Server` (`TestServeBuildsItsServerOnlyThroughNewServer` walks the syntax trees; `TestServedServerAnswersInitializeWithTheInstructions` drives an initialize through it). Every snake_case word in the text must be a registered tool or a declared argument, and the "Every tool except … takes work_dir" sentence must name exactly the tools whose schema has no `work_dir` — so adding a tool without `work_dir`, or renaming one the text mentions, fails `make test` until the text is updated.
- **`delete_workspace` dry_run path is non-destructive**: `dry_run: true` runs `Manager.PreviewDelete` which only reads (podman ps + filepath.Walk for disk_usage_bytes). Verify nothing in the dry_run path calls `os.RemoveAll` or `podman rm`.
- **Whether a file exists never changes the answer.** `ResolveInput` (load_data) places the path first (`workdir.Where`, the last of pathguard's forms: every link followed, a dangling one by its target) and judges it there, as given and as placed, before `EvalSymlinks(where)` asks whether it exists; `attach_files` judges each path at its place before `root.Stat`. A missing credential file used to answer `invalid_arguments` and an existing one `path_not_allowed`, and `attach_files` returned a `.env` or the target of a `~/.ssh` link lying in `/work`. Do not give a path that does not resolve a branch of its own, and do not stat before the floor (ADR-0012, amendment v0.8.1). `TestExistenceIsNotRevealedByLoadData` and `…ByAttachFiles` compare the whole answer for a path with and without its file; the mutations of the order are all caught by assertion. The floor on `attach_files` is not a boundary for `/work`: `execute_code` can read everything there, so a workspace that *contains* a protected place (a sync folder a link in `~/.ssh` leads into) is still exposed to the sandbox.
- **The work-directory resolver is constructed in exactly one place**: `workDirResolver()` in `internal/tools/workdir.go`, reached only through `resolveWorkDir`, which every tool calls. Eight tools used to write `workdir.Resolver{}` each, and all eight therefore ran with an empty `Denied` list — a caller could name this server's own config directory as its `work_dir` and get `load_data` to bind-mount it into a container, `execute_code` to run Python with it writable and `delete_workspace` to remove a subtree of it. It is now `workdir.NewResolver(serverOwnedDirs()...)`, protecting `config.Dir()` = `~/.config/data-toolbox-mcp` (organization ADR-021 §4), the same expression `serve` and `doctor` search through `config.SearchPaths()`; the judgement itself is nlink-jp/pathguard's (ADR-0012) — do not add a location list or a name comparison here. There is no state directory: ADR-0011 moved workspaces under the caller's `work_dir` and the runtime image lives in podman's storage. `TestOnlyOnePlaceConstructsAResolver` walks the package's syntax trees and fails unless `workdir.go` holds the one `workdir.NewResolver` call and no `workdir.Resolver` literal exists anywhere (the zero value refuses every call), so the class is closed rather than its eight instances. The workspace directory is judged too: `workspace.NewManager(cfg, podman, check)` takes `tools.WorkspaceCheck` (wired once in `newWorkspaceManager` in `cmd/serve.go`), and `Ensure`, `PreviewDelete` and `Delete` judge `<work_dir>/<workspace_id>` before mounting, loading from or deleting it — `work_dir=~/.config` with `workspace_id=gh` is `~/.config/gh`. A Manager without a check refuses every workspace.

## Conventions (organization-wide)

See `../CLAUDE.md` and the organization [CONVENTIONS.md](https://github.com/nlink-jp/.github/blob/main/CONVENTIONS.md).

- Tests are mandatory; design for testability (pure functions, injected deps).
- Small, typed commits (`feat:` / `fix:` / `docs:` / `chore:` / `test:` / `refactor:`).
- README.md and README.ja.md update in the same commit as behavior changes.
- No secrets, no PII, no infra values (GCP project IDs, SA emails, tokens) ever committed.
