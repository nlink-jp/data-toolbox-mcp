package tools

import (
	"context"
	"encoding/json"

	"github.com/nlink-jp/data-toolbox-mcp/internal/config"
	"github.com/nlink-jp/data-toolbox-mcp/internal/mcpserver"
	"github.com/nlink-jp/data-toolbox-mcp/internal/workspace"
)

// Register attaches load_data / query_data / execute_code to the MCP server.
func Register(srv *mcpserver.Server, mgr *workspace.Manager, cfg *config.Config) {
	srv.RegisterTool(mcpserver.Tool{
		Name:        "load_data",
		Description: "Load a host file into the workspace's DuckDB as a table. file_path must be within allowed_paths.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"workspace_id": {"type":"string","description":"Workspace key (matches ^[a-zA-Z0-9_-]{1,64}$)."},
				"file_path":    {"type":"string","description":"Host absolute path. Must lie under allowed_paths after symlink resolution."},
				"table_name":   {"type":"string","description":"DuckDB table name to create or replace."}
			},
			"required": ["workspace_id","file_path","table_name"]
		}`),
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		return LoadData(ctx, mgr, cfg, args)
	})

	srv.RegisterTool(mcpserver.Tool{
		Name:        "query_data",
		Description: "Run SQL against the workspace's DuckDB and return rows as JSON. If the SQL has no LIMIT, a default row cap is applied.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"workspace_id": {"type":"string"},
				"sql":          {"type":"string","description":"DuckDB SQL. Reads only; persistence goes through load_data and execute_code."}
			},
			"required": ["workspace_id","sql"]
		}`),
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		return QueryData(ctx, mgr, cfg, args)
	})

	srv.RegisterTool(mcpserver.Tool{
		Name:        "execute_code",
		Description: "Run Python code inside the workspace's container. Bundled libraries: duckdb, pandas, polars, pyarrow. DuckDB file is at /work/analysis.duckdb.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"workspace_id": {"type":"string"},
				"language":     {"type":"string","enum":["python"]},
				"code":         {"type":"string","description":"Python source. Use stdout for results."}
			},
			"required": ["workspace_id","language","code"]
		}`),
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		return ExecuteCode(ctx, mgr, cfg, args)
	})
}
