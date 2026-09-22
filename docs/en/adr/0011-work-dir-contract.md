# ADR-0011: Workspaces live under the caller's `work_dir`; `allowed_paths` is removed

- Status: Accepted — its implementation (the work-directory checks and the input blacklist) is
  replaced by [ADR-0012](0012-pathguard.md) (nlink-jp/pathguard)
- Date: 2026-09-13
- Amends: the workspace location (under `workspace_dir`) decided in [ADR-0001](0001-workspace-id-lifecycle.md), and the `allowed_paths` input guard

## Context

This applies organization ADR-021 (the work-directory contract for file-mediated
MCP servers) here, in the same shape as pcap-analyzer-mcp (its ADR-0008) — the
owner's instruction.

This server's output lived **where the caller cannot open it**. A workspace sat
under the server-owned `workspace_dir` (default `~/.data-toolbox`), and results
carried `host_work_dir` — the host path of what `execute_code` wrote into
`/work` — which no calling agent's file tools can read. `attach_files` returning
content inline is the reason that was survivable, not the design saying so.

On the input side, `allowed_paths` could not express what it was for: the
matcher is a resolved-path prefix test with no per-repository granularity, so
covering a work root means listing the home directory, and that admits `.ssh`
and `.aws`.

## Decision

1. **Every tool takes a required `work_dir`.** A workspace is
   `<work_dir>/<workspace_id>/`, and what mounts at `/work` in the container is
   `<work_dir>/<workspace_id>/work`. **`host_work_dir` now names a path the
   caller can open**, which is the point of this record.
2. **Resolution is argument → `_meta["jp.nlink/work_dir"]` → error**, with the
   closed validation list (absolute, no `~`, no `..`, exists and is a directory,
   writable, not a system or credential location).
3. **`workspace.workspace_dir` and `workspace.allowed_paths` are removed**, and a
   config still carrying either fails at startup with the reason named — a
   containment list an operator believes is in force, silently ignored, is the
   worst outcome available.
4. **`load_data`'s `file_path` is guarded by the blacklist alone**: credential and
   agent-control locations (`~/.ssh`, `~/.aws`, …) are refused and everything
   else is read, checked on both spellings of the path against both spellings of
   every entry. The blacklist is a floor, not a boundary.
5. **The container name carries a digest of the work directory.** The same
   `workspace_id` under two work directories is two workspaces, and one
   long-lived container cannot serve both.
6. `list_workspaces` and `delete_workspace` take `work_dir` too — they scan the
   caller's directory.

## Consequences

- **Breaking.** Every tool's schema changes, and a config with `workspace_dir` or
  `allowed_paths` will not start. Workspaces already under `~/.data-toolbox` stop
  being referenced (their contents are left alone; remove them by hand if
  unwanted).
- `host_work_dir` becomes an openable path — the reason for the change.
- `attach_files` remains useful for returning images and text inline, but it is
  no longer needed *because the path was unopenable*.
- Existing long-lived containers named `data-toolbox-mcp-<id>` are not reused;
  clean them up with `podman rm` if they are not wanted.

## References

- Organization ADR-021; pcap-analyzer-mcp ADR-0008 (the same shape, earlier);
  voice-scribe ADR-0010 (the reference implementation)
