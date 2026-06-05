package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/nlink-jp/data-toolbox-mcp/internal/config"
	"github.com/nlink-jp/data-toolbox-mcp/internal/toolerr"
	"github.com/nlink-jp/data-toolbox-mcp/internal/workspace"
)

type loadFromWorkArgs struct {
	WorkspaceID string `json:"workspace_id"`
	FilePath    string `json:"file_path"`
	TableName   string `json:"table_name"`
}

// LoadFromWork implements the load_from_work MCP tool (ADR-0009).
//
// Table-izes a file that already lives inside the workspace's /work mount,
// bypassing allowed_paths. The file_path must start with /work/ and stay
// inside the workspace's host-side work directory after symlink-clean
// resolution (defense-in-depth).
func LoadFromWork(ctx context.Context, mgr *workspace.Manager, cfg *config.Config, rawArgs json.RawMessage) (any, error) {
	var args loadFromWorkArgs
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

	// Contract: file_path must be a container-absolute path under /work/.
	// (Use load_data if you have a host absolute path instead.)
	if !strings.HasPrefix(args.FilePath, "/work/") {
		return nil, toolerr.Newf(toolerr.CodeInvalidArguments,
			"file_path must start with /work/ (got %q); use load_data for host paths",
			args.FilePath)
	}

	w, err := mgr.Ensure(ctx, args.WorkspaceID)
	if err != nil {
		return nil, wrapWorkspaceErr(err)
	}

	// Defense-in-depth: re-verify the resolved host path stays inside
	// HostWorkDir. ValidateID already constrains workspace_id, but check the
	// /work-relative subpath here too (handles "/work/../escape" etc.).
	sub := strings.TrimPrefix(args.FilePath, "/work/")
	cleanedHWD := filepath.Clean(w.HostWorkDir)
	full := filepath.Clean(filepath.Join(cleanedHWD, sub))
	rel, rerr := filepath.Rel(cleanedHWD, full)
	if rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return nil, toolerr.Newf(toolerr.CodeInvalidArguments,
			"path escapes /work: %q", args.FilePath)
	}

	// The container-side file path is the user-supplied file_path verbatim
	// (already /work/...), since /work in the container mirrors HostWorkDir
	// on the host.
	return runLoadScript(ctx, mgr, w, cfg, "load_from_work",
		args.TableName, chooseReader(args.FilePath), args.FilePath)
}
