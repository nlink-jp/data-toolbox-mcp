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

	// --- v0.2.0 tools (ADR-0006) ---

	srv.RegisterTool(mcpserver.Tool{
		Name:        "list_workspaces",
		Description: "List every workspace that has on-disk state (id, last_used, container_state). Use this to discover prior workspaces across chat sessions.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {},
			"additionalProperties": false
		}`),
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		return ListWorkspaces(ctx, mgr, cfg, args)
	})

	srv.RegisterTool(mcpserver.Tool{
		Name:        "delete_workspace",
		Description: "Stop the container (if any) and wipe a workspace's on-disk state completely. IRREVERSIBLE.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"workspace_id": {"type":"string","description":"Workspace key to delete. Must match ^[a-zA-Z0-9_-]{1,64}$."}
			},
			"required": ["workspace_id"],
			"additionalProperties": false
		}`),
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		return DeleteWorkspace(ctx, mgr, cfg, args)
	})

	srv.RegisterTool(mcpserver.Tool{
		Name:        "describe_runtime",
		Description: "Return what the container runtime ships: python version, pip packages, fonts, network setting, mount points, and notes. Call once at session start to learn capabilities without trial-and-error.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {},
			"additionalProperties": false
		}`),
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		return DescribeRuntime(ctx, mgr, cfg, args)
	})

	// --- v0.3.0 tools ---

	// ADR-0008: return generated artifacts as MCP image / text content blocks.
	srv.RegisterTool(mcpserver.Tool{
		Name:        "attach_files",
		Description: "Return files from the workspace's /work as MCP content blocks. Images come back as inline image content (PNG/JPG/SVG/GIF/WEBP/BMP); CSV/JSON/TXT/MD as text content; other types as metadata-only (host path + size + sha256). Use this to hand plots and exports back to the user when the client cannot resolve host paths directly.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"workspace_id": {"type":"string"},
				"paths": {
					"type": "array",
					"items": {"type":"string","description":"\"/work/<sub>\" or \"<sub>\" relative to the workspace /work directory."},
					"minItems": 1,
					"maxItems": 16
				}
			},
			"required": ["workspace_id","paths"],
			"additionalProperties": false
		}`),
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		return AttachFiles(ctx, mgr, cfg, args)
	})

	// ADR-0009: table-ize files that already live inside the workspace's
	// /work mount, bypassing allowed_paths.
	srv.RegisterTool(mcpserver.Tool{
		Name:        "load_from_work",
		Description: "Table-ize a CSV/JSON/Parquet file that already lives inside the workspace's /work directory. Use this for files written by execute_code (e.g. a polars write_csv output) when you want them back as DuckDB tables. file_path must start with /work/. For host files, use load_data instead.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"workspace_id": {"type":"string"},
				"file_path":    {"type":"string","description":"Container-absolute path under /work, e.g. \"/work/derived.csv\" or \"/work/subdir/data.parquet\"."},
				"table_name":   {"type":"string","description":"DuckDB table name to create or replace."}
			},
			"required": ["workspace_id","file_path","table_name"],
			"additionalProperties": false
		}`),
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		return LoadFromWork(ctx, mgr, cfg, args)
	})
}
