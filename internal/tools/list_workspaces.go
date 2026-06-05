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

// ListWorkspaces implements the list_workspaces MCP tool (ADR-0006).
// No arguments; returns every workspace whose disk state is present
// under workspace_dir, with its last_used time and current container_state.
func ListWorkspaces(ctx context.Context, mgr *workspace.Manager, _ *config.Config, _ json.RawMessage) (any, error) {
	infos, err := mgr.List(ctx)
	if err != nil {
		return nil, toolerr.Newf(toolerr.CodeWorkspaceFailed, "list workspaces: %v", err)
	}
	return ListWorkspacesResult{Workspaces: infos}, nil
}
