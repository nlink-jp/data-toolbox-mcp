package tools

import (
	"context"
	"encoding/json"

	"github.com/nlink-jp/data-toolbox-mcp/internal/config"
	"github.com/nlink-jp/data-toolbox-mcp/internal/toolerr"
	"github.com/nlink-jp/data-toolbox-mcp/internal/workspace"
)

// ListWorkspacesResult is the structured return of list_workspaces.
type ListWorkspacesResult struct {
	Workspaces []workspace.WorkspaceInfo `json:"workspaces"`
}

// listWorkspacesArgs is the input of list_workspaces.
type listWorkspacesArgs struct {
	WorkDir string `json:"work_dir"`
}

// ListWorkspaces implements the list_workspaces MCP tool (ADR-0006).
// It returns every workspace whose disk state is present under the caller's
// work_dir, with its last_used time and current container_state.
func ListWorkspaces(ctx context.Context, mgr *workspace.Manager, _ *config.Config, rawArgs json.RawMessage) (any, error) {
	var args listWorkspacesArgs
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return nil, toolerr.Newf(toolerr.CodeInvalidArguments, "invalid arguments: %v", err)
		}
	}
	workDir, err := resolveWorkDir(ctx, args.WorkDir)
	if err != nil {
		return nil, err
	}
	infos, err := mgr.List(ctx, workDir)
	if err != nil {
		return nil, toolerr.Newf(toolerr.CodeWorkspaceFailed, "list workspaces: %v", err)
	}
	return ListWorkspacesResult{Workspaces: infos}, nil
}
