package tools

import (
	"context"
	"encoding/json"

	"github.com/nlink-jp/data-toolbox-mcp/internal/config"
	"github.com/nlink-jp/data-toolbox-mcp/internal/toolerr"
	"github.com/nlink-jp/data-toolbox-mcp/internal/workspace"
)

type describeWorkspaceArgs struct {
	WorkspaceID string `json:"workspace_id"`
}

// ColumnInfo is one entry in DescribeWorkspaceResult.Tables[i].Columns.
type ColumnInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// TableInfo is one table's full schema, as returned by describe_workspace.
type TableInfo struct {
	Name    string       `json:"name"`
	Columns []ColumnInfo `json:"columns"`
}

// DescribeWorkspaceResult is the structured return of describe_workspace
// (ADR-0010). Pair with list_workspaces for "browse → drill into one workspace."
type DescribeWorkspaceResult struct {
	WorkspaceID    string      `json:"workspace_id"`
	HostWorkDir    string      `json:"host_work_dir"`
	ContainerState string      `json:"container_state"`
	Tables         []TableInfo `json:"tables"`
}

// DescribeWorkspace implements the describe_workspace MCP tool (ADR-0010).
// Returns every user table's schema in the workspace, intended to be called
// once when the LLM resumes work on an existing workspace (or at any time
// to confirm "what's in here?").
func DescribeWorkspace(ctx context.Context, mgr *workspace.Manager, cfg *config.Config, rawArgs json.RawMessage) (any, error) {
	var args describeWorkspaceArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, toolerr.Newf(toolerr.CodeInvalidArguments, "invalid arguments: %v", err)
	}
	if args.WorkspaceID == "" {
		return nil, toolerr.New(toolerr.CodeMissingArgument, "workspace_id is required")
	}

	w, err := mgr.Ensure(ctx, args.WorkspaceID)
	if err != nil {
		return nil, wrapWorkspaceErr(err)
	}

	// One script: SHOW TABLES → DESCRIBE for each → JSON.
	// Saves a podman exec round trip vs. issuing N + 1 calls.
	script := `
import duckdb, json
con = duckdb.connect("/work/analysis.duckdb")
tables = [r[0] for r in con.execute("SHOW TABLES").fetchall()]
out = []
for t in tables:
    desc = con.execute(f'DESCRIBE "{t}"').fetchall()
    out.append({
        "name": t,
        "columns": [{"name": d[0], "type": d[1]} for d in desc],
    })
print(json.dumps({"tables": out}))
`
	res, err := execScript(ctx, mgr, w, cfg, "describe_ws", script)
	if err != nil {
		return nil, toolerr.Newf(toolerr.CodeContainerFailed, "podman exec: %v", err)
	}
	if res.ExitCode != 0 {
		return nil, toolerr.Newf(toolerr.CodeScriptFailed,
			"describe_workspace script failed (exit %d): %s",
			res.ExitCode, string(res.Stderr)).WithDetails(map[string]any{
			"exit_code": res.ExitCode,
			"stderr":    string(res.Stderr),
		})
	}

	var parsed struct {
		Tables []TableInfo `json:"tables"`
	}
	if err := json.Unmarshal(res.Stdout, &parsed); err != nil {
		return nil, toolerr.Newf(toolerr.CodeScriptOutputParse,
			"parse python output: %v", err).WithDetails(map[string]any{
			"stdout": string(res.Stdout),
		})
	}

	// container_state: ask podman; default "absent" on error.
	containerState := "absent"
	if st, perr := mgr.ContainerStateOf(ctx, args.WorkspaceID); perr == nil {
		containerState = st
	}

	return DescribeWorkspaceResult{
		WorkspaceID:    args.WorkspaceID,
		HostWorkDir:    w.HostWorkDir,
		ContainerState: containerState,
		Tables:         parsed.Tables,
	}, nil
}
