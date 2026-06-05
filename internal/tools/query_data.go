package tools

import (
	"context"
	"encoding/json"
	"fmt"
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
type QueryDataResult struct {
	Rows         []map[string]any `json:"rows"`
	RowCount     int              `json:"row_count"`
	LimitApplied int              `json:"limit_applied,omitempty"`
	LimitReached bool             `json:"limit_reached,omitempty"`
}

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
		return nil, toolerr.Newf(toolerr.CodeScriptFailed,
			"query_data script failed (exit %d): %s", res.ExitCode, strings.TrimSpace(string(res.Stderr))).
			WithDetails(map[string]any{
				"exit_code": res.ExitCode,
				"stderr":    string(res.Stderr),
			})
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
	}
	return out, nil
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
