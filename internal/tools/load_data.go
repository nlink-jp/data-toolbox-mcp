package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nlink-jp/data-toolbox-mcp/internal/config"
	"github.com/nlink-jp/data-toolbox-mcp/internal/toolerr"
	"github.com/nlink-jp/data-toolbox-mcp/internal/workspace"
)

type loadDataArgs struct {
	WorkspaceID string `json:"workspace_id"`
	FilePath    string `json:"file_path"`
	TableName   string `json:"table_name"`
}

// LoadDataResult is the structured return value of load_data.
type LoadDataResult struct {
	RowsLoaded int                 `json:"rows_loaded"`
	Schema     []map[string]string `json:"schema"`
}

// tableNamePattern guards the table name interpolated into the SQL identifier
// position. Anything else would let a malicious value escape the identifier
// quoting.
var tableNamePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// ErrInvalidTableName is the sentinel for an invalid SQL identifier in
// load_data's table_name. errors.Is matches by Code.
var ErrInvalidTableName = toolerr.New(toolerr.CodeInvalidTableName, "invalid table_name")

// LoadData implements the load_data MCP tool.
func LoadData(ctx context.Context, mgr *workspace.Manager, cfg *config.Config, rawArgs json.RawMessage) (any, error) {
	var args loadDataArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, toolerr.Newf(toolerr.CodeInvalidArguments, "invalid arguments: %v", err)
	}
	if args.WorkspaceID == "" || args.FilePath == "" || args.TableName == "" {
		return nil, toolerr.New(toolerr.CodeMissingArgument,
			"workspace_id, file_path, and table_name are required")
	}
	if !tableNamePattern.MatchString(args.TableName) {
		return nil, toolerr.Newf(toolerr.CodeInvalidTableName,
			"invalid table_name %q: must match ^[a-zA-Z_][a-zA-Z0-9_]*$", args.TableName)
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

	readFn := chooseReader(resolved)
	containerPath := "/work/_upload/" + filepath.Base(resolved)

	script := fmt.Sprintf(`
import duckdb, json
con = duckdb.connect("/work/analysis.duckdb")
con.execute('CREATE OR REPLACE TABLE "%s" AS SELECT * FROM %s(?)', [%q])
n = con.execute('SELECT COUNT(*) FROM "%s"').fetchone()[0]
schema = con.execute('DESCRIBE "%s"').fetchall()
print(json.dumps({
    "rows_loaded": n,
    "schema": [{"name": r[0], "type": r[1]} for r in schema],
}))
`,
		args.TableName, readFn, containerPath, args.TableName, args.TableName,
	)

	res, err := execScript(ctx, mgr, w, cfg, "load", script)
	if err != nil {
		return nil, toolerr.Newf(toolerr.CodeContainerFailed, "podman exec: %v", err)
	}
	if res.ExitCode != 0 {
		return nil, toolerr.Newf(toolerr.CodeScriptFailed,
			"load_data script failed (exit %d): %s", res.ExitCode, strings.TrimSpace(string(res.Stderr))).
			WithDetails(map[string]any{
				"exit_code": res.ExitCode,
				"stderr":    string(res.Stderr),
			})
	}

	var out LoadDataResult
	if err := json.Unmarshal(res.Stdout, &out); err != nil {
		return nil, toolerr.Newf(toolerr.CodeScriptOutputParse,
			"parse python output: %v", err).WithDetails(map[string]any{
			"stdout": string(res.Stdout),
			"stderr": string(res.Stderr),
		})
	}
	return out, nil
}

func chooseReader(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json", ".jsonl", ".ndjson":
		return "read_json_auto"
	case ".parquet":
		return "read_parquet"
	default:
		return "read_csv_auto"
	}
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
