package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/nlink-jp/data-toolbox-mcp/internal/config"
	"github.com/nlink-jp/data-toolbox-mcp/internal/toolerr"
	"github.com/nlink-jp/data-toolbox-mcp/internal/workspace"
)

type queryDataArgs struct {
	WorkspaceID string `json:"workspace_id"`
	SQL         string `json:"sql"`
}

// QueryDataResult is the structured return of query_data.
//
// v0.4.0 additions (ADR-0010):
//   - Truncated: alias of LimitReached, more common terminology.
//   - Total: total row count of the un-LIMIT-ed user query. Equal to
//     RowCount when not truncated; the COUNT(*) result when truncated.
//     nil when an additional COUNT() was attempted but didn't finish.
//   - TotalUnavailableReason: set when Total is nil (e.g. "count_timed_out").
type QueryDataResult struct {
	Rows                   []map[string]any `json:"rows"`
	RowCount               int              `json:"row_count"`
	LimitApplied           int              `json:"limit_applied,omitempty"`
	LimitReached           bool             `json:"limit_reached,omitempty"`
	Truncated              bool             `json:"truncated"`
	Total                  *int             `json:"total"`
	TotalUnavailableReason string           `json:"total_unavailable_reason,omitempty"`
}

// catalogMissingTablePattern extracts the table name from DuckDB's
// "Table with name <X> does not exist" / "Table or view ..." messages.
var catalogMissingTablePattern = regexp.MustCompile(`(?i)Table(?: with name)?\s+(?:"|with name\s+)?([A-Za-z_][A-Za-z0-9_]*)["']?\s+(?:does not exist|not found)`)

// QueryData implements the query_data MCP tool.
func QueryData(ctx context.Context, mgr *workspace.Manager, cfg *config.Config, rawArgs json.RawMessage) (any, error) {
	var args queryDataArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, toolerr.Newf(toolerr.CodeInvalidArguments, "invalid arguments: %v", err)
	}
	if args.WorkspaceID == "" || strings.TrimSpace(args.SQL) == "" {
		return nil, toolerr.New(toolerr.CodeMissingArgument, "workspace_id and sql are required")
	}

	w, err := mgr.Ensure(ctx, args.WorkspaceID)
	if err != nil {
		return nil, wrapWorkspaceErr(err)
	}

	limit := cfg.Query.DefaultRowLimit
	if limit <= 0 {
		limit = 20000
	}
	hasUserLimit := containsLimit(args.SQL)
	inner := strings.TrimRight(args.SQL, " ;\n\t")
	wrapped := fmt.Sprintf("SELECT * FROM (%s) sub LIMIT %d", inner, limit+1)

	script := fmt.Sprintf(`
import duckdb, json, datetime, decimal

def _to_jsonable(v):
    if isinstance(v, (datetime.date, datetime.datetime, datetime.time, decimal.Decimal)):
        return str(v)
    if isinstance(v, (bytes, bytearray)):
        return v.decode('utf-8', errors='replace')
    return v

con = duckdb.connect("/work/analysis.duckdb")
rows = con.execute(%q).fetchall()
cols = [d[0] for d in con.description]
out_rows = [dict(zip(cols, [_to_jsonable(v) for v in r])) for r in rows]
print(json.dumps({"rows": out_rows, "row_count": len(out_rows)}, default=str))
`, wrapped)

	res, err := execScript(ctx, mgr, w, cfg, "query", script)
	if err != nil {
		return nil, toolerr.Newf(toolerr.CodeContainerFailed, "podman exec: %v", err)
	}
	if res.ExitCode != 0 {
		return nil, buildQueryScriptError(ctx, mgr, w, cfg, args.WorkspaceID, res.ExitCode, res.Stderr)
	}

	var raw struct {
		Rows     []map[string]any `json:"rows"`
		RowCount int              `json:"row_count"`
	}
	if err := json.Unmarshal(res.Stdout, &raw); err != nil {
		return nil, toolerr.Newf(toolerr.CodeScriptOutputParse,
			"parse python output: %v", err).WithDetails(map[string]any{
			"stdout": string(res.Stdout),
			"stderr": string(res.Stderr),
		})
	}

	out := QueryDataResult{Rows: raw.Rows, RowCount: raw.RowCount, LimitApplied: limit}
	if !hasUserLimit && raw.RowCount > limit {
		out.Rows = raw.Rows[:limit]
		out.RowCount = limit
		out.LimitReached = true
		out.Truncated = true
		// Fetch the true total via an additional COUNT() (ADR-0010 item 2).
		total, totalErr := fetchUserQueryTotal(ctx, mgr, w, cfg, inner)
		if totalErr == nil {
			out.Total = &total
		} else {
			out.TotalUnavailableReason = totalErr.Error()
		}
	} else {
		// Not truncated: total equals row_count.
		rc := raw.RowCount
		out.Total = &rc
	}
	return out, nil
}

// fetchUserQueryTotal issues `SELECT COUNT(*) FROM (user_sql) sub` and returns
// the value. Returns ("count_timed_out", err) when the script times out, etc.
func fetchUserQueryTotal(ctx context.Context, mgr *workspace.Manager, w *workspace.Workspace, cfg *config.Config, userSQL string) (int, error) {
	countScript := fmt.Sprintf(`
import duckdb, json
con = duckdb.connect("/work/analysis.duckdb")
n = con.execute("SELECT COUNT(*) FROM (%s) sub").fetchone()[0]
print(json.dumps({"total": n}))
`, strings.ReplaceAll(userSQL, `"`, `\"`))
	res, err := execScript(ctx, mgr, w, cfg, "querycount", countScript)
	if err != nil {
		return 0, fmt.Errorf("count_timed_out")
	}
	if res.ExitCode != 0 {
		return 0, fmt.Errorf("count_failed")
	}
	var raw struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(res.Stdout, &raw); err != nil {
		return 0, fmt.Errorf("count_parse_failed")
	}
	return raw.Total, nil
}

// buildQueryScriptError constructs the script_failed error for a non-zero exit
// of the user's query. If the stderr suggests a missing-table CatalogException,
// also attach available tables and other workspaces as a hint (ADR-0010 item 4).
func buildQueryScriptError(ctx context.Context, mgr *workspace.Manager, w *workspace.Workspace,
	cfg *config.Config, wsID string, exitCode int, stderr []byte) error {
	details := map[string]any{
		"exit_code": exitCode,
		"stderr":    string(stderr),
	}
	if m := catalogMissingTablePattern.FindStringSubmatch(string(stderr)); len(m) == 2 {
		details["missing_table"] = m[1]
		if tables, err := listTablesInWorkspace(ctx, mgr, w, cfg); err == nil {
			details["available_tables_in_this_workspace"] = tables
		}
		if infos, err := mgr.List(ctx); err == nil {
			others := make([]string, 0, len(infos))
			for _, info := range infos {
				if info.ID != wsID {
					others = append(others, info.ID)
				}
			}
			details["other_workspaces"] = others
		}
	}
	return toolerr.Newf(toolerr.CodeScriptFailed,
		"query_data script failed (exit %d): %s", exitCode, strings.TrimSpace(string(stderr))).
		WithDetails(details)
}

// listTablesInWorkspace runs SHOW TABLES inside the workspace and returns the
// names. Shared with describe_workspace.
func listTablesInWorkspace(ctx context.Context, mgr *workspace.Manager, w *workspace.Workspace, cfg *config.Config) ([]string, error) {
	script := `
import duckdb, json
con = duckdb.connect("/work/analysis.duckdb")
rows = con.execute("SHOW TABLES").fetchall()
print(json.dumps([r[0] for r in rows]))
`
	res, err := execScript(ctx, mgr, w, cfg, "showtables", script)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("show tables failed (exit %d)", res.ExitCode)
	}
	var names []string
	if err := json.Unmarshal(res.Stdout, &names); err != nil {
		return nil, err
	}
	return names, nil
}

// containsLimit does a crude case-insensitive search for a LIMIT keyword at a
// word boundary. False positives (e.g. column named "limit_value") are
// acceptable here — they only suppress the synthetic limit, which is the
// caller's intent if they wrote LIMIT themselves.
func containsLimit(sql string) bool {
	u := strings.ToUpper(sql)
	idx := strings.Index(u, "LIMIT")
	for idx >= 0 {
		left := idx == 0 || !isIdentChar(u[idx-1])
		right := idx+5 >= len(u) || !isIdentChar(u[idx+5])
		if left && right {
			return true
		}
		next := strings.Index(u[idx+5:], "LIMIT")
		if next < 0 {
			return false
		}
		idx = idx + 5 + next
	}
	return false
}

func isIdentChar(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}
