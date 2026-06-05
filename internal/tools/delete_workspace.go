package tools

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/nlink-jp/data-toolbox-mcp/internal/config"
	"github.com/nlink-jp/data-toolbox-mcp/internal/toolerr"
	"github.com/nlink-jp/data-toolbox-mcp/internal/workspace"
)

type deleteWorkspaceArgs struct {
	WorkspaceID string `json:"workspace_id"`
}

// DeleteWorkspaceResult is the structured return of delete_workspace.
type DeleteWorkspaceResult struct {
	Deleted     bool   `json:"deleted"`
	WorkspaceID string `json:"workspace_id"`
}

// DeleteWorkspace implements the delete_workspace MCP tool (ADR-0006).
// Irreversible: removes the container (if any) and wipes the on-disk state.
// Relies on the MCP client's user-approval gate for confirmation.
func DeleteWorkspace(ctx context.Context, mgr *workspace.Manager, _ *config.Config, rawArgs json.RawMessage) (any, error) {
	var args deleteWorkspaceArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, toolerr.Newf(toolerr.CodeInvalidArguments, "invalid arguments: %v", err)
	}
	if args.WorkspaceID == "" {
		return nil, toolerr.New(toolerr.CodeMissingArgument, "workspace_id is required")
	}

	if err := mgr.Delete(ctx, args.WorkspaceID); err != nil {
		// ValidateID and friends already return *toolerr.Error; pass through.
		var te *toolerr.Error
		if errors.As(err, &te) {
			return nil, te
		}
		return nil, toolerr.Newf(toolerr.CodeWorkspaceFailed, "delete workspace: %v", err)
	}
	return DeleteWorkspaceResult{Deleted: true, WorkspaceID: args.WorkspaceID}, nil
}
