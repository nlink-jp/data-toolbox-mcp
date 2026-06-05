package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/nlink-jp/data-toolbox-mcp/internal/config"
	"github.com/nlink-jp/data-toolbox-mcp/internal/toolerr"
	"github.com/nlink-jp/data-toolbox-mcp/internal/workspace"
)

type executeCodeArgs struct {
	WorkspaceID string `json:"workspace_id"`
	Language    string `json:"language"`
	Code        string `json:"code"`
}

// ExecuteCodeResult is the structured return of execute_code.
//
// HostWorkDir was added in v0.2.1 (ADR-0006 amendment) so the LLM can tell
// the user where artifacts written to /work/<name> actually land on the host.
type ExecuteCodeResult struct {
	Stdout      string `json:"stdout"`
	Stderr      string `json:"stderr"`
	ExitCode    int    `json:"exit_code"`
	HostWorkDir string `json:"host_work_dir"`
}

// ExecuteCode implements the execute_code MCP tool.
// Only language="python" is accepted (ADR-0003).
func ExecuteCode(ctx context.Context, mgr *workspace.Manager, cfg *config.Config, rawArgs json.RawMessage) (any, error) {
	var args executeCodeArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, toolerr.Newf(toolerr.CodeInvalidArguments, "invalid arguments: %v", err)
	}
	if args.WorkspaceID == "" || args.Code == "" {
		return nil, toolerr.New(toolerr.CodeMissingArgument, "workspace_id and code are required")
	}
	if args.Language != "python" {
		return nil, toolerr.Newf(toolerr.CodeUnsupportedLanguage,
			"unsupported_language: %q (only \"python\" is supported in Phase 1)", args.Language).
			WithDetails(map[string]any{"requested": args.Language, "supported": []string{"python"}})
	}

	w, err := mgr.Ensure(ctx, args.WorkspaceID)
	if err != nil {
		return nil, wrapWorkspaceErr(err)
	}

	codeDir := filepath.Join(w.HostWorkDir, "_code")
	if err := os.MkdirAll(codeDir, 0o755); err != nil {
		return nil, toolerr.Newf(toolerr.CodeWorkspaceFailed, "mkdir _code: %v", err)
	}
	name := "exec-" + randomHex() + ".py"
	hostPath := filepath.Join(codeDir, name)
	if err := os.WriteFile(hostPath, []byte(args.Code), 0o644); err != nil {
		return nil, toolerr.Newf(toolerr.CodeWorkspaceFailed, "write code: %v", err)
	}

	timeoutSec := cfg.Container.Limits.TimeoutSeconds
	if timeoutSec <= 0 {
		timeoutSec = 60
	}
	res, err := mgr.Exec(ctx, w, []string{"python", "/work/_code/" + name}, time.Duration(timeoutSec)*time.Second)
	if err != nil {
		return nil, toolerr.Newf(toolerr.CodeContainerFailed, "podman exec: %v", err)
	}
	return ExecuteCodeResult{
		Stdout:      string(res.Stdout),
		Stderr:      string(res.Stderr),
		ExitCode:    res.ExitCode,
		HostWorkDir: w.HostWorkDir,
	}, nil
}
