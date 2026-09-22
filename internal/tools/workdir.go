package tools

import (
	"context"

	"github.com/nlink-jp/data-toolbox-mcp/internal/config"
	"github.com/nlink-jp/data-toolbox-mcp/internal/workdir"
)

// resolveWorkDir resolves and validates the caller's work directory for one
// call: the `work_dir` argument, else the runtime hint in the request's
// `_meta`, else an error (organization ADR-021 §2).
//
// Every tool goes through here. That is the point: eight tools each building
// their own `workdir.Resolver{}` is eight places to forget what the resolver
// has to refuse, and all eight had forgotten. A tool added later cannot,
// because there is nothing for it to construct — and a zero Resolver now
// refuses every call rather than protecting nothing.
func resolveWorkDir(ctx context.Context, arg string) (string, error) {
	return workDirResolver().Resolve(ctx, arg)
}

// WorkspaceCheck judges <work_dir>/<workspace_id> — the directory a call
// actually uses, which may not exist yet — with the same resolver (organization
// ADR-021 §4). The workspace manager takes it at construction and calls it
// before it mounts, loads from or deletes a workspace.
func WorkspaceCheck(dir string) error { return workDirResolver().CheckBeneath(dir) }

// workDirResolver builds the resolver, denying this server's own directories.
//
// A work directory is the caller's, not ours (organization ADR-021 §4: "not a
// system location … and not the server's own config or state directory" →
// `work_dir_denied`). Without the denial a caller could name our config
// directory as its work directory: `load_data` would bind-mount it into a
// container, `execute_code` would run arbitrary Python with it writable, and
// `delete_workspace` would remove a subtree of it — all on a model's say-so,
// against the file that sets this server's own container limits.
func workDirResolver() workdir.Resolver {
	return workdir.NewResolver(serverOwnedDirs()...)
}

// serverOwnedDirs lists this server's own config and state directories.
//
// There is one: the config directory. This server keeps no state on disk
// any more — workspaces, their DuckDB files and everything `execute_code`
// creates live under the caller's `work_dir`, which is what ADR-0011 moved
// them there for, and the runtime image lives in podman's storage rather than
// in a directory of ours.
//
// An operator who points `--config` at a file somewhere else is not covered:
// that directory is the operator's choice, not this server's own, and
// refusing an arbitrary directory — a project tree, or the process's working
// directory — would deny work directories callers legitimately use.
//
// An empty directory (no home) is passed on, and refuses every call rather
// than protecting nothing.
func serverOwnedDirs() []string {
	return []string{config.Dir()}
}
