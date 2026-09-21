package tools

// Instructions is the initialize-time hint a client hands its model before
// any tool list (the MCP `instructions` field; cmd/serve.go sets it). It
// states the work-directory contract (organization ADR-021, project ADR-0011)
// as the schemas in tools.go implement it and points at describe_runtime,
// the one tool that takes no work_dir. Every claim in it is pinned by
// instructions_test.go and workdir_contract_test.go.
const Instructions = "data-toolbox-mcp loads CSV, JSON and Parquet files into a per-workspace DuckDB database " +
	"and lets you query it with SQL or analyse it with Python in a sandboxed Podman container. " +
	"Every tool except describe_runtime takes work_dir: the absolute path of a directory you can read " +
	"back (your session or working directory). It is required, has no default and must already exist; " +
	"the workspace is <work_dir>/<workspace_id>/, and everything the server writes for you lands under " +
	"it, including the DuckDB file and whatever execute_code writes to /work. Pass the same work_dir " +
	"and workspace_id on every call to keep working with the same tables and files, and call " +
	"list_workspaces to find the workspaces already under a work_dir. Call describe_runtime once at " +
	"session start to learn what the container ships (Python version, packages, fonts, network " +
	"setting) and where files written to /work appear on the host."
