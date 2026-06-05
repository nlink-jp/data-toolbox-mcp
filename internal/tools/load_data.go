package tools

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/nlink-jp/data-toolbox-mcp/internal/config"
	"github.com/nlink-jp/data-toolbox-mcp/internal/toolerr"
	"github.com/nlink-jp/data-toolbox-mcp/internal/workspace"
)

type loadDataArgs struct {
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
// Uses ResolveAndCheck for allowed_paths defense; the reader, script, and
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

	resolved, err := ResolveAndCheck(args.FilePath, cfg.Workspace.AllowedPaths)
	if err != nil {
		return nil, err
	}

	w, err := mgr.Ensure(ctx, args.WorkspaceID)
	if err != nil {
		return nil, wrapWorkspaceErr(err)
	}

	uploadDir := filepath.Join(w.HostWorkDir, "_upload")
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		return nil, toolerr.Newf(toolerr.CodeWorkspaceFailed, "mkdir _upload: %v", err)
	}
	dst := filepath.Join(uploadDir, filepath.Base(resolved))
	if err := copyFile(resolved, dst); err != nil {
		return nil, toolerr.Newf(toolerr.CodeWorkspaceFailed, "copy host file: %v", err)
	}

	containerPath := "/work/_upload/" + filepath.Base(resolved)
	return runLoadScript(ctx, mgr, w, cfg, "load",
		args.TableName, chooseReader(resolved), containerPath)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
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
