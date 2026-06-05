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
	DryRun      bool   `json:"dry_run"`
}

// DeleteWorkspaceResult is the structured return of delete_workspace when
// dry_run is false (the default, destructive case).
type DeleteWorkspaceResult struct {
	Deleted     bool   `json:"deleted"`
	WorkspaceID string `json:"workspace_id"`
}

// DeleteWorkspaceDryRunResult is the structured return when dry_run is true
// (ADR-0010): nothing is removed, but the LLM sees what would be removed
// (container state + host paths + disk usage) so it can show the user a
// confirmation summary.
type DeleteWorkspaceDryRunResult struct {
	WouldDelete    bool              `json:"would_delete"`
	WorkspaceID    string            `json:"workspace_id"`
	ContainerID    string            `json:"container_id,omitempty"`
	ContainerState string            `json:"container_state"`
	HostPaths      map[string]string `json:"host_paths"`
	DiskUsageBytes int64             `json:"disk_usage_bytes"`
}

// DeleteWorkspace implements the delete_workspace MCP tool (ADR-0006 + ADR-0010).
// Default dry_run=false: removes the container (if any) and wipes on-disk state.
// dry_run=true: returns a preview without acting.
func DeleteWorkspace(ctx context.Context, mgr *workspace.Manager, _ *config.Config, rawArgs json.RawMessage) (any, error) {
	var args deleteWorkspaceArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, toolerr.Newf(toolerr.CodeInvalidArguments, "invalid arguments: %v", err)
	}
	if args.WorkspaceID == "" {
		return nil, toolerr.New(toolerr.CodeMissingArgument, "workspace_id is required")
	}

	if args.DryRun {
		preview, err := mgr.PreviewDelete(ctx, args.WorkspaceID)
		if err != nil {
			var te *toolerr.Error
			if errors.As(err, &te) {
				return nil, te
			}
			return nil, toolerr.Newf(toolerr.CodeWorkspaceFailed, "preview delete: %v", err)
		}
		return DeleteWorkspaceDryRunResult{
			WouldDelete:    true,
			WorkspaceID:    args.WorkspaceID,
			ContainerID:    preview.ContainerID,
			ContainerState: preview.ContainerState,
			HostPaths: map[string]string{
				"base":   preview.HostBaseDir,
				"work":   preview.HostWorkDir,
				"duckdb": preview.HostDBPath,
			},
			DiskUsageBytes: preview.DiskUsageBytes,
		}, nil
	}

	if err := mgr.Delete(ctx, args.WorkspaceID); err != nil {
		var te *toolerr.Error
		if errors.As(err, &te) {
			return nil, te
		}
		return nil, toolerr.Newf(toolerr.CodeWorkspaceFailed, "delete workspace: %v", err)
	}
	return DeleteWorkspaceResult{Deleted: true, WorkspaceID: args.WorkspaceID}, nil
}
