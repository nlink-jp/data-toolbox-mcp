# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.8.1] - 2026-09-22

### Security

- **Whether a file exists no longer changes the answer.** A `load_data`
  `file_path` in a credential or agent-control location was refused when the
  file was there and answered `invalid_arguments` when it was not, so the
  answer told the caller which secrets exist. The path is now judged at the
  place it leads to before anything looks for a file, and a refused place gets
  the same answer, message and details, either way (ADR-0012, amendment).
- `attach_files` applies the same floor to each path under `/work`: a `.env`
  there, or the file a link in `~/.ssh` leads to when the workspace lies in
  that sync folder, is rejected — it was returned when present: inline for a
  text or image extension, otherwise its size, time and hash.
- A `load_data` refusal names the path only as given: `details.resolved` is
  gone. It named where the path leads, which differed when an entry on the way
  is a link, and so said which entries exist and where they lead.

## [0.8.0] - 2026-09-22

### Changed

- **Path judgement moved to [nlink-jp/pathguard](https://github.com/nlink-jp/pathguard)**
  (ADR-0012). `internal/workdir` is now an adapter onto it; the resolver is
  built with `workdir.NewResolver(serverOwnedDirs()...)`. Places are compared by
  file identity and by names folded the way the disk folds them, instead of by
  name.
- `load_data` now **refuses** the real places under your home from the list
  gem-agent and lagent use — newly `~/.kube`, `~/.config/gh`, `~/.azure`,
  `~/.terraform.d`, `~/.gemini`, `~/.config/mcp-bridge`, `~/.netrc`, `~/.npmrc`,
  `~/.pypirc`, `~/.git-credentials`, `~/.vault-token`, `~/.docker/config.json`,
  `~/.claude.json`, `~/.bash_history`, `~/.zsh_history` — every spelling of any
  refused place (another case, a link, a firmlink), and wherever a link
  directly inside one of those directories points. When `$HOME` names another
  directory than the account's home, both are protected. Linux `/etc` is
  refused as a `work_dir`.
- `load_data` now **accepts** `.env.example`, `.env.sample`, `.env.template` and
  `.env.dist` (templates, not secrets).
- When the home directory cannot be determined, `load_data` paths and every
  `work_dir` are **refused**; they used to pass unchecked.
- `work_dir_denied` carries `reason` in its `details`.

### Security

- **The workspace directory is judged, not only `work_dir`.** `work_dir=~/.config`
  with `workspace_id=gh` made the workspace `~/.config/gh`: `load_data` and
  `execute_code` mounted it into the container as `/work`, so `load_from_work`,
  `query_data` or a script could read a credential file there and return it, and
  `delete_workspace` could delete it. `<work_dir>/<workspace_id>` is now refused
  with `work_dir_denied` wherever `work_dir` itself would be, before the manager
  mounts, loads from or deletes anything. The hole was present since the
  work-directory contract (ADR-0011).
- A path holding a NUL byte is refused (pathguard v0.2.0).

## [0.7.0] - 2026-09-22

### Added

- **The server now sends MCP `instructions` at initialize.** Clients hand this
  text to their model before any tool list. It says what the server is for,
  that every tool except `describe_runtime` takes a required `work_dir` (an
  absolute path the caller can read back, no default, must already exist),
  that the workspace is `<work_dir>/<workspace_id>/` and holds everything the
  server writes, including the DuckDB file and what `execute_code` writes to
  `/work`, and to call `describe_runtime` once at session start. Tests pin
  each claim against the registered tools: the contract terms, the tools that
  take no `work_dir`, that every tool or argument name it uses exists, that no
  retired work-dir name appears, and that the served binary actually sets it.

### Fixed

- **`make verify-release` now fails closed.** Its last block chained unzip, the
  packaged binary's `--version` and `spctl` with `&&` and ended the whole chain
  in `|| true`, so a zip that did not unpack or a binary that did not run exited
  0 and the upload proceeded. Each step is now judged on its own, the packaged
  binary's `--version` must contain the tag being released, and only the
  informational `spctl` line may be ignored. Matches the org template
  (CONVENTIONS.md §Code Signing → Verifying a release).

### Tests

- The per-tool contract tests fail when no tool is registered. They loop over
  the registered tools, and with an empty list every one of them passed without
  examining anything.

### Documentation

- The README's tool list gives each tool's `work_dir` argument (every tool but
  `describe_runtime` requires it), and its feature list says nine tools, not three.

## [0.6.5] - 2026-09-21

### Security

- **`work_dir` may no longer be this server's own config directory.**
  Organization ADR-021 §4 closes the work-directory checks with "not a system
  location … and not the server's own config or state directory" →
  `work_dir_denied`, and the resolver has carried a `Denied` list for exactly
  that — but each of the eight work-directory-taking tools built its own
  `workdir.Resolver{}`, so all eight ran with the list empty. A caller could
  pass `work_dir = ~/.config/data-toolbox-mcp` and get `load_data` to
  bind-mount that directory into a container, `execute_code` to run arbitrary
  Python with it writable, and `delete_workspace` to remove a subtree of it —
  against the file that sets this server's own container limits, on a model's
  say-so. `~/.config/data-toolbox-mcp` and everything under it is now refused.
- The eight literals are gone: every tool now calls one `resolveWorkDir`, and
  `TestOnlyOnePlaceConstructsAResolver` walks the package's syntax trees and
  fails on a `workdir.Resolver` literal written anywhere else, so the next
  tool cannot reintroduce the defect by copying its neighbour. The denied path
  comes from `config.Dir()`, the same expression `serve` and `doctor` search
  through `config.SearchPaths()` — one spelling instead of the three that
  existed.

## [0.6.4] - 2026-09-21

### Fixed

- **A deleted workspace could not be used again until the server restarted.**
  `delete_workspace` removed the directory, but the manager kept the workspace's
  cached handle — it stored handles under (work_dir, id) and evicted them by bare
  id, so nothing was ever evicted. The next call with the same `workspace_id`
  was answered from the cache: no container was started, the work directory was
  not recreated, and every `execute_code` / `query_data` ran against a container
  that no longer existed. Releasing a workspace had the same flaw.
- **`delete_workspace` looked for the wrong container**, by the name used before
  the work directory became part of a workspace's identity (0.6.0). Podman reads
  `--filter name=` as an unanchored regular expression, so the old name still
  matched — along with the same `workspace_id` under any other work directory
  and any longer id that starts the same way (`gamma` matched `gamma2`). With one
  wrong match a delete force-removed another workspace's container; with several,
  the delete failed on a multi-line "ID". `dry_run` reported the same wrong
  container. The name is now derived in one place and matched exactly.
- **`attach_files` followed a symlink out of the workspace.** Its path check was
  lexical, and `/work` is writable by the code `execute_code` runs: a link left
  there was followed on the host, and the target's content — or, for an
  unrecognised extension, its size, mtime and SHA-256 — was returned. That went
  past the credential blacklist `load_data` applies to the same kind of path.
  The server's own writes had the mirror-image flaw: `load_data` (`_upload/`)
  and `execute_code` (`_code/`) created their files by path, so a linked
  directory or a link planted at the destination redirected the write to the
  host, overwriting the target. All of these now go through an `os.Root` on the
  work directory, which refuses a path that leaves it; links that stay inside
  `/work` keep working. Requires Go 1.25 to build.
- The same root is opened through a root on `work_dir` rather than by its own
  path, and `Ensure` refuses a workspace whose directory is a link before podman
  mounts it: a caller that names a `work_dir` inside another workspace's `/work`
  let sandboxed code plant the workspace directory itself.
- `attach_files` blocked forever on a FIFO named like a text file, and the
  server answers one request at a time. Anything that is not a regular file is
  rejected.
- `samples/README.md` told readers to add `[workspace] allowed_paths` to
  `config.toml` — a key the server has refused to start with since 0.6.0. The
  tool tables in both READMEs listed `work_dir` for one tool out of eight, and
  the English architecture reference still said `workspace_dir`.

### Added

- Tests that ask a podman which remembers its containers, instead of one that
  answers every lookup with the same id — the reason the above stayed green —
  plus an opt-in run of the same question against the real podman
  (`DATA_TOOLBOX_TEST_PODMAN=1`), and a source check that keeps a second
  spelling of a workspace's identity from coming back.
- Tests that plant the links sandboxed code could plant, and a source check
  that refuses a path-based file call in `internal/tools`. Both source checks
  carry a positive control.

## [0.6.3] - 2026-09-14

### Fixed

- **`describe_workspace` required `work_dir` without declaring it**, and a
  strict client refuses the whole tool list for that: Vertex AI answers
  `tools/list` with "schema at top-level requires unspecified property
  'work_dir'" and the session cannot start at all (gem-agent, 2026-09-14). The
  property is declared, from the same shared snippet every other tool uses.

### Added

- `TestEveryRequiredNameIsDeclared` — the existing contract test checked
  declared ⇒ required and was blind to the other direction; JSON Schema permits
  it, so nothing else caught it. Added to every server in the fleet.

## [0.6.2] - 2026-09-14

### Fixed

- The RFP's Input/Output section still described `load_data` as whitelisted by
  `allowed_paths` and the artifact mount as `workspace_dir/...`. Both are
  annotated with the revision that overtook them, in either language, instead
  of reading as current. (`docs/*/reference/phase1-plan` is left alone: it is
  dated and marked a Phase 0 draft.)

## [0.6.1] - 2026-09-13

### Fixed

- **`describe_runtime`'s artifact-exchange note still gave the host path as
  `<workspace_dir>/<workspace_id>/work/<name>`** — a config key 0.6.0 removed
  and now refuses to load. It names the `work_dir` the call passed, which is
  where the workspace actually is.
- Two comments still described `load_from_work` as bypassing `allowed_paths`;
  there is no allowlist to bypass.
- **README.ja was left behind by 0.6.0**: its security model still said
  `load_data` is restricted to `allowed_paths`, and `list_workspaces` was still
  documented as taking no arguments. It says what the English one says.

### Added

- `TestManifestNamesNoRetiredKey` / `TestArtifactExchangeNoteNamesWorkDir` —
  the manifest goes to the model verbatim, so a retired name in it is a defect
  a test should catch.
- A contract test walking every registered tool: no retired name in a schema or
  a description, and `work_dir` required wherever it is declared. ADR-0011
  asked for this test and the release shipped without it.
- `make test` now also type-checks the `e2e` suite, which `go test ./...` never
  builds.

## [0.6.0] - 2026-09-13

### Changed

- **Breaking: every tool takes a required `work_dir`, and workspaces live under
  it.** A workspace is `<work_dir>/<workspace_id>/`, so the `host_work_dir` a
  result reports — where `execute_code`'s files land — is **a path the caller can
  open**. Until now it was under the server's own `~/.data-toolbox`, which no
  calling agent's file tools can read; `attach_files` returning content inline is
  the reason that was survivable, not the design saying so. See
  [ADR-0011](docs/en/adr/0011-work-dir-contract.md); organization ADR-021.
- **Breaking: `workspace.workspace_dir` and `workspace.allowed_paths` are
  removed**, and a config still carrying either fails at startup with the reason
  named. The allowlist could not express what it was for: prefix matching has no
  per-repository granularity, so covering a work root meant naming the home
  directory, which admits the credential files the list existed to keep out.
- `load_data` reads any file you can read, except a fixed in-code blacklist of
  credential and agent-control locations (`~/.ssh`, `~/.aws`, `~/.gnupg`,
  `~/.config/gcloud`, `~/Library/Keychains`, `~/.claude`, `~/.codex`, any
  `.env`), checked on the path as given and on its symlink-resolved form. It is
  a floor, not a boundary.
- A runtime may supply the directory instead of the model: the server reads
  `_meta["jp.nlink/work_dir"]` when the argument is absent. The argument wins.
- Container names now carry a digest of the work directory: the same
  `workspace_id` under two work directories is two workspaces, and one
  long-lived container cannot serve both. Containers named
  `data-toolbox-mcp-<id>` from earlier versions are not reused — remove them with
  `podman rm` if they are not wanted, and workspaces under `~/.data-toolbox` are
  left on disk but no longer referenced.

### Added

- `work_dir_required`, `work_dir_invalid`, `work_dir_not_found`,
  `work_dir_not_writable`, `work_dir_denied` — the fleet's codes for the part of
  the contract that failed.

## [0.5.2] - 2026-08-31

### Fixed

- **`list_workspaces` no longer reports directories that are not workspaces.**
  The shipped config nests the log directory inside `workspace_dir`
  (`log_file = "<workspace_dir>/logs/server.log"`), and `logs` passes the id
  pattern, so it was listed as a workspace with a `host_work_dir` that does not
  exist. Since the tool tells agents to use it to "discover prior workspaces",
  an agent could pick that phantom and have `execute_code` create `work/` and
  `analysis.duckdb` inside the log directory. A workspace is now recognised by
  the `work/` directory `Ensure` always creates, not by its name alone — an
  operator may nest anything under `workspace_dir`, and the listing must not
  assume otherwise.

## [0.5.1] - 2026-07-26

### Fixed

- **`--version` now works.** The binary only implemented a `version`
  subcommand, so `data-toolbox-mcp --version` failed with "unknown flag" — and
  the shared org homebrew formula template tests exactly that invocation, so
  `brew test data-toolbox-mcp` failed. `rootCmd.Version` is now set, which
  makes cobra provide the flag. The `version` subcommand is unchanged, and both
  spellings print the identical string (bare version, no "<name> version "
  prefix).

## [0.5.0] - 2026-07-12

### Removed

- **darwin/amd64 (Intel) pre-built binary.** macOS releases now ship
  **arm64 only**, per the org-wide policy (darwin is Apple-Silicon only; no
  universal binaries). Intel Mac users can build from source.

### Changed

- **Linux release archives are now `.tar.gz`** (darwin/windows remain `.zip`),
  per `nlink-jp/.github` CONVENTIONS.md §Release Archive Standard.
- **`README.md` and `LICENSE` are now bundled** in every release archive
  (previously the archive held only the binary).
- **darwin code-signature identifier** is now the canonical `data-toolbox-mcp`.

No change to the binary's behaviour — a packaging / build-config release.

## [0.4.0] - 2026-06-06

UX polish driven by the LLM-side feedback collected after the v0.3.0 verification. Five additive items in one release (ADR-0010), no breaking changes.

### Added

- **`describe_workspace(workspace_id)`** MCP tool. Returns the full list of user tables with their column schemas. Symmetric to `list_workspaces`: the latter lists workspaces, this one drills into one. Row counts are intentionally excluded (heavy at scale; revisit on demand).
- **`delete_workspace`** gains a `dry_run` argument (default false). When `true`, returns `{would_delete, container_id, container_state, host_paths, disk_usage_bytes}` without removing anything, so the LLM can show "this is what would be deleted" to the user before acting.
- **`query_data`** result gains `truncated` and `total` (ADR-0010):
  - `truncated`: alias of `limit_reached` (kept for backward compat).
  - `total`: the true row count of the un-LIMIT-ed user query. Equal to `row_count` when not truncated; when truncated, an additional `SELECT COUNT(*) FROM (user_sql) sub` is run to fetch the real total.
  - `total_unavailable_reason`: set (e.g. `"count_timed_out"`) when the extra COUNT couldn't finish.
- **`query_data`** table-not-found errors now carry a hint. When the stderr looks like `CatalogException: Table "<X>" does not exist`, the structured `script_failed` error's `details` gains `missing_table`, `available_tables_in_this_workspace`, and `other_workspaces` so the LLM can immediately spot a typo / wrong workspace_id / unloaded table.
- Tool `description` strings expanded for `load_data` / `execute_code` / `query_data` / `delete_workspace` with a one-line hint each (when to use load_from_work / artifact convention / truncated behavior / dry_run knob), surfacing the right tool earlier.

### Changed

- Tool surface: 8 → 9 (`describe_workspace` added).
- `Manager.PreviewDelete` + `Manager.ContainerStateOf` added to `internal/workspace`.

### Tests

- 4 new e2e scenarios under `e2e/v0_4_0_test.go`:
  - `TestE2E_v040_QueryData_TruncatedTotal`: 20001-row table → truncated=true + total=20001.
  - `TestE2E_v040_QueryData_TableNotFoundHint`: missing-table query → details lists available + other workspaces.
  - `TestE2E_v040_DeleteWorkspace_DryRun`: dry_run=true returns preview, workspace still alive after.
  - `TestE2E_v040_DescribeWorkspace_RoundTrip`: two seeded tables → describe returns both with full column schemas.
- All 14 e2e scenarios (v0.1.x through v0.4.0) green in 145s.

### Compatibility

Strictly additive: existing fields retained (`limit_reached`, the bare `delete_workspace` flow). No removed fields, no argument renames, no runtime semantic changes.

## [0.3.0] - 2026-06-06

Closes the "last mile of artifact handoff" identified during the v0.2.x real-machine verification:

1. The LLM can now return generated artifacts (PNG plots, CSV/JSON snippets, etc.) **as MCP image / text content blocks** instead of via a host file reference, so MCP clients (Claude Desktop) inline-render them with no connected-folder setup.
2. The LLM can table-ize files **that already live inside the sandbox** (e.g. files written by `execute_code`), without needing them to be present in `allowed_paths`.

### Added

- **`attach_files`** MCP tool (ADR-0008). Returns any of the workspace's `/work` files as MCP content blocks dispatched by extension:
  - PNG / JPG / JPEG / GIF / WEBP / BMP / SVG → MCP image content (base64 + mimeType). Claude Desktop renders inline.
  - CSV / TSV / JSON / JSONL / NDJSON / TXT / MD / LOG / YAML / TOML → MCP text content.
  - All other types → metadata-only (host path + size + sha256 when ≤100 MiB).
  - Per-file cap `10 MiB` and cumulative cap `20 MiB` (configurable via `[attach] max_single_size_bytes` / `max_total_size_bytes`); over-cap files downgrade to metadata-only.
  - Path-traversal defense-in-depth: each path is resolved under `<host_work_dir>` with `filepath.Clean` + prefix re-check.
- **`load_from_work`** MCP tool (ADR-0009). Table-izes a sandbox file by its container-absolute `/work/...` path:
  - Reads the file directly from `<host_work_dir>` (no copy through `_upload/`).
  - Reader chosen by extension (CSV / JSON / Parquet), same table as `load_data`.
  - Requires `/work/` prefix; rejects host paths and traversal attempts.
  - Bypasses `allowed_paths` because the target is already inside the sandbox.
- `internal/mcpserver.RawResult` + `ContentBlock` types let a tool handler return multiple content blocks. Existing tools keep returning a single text block (backward compatible).
- `internal/tools/load_helpers.go` factors `chooseReader` / `validateTableName` / `buildLoadScript` / `runLoadScript` so `load_data` and `load_from_work` share the engine.
- `[attach]` section in `config.toml` with `max_single_size_bytes` and `max_total_size_bytes` (defaults 10 MiB / 20 MiB).

### Changed

- Tool surface: 6 → 8 (`load_data` / `query_data` / `execute_code` / `list_workspaces` / `delete_workspace` / `describe_runtime` / `attach_files` / `load_from_work`).
- `load_data` was refactored to use the new shared helpers; behavior unchanged.

### Tests

- 7 new unit tests under `internal/tools` covering `attach_files` (extension dispatch, per-file cap, cumulative cap, traversal rejection, bad-args) and `load_from_work` (non-`/work` rejection, missing-arg / table_name rules).
- 3 new e2e scenarios under `e2e/v0_3_0_test.go`: `AttachFiles_RoundTrip`, `LoadFromWork_RoundTrip`, `LoadFromWork_RejectsOutsideWork`.
- Full e2e suite (10 scenarios incl. all v0.1.x / v0.2.x) all green.

### Compatibility

Strictly additive: no tool arguments changed, no result fields removed, no runtime semantics changed.

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
