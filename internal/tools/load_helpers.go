package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nlink-jp/data-toolbox-mcp/internal/config"
	"github.com/nlink-jp/data-toolbox-mcp/internal/toolerr"
	"github.com/nlink-jp/data-toolbox-mcp/internal/workspace"
)

// tableNamePattern guards the SQL-identifier slot in CREATE TABLE.
// Shared by load_data and load_from_work.
var tableNamePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// ErrInvalidTableName is the sentinel for invalid `table_name` arguments.
// errors.Is matches by Code (toolerr.CodeInvalidTableName).
var ErrInvalidTableName = toolerr.New(toolerr.CodeInvalidTableName, "invalid table_name")

// validateTableName returns a structured error if name violates the rule.
func validateTableName(name string) error {
	if !tableNamePattern.MatchString(name) {
		return toolerr.Newf(toolerr.CodeInvalidTableName,
			"invalid table_name %q: must match ^[a-zA-Z_][a-zA-Z0-9_]*$", name)
	}
	return nil
}

// chooseReader returns the DuckDB reader function for the given path's
// extension. Defaults to read_csv_auto. Shared by load_data (host file
// resolved to /work/_upload/<basename>) and load_from_work (caller-supplied
// /work/<sub>/<name>).
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

// buildLoadScript composes the Python script that creates/replaces the
// table from the container-side file, then prints `{rows_loaded, schema}`
// as JSON. tableName is interpolated as a SQL identifier (already validated
// by validateTableName). containerPath is passed as a positional parameter
// so quoting is safe.
func buildLoadScript(tableName, readerFn, containerPath string) string {
	return fmt.Sprintf(`
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
		tableName, readerFn, containerPath, tableName, tableName,
	)
}

// runLoadScript writes the script into _code/, runs it under the configured
// timeout, classifies the result, and parses the JSON output.
// Shared engine for load_data and load_from_work.
func runLoadScript(ctx context.Context, mgr *workspace.Manager, w *workspace.Workspace,
	cfg *config.Config, prefix, tableName, readerFn, containerPath string,
) (LoadDataResult, error) {
	script := buildLoadScript(tableName, readerFn, containerPath)
	res, err := execScript(ctx, mgr, w, cfg, prefix, script)
	if err != nil {
		return LoadDataResult{}, toolerr.Newf(toolerr.CodeContainerFailed, "podman exec: %v", err)
	}
	if res.ExitCode != 0 {
		return LoadDataResult{}, toolerr.Newf(toolerr.CodeScriptFailed,
			"%s script failed (exit %d): %s", prefix, res.ExitCode, strings.TrimSpace(string(res.Stderr))).
			WithDetails(map[string]any{
				"exit_code": res.ExitCode,
				"stderr":    string(res.Stderr),
			})
	}
	var out LoadDataResult
	if err := json.Unmarshal(res.Stdout, &out); err != nil {
		return LoadDataResult{}, toolerr.Newf(toolerr.CodeScriptOutputParse,
			"parse python output: %v", err).WithDetails(map[string]any{
			"stdout": string(res.Stdout),
			"stderr": string(res.Stderr),
		})
	}
	return out, nil
}
