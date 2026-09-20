package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/nlink-jp/data-toolbox-mcp/internal/config"
	"github.com/nlink-jp/data-toolbox-mcp/internal/toolerr"
	"github.com/nlink-jp/data-toolbox-mcp/internal/workdir"
	"github.com/nlink-jp/data-toolbox-mcp/internal/workspace"
)

type loadDataArgs struct {
	WorkDir     string `json:"work_dir"`
	WorkspaceID string `json:"workspace_id"`
	FilePath    string `json:"file_path"`
	TableName   string `json:"table_name"`
}

// LoadDataResult is the structured return value of load_data and load_from_work.
type LoadDataResult struct {
	RowsLoaded int                 `json:"rows_loaded"`
	Schema     []map[string]string `json:"schema"`
}

// LoadData implements the load_data MCP tool. Host file → workspace ingest.
// Uses ResolveInput for the blacklist floor; the reader, script, and
// result-parsing logic is shared with load_from_work via load_helpers.go.
func LoadData(ctx context.Context, mgr *workspace.Manager, cfg *config.Config, rawArgs json.RawMessage) (any, error) {
	var args loadDataArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, toolerr.Newf(toolerr.CodeInvalidArguments, "invalid arguments: %v", err)
	}
	if args.WorkspaceID == "" || args.FilePath == "" || args.TableName == "" {
		return nil, toolerr.New(toolerr.CodeMissingArgument,
			"workspace_id, file_path, and table_name are required")
	}
	if err := validateTableName(args.TableName); err != nil {
		return nil, err
	}

	resolved, err := ResolveInput(args.FilePath)
	if err != nil {
		return nil, err
	}

	workDir, err := workdir.Resolver{}.Resolve(ctx, args.WorkDir)
	if err != nil {
		return nil, err
	}

	w, err := mgr.Ensure(ctx, workDir, args.WorkspaceID)
	if err != nil {
		return nil, wrapWorkspaceErr(err)
	}

	if err := stageUpload(w.HostWorkDir, resolved); err != nil {
		return nil, err
	}

	containerPath := "/work/_upload/" + filepath.Base(resolved)
	return runLoadScript(ctx, mgr, w, cfg, "load",
		args.TableName, chooseReader(resolved), containerPath)
}

// stageUpload copies the host file src to <hostWorkDir>/_upload/<its name>,
// where the container finds it as /work/_upload/<name>.
//
// The write goes through an os.Root on the work directory. /work is writable
// by the code execute_code runs, so both _upload and the file inside it may be
// symlinks that code left behind; a plain MkdirAll + Create followed them and
// wrote the copy wherever they pointed on the host. A root refuses a path that
// leaves it, in the kernel's terms.
func stageUpload(hostWorkDir, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return toolerr.Newf(toolerr.CodeWorkspaceFailed, "copy host file: %v", err)
	}
	defer func() { _ = in.Close() }()
	if err := writeInWork(hostWorkDir, "_upload", filepath.Base(src), in); err != nil {
		return toolerr.Newf(toolerr.CodeWorkspaceFailed, "copy host file: %v", err)
	}
	return nil
}

// writeInWork is the only way this package writes into a workspace's work
// directory: <hostWorkDir>/<dir>/<name>, through an os.Root, so neither dir nor
// name can be a link that leads out. TestWorkspaceWritesGoThroughARoot keeps
// the path-based calls from coming back.
func writeInWork(hostWorkDir, dir, name string, content io.Reader) error {
	root, err := os.OpenRoot(hostWorkDir)
	if err != nil {
		return fmt.Errorf("open work directory: %w", err)
	}
	defer func() { _ = root.Close() }()

	if err := root.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	out, err := root.Create(filepath.Join(dir, name))
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, content); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// wrapWorkspaceErr surfaces ValidateID/Ensure errors with a structured code.
// ValidateID already returns a *toolerr.Error so pass through. Other errors
// (Podman failure, etc.) are wrapped under workspace_failed.
func wrapWorkspaceErr(err error) error {
	var te *toolerr.Error
	if asToolerr(err, &te) {
		return te
	}
	return toolerr.Newf(toolerr.CodeWorkspaceFailed, "workspace error: %v", err)
}

// asToolerr is a small inlined errors.As wrapper to avoid importing errors in
// every site that just wants to narrow.
func asToolerr(err error, dst **toolerr.Error) bool {
	for err != nil {
		if te, ok := err.(*toolerr.Error); ok {
			*dst = te
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
