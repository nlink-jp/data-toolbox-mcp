# ADR-0012: Leave path judgement to nlink-jp/pathguard — keep no copy

- Status: Accepted
- Date: 2026-09-22

## Context

Since ADR-0011, `work_dir` validation and the read blacklist (`workdir.Sensitive`) lived in
`internal/mcp/workdir`, a copy of voice-scribe's (the reference implementation of organization
ADR-021); seven other servers held the same copy. Every copy compared places **by name**. APFS is
case-insensitive by default, so `~/.SSH`, `.ENV` and `/USR/local` named the same places and passed
the checks. When the home directory could not be determined, `Sensitive` returned "" and passed
everything.

The organization moved this judgement into one module (`nlink-jp/pathguard`, lib-series). It compares
places by file identity and by names folded the way the disk folds them, and it catches a place that
does not exist yet through the identity of its parent. It holds one list, the same as gem-agent's and
lagent's.

## Decision

- Depend on `github.com/nlink-jp/pathguard` v0.1.0. No code from outside this organization comes
  with it.
- `internal/mcp/workdir` becomes a **thin adapter**. It keeps only:
  - taking the request's `_meta` from the context and passing it to `pathguard/workdir`'s `Resolve`,
  - moving that `*workdir.Error` onto `toolerr` with the same code, message and details,
  - `NewResolver(serverDirs...)` — passing this server's own directories (today only its config
    directory, `~/.config/data-toolbox-mcp`) as protected places (`pathguard.ServerDir`), and its one
    sentence for `work_dir_required` as `RequiredHint`. An empty path refuses every call rather than
    protecting nothing (the config directory is undetermined only when the home directory is, and
    then pathguard refuses everything anyway),
  - `Sensitive` — `pathguard/workdir.Sensitive` (the Local policy), passed through.
- The call sites (`Resolve`, `Validate`, `Sensitive`) do not change. What changes is the one line that
  builds the resolver (`workDirResolver` in `internal/tools/workdir.go`) and the tests that built it as a zero value.
- `TestOnlyOnePlaceConstructsAResolver` now checks on the syntax trees that `workdir.go` holds the one
  `workdir.NewResolver` call and that no `workdir.Resolver` literal exists anywhere (the zero value
  refuses every call).
- The tests of the judgement itself are in pathguard. What stays here are the adapter's tests (taking
  `_meta`, carrying the error across, the protected place, a zero value refusing) and the existing
  contract tests.

## Consequences

`load_data` and the `work_dir` check behave differently (the CHANGELOG says so):

- **Refused now**: the real places under your home from the runtimes' list (`~/.kube`,
  `~/.config/gh`, `~/.azure`, `~/.terraform.d`, `~/.gemini`, `~/.config/mcp-bridge`, `~/.netrc`,
  `~/.npmrc`, `~/.pypirc`, `~/.git-credentials`, `~/.vault-token`, `~/.docker/config.json`,
  `~/.claude.json`, `~/.bash_history`, `~/.zsh_history`); every spelling of any floor place — case
  variants, links, firmlinks; wherever a link directly inside one of those directories points (a
  `~/.ssh/config` that links into a sync folder protects the file it points at); when `$HOME` names
  another directory than the account's home, both; Linux `/etc` as a `work_dir`.
- **Accepted now**: `.env.example`, `.env.sample`, `.env.template`, `.env.dist` (templates, not
  secrets).
- **An unknown home refuses `load_data` paths and every `work_dir`.** It used to pass everything.
- `work_dir_denied` carries `reason` in its `details`.
- One check costs about 2 ms (measured in pathguard) — nothing next to a load.

With no copy here, a fix to the judgement is a pathguard release and a one-line dependency update.

## Amendment (2026-09-22): judge the directory actually used

Only `work_dir` was checked, so `work_dir=~/.config` with `workspace_id=gh` made the workspace
`~/.config/gh`, mounted into the container as `/work` (`load_from_work`, `query_data` or a script
could read a credential file there and return it; `delete_workspace` could delete it). The hole dates
from ADR-0011; image-forge's independent review found it. `workspace.NewManager(cfg, podman, check)`
takes the judgement as a required argument, and `Ensure`, `PreviewDelete` and `Delete` judge
`<work_dir>/<workspace_id>` with `workdir.Resolver.CheckBeneath` (pathguard v0.2.0) first. The server
passes `tools.WorkspaceCheck`, wired once in `newWorkspaceManager`; a Manager without one refuses
every workspace. pathguard v0.2.0 also refuses a path holding a NUL byte.

## Amendment (2026-09-22, v0.8.1): whether a file exists never changes the answer

A `load_data` `file_path` was resolved with `filepath.EvalSymlinks` before the floor judged it, so a
file in a credential location got `path_not_allowed` when it was there and `invalid_arguments` when it
was not — the answer told the caller which secrets exist. `attach_files` did not apply the floor to
files in `/work` at all, and returned a `.env` there, or the target of a link in `~/.ssh` when the
workspace lay in that sync folder, when present. It is the class the independent reviews of
slack-mcp-extender and chrome-pilot-mcp found; here it was measured with the home directory redirected
to a temporary one (all 7 `load_data` pairs and 2 of 4 `attach_files` pairs got different answers; the
two planted links out of `/work` were refused by the `os.Root` whether or not their targets existed).

- `ResolveInput` places the path first (`workdir.Where`, the last of pathguard's `Forms`: every link
  followed, a dangling one by its target — for a path that exists, what `EvalSymlinks` returns), judges
  it as given and as placed, and only then asks existence of the place (`EvalSymlinks(where)`). When it
  resolves elsewhere than it was placed (it changed in between), it is judged again there. A refusal's
  `details.resolved` is the place, which does not depend on existence.
- `attach_files` judges each path at its place before `root.Stat`; reads still go through the
  `os.Root`.
- A path that does not resolve gets no branch of its own.
- `TestExistenceIsNotRevealedByLoadData` and `…ByAttachFiles` call the same path while a file is there
  and after it is removed and compare the whole answer. The mutations (the old order, the
  `attach_files` floor removed, existence re-walked from the spelling, no placement) all fail by
  assertion.
- Limits: the floor on `attach_files` is not a boundary for `/work`. `execute_code` reads everything
  there, so a workspace that *contains* a protected place (a sync folder a link in `~/.ssh` leads into)
  stays visible to the sandbox. A hard link to a credential file made elsewhere is refused by identity
  only while it exists; whoever can make one already reaches the file.

## References

- Organization ADR-021 (the work-dir contract of the file-mediated MCP servers)
- ADR-0011 (work-dir contract): the closed list of checks and the read blacklist — whose
  implementation this replaces
- nlink-jp/pathguard's RFP (`docs/en/pathguard-rfp.md`)
