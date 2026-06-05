package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nlink-jp/data-toolbox-mcp/internal/config"
	"github.com/nlink-jp/data-toolbox-mcp/internal/tools"
	"github.com/nlink-jp/data-toolbox-mcp/internal/workspace"
)

// TestIntegrationFullToolFlow drives load_data → query_data → execute_code
// against the real runtime container. Skipped unless
// DATA_TOOLBOX_TEST_PODMAN=1.
func TestIntegrationFullToolFlow(t *testing.T) {
	if os.Getenv("DATA_TOOLBOX_TEST_PODMAN") != "1" {
		t.Skip("set DATA_TOOLBOX_TEST_PODMAN=1 to run podman integration tests")
	}

	wsDir := t.TempDir()
	dataDir := t.TempDir()
	csv := filepath.Join(dataDir, "sample.csv")
	if err := os.WriteFile(csv, []byte("a,b\n1,foo\n2,bar\n3,baz\n"), 0o644); err != nil {
		t.Fatalf("write csv: %v", err)
	}

	cfg := config.Default()
	cfg.Workspace.Dir = wsDir
	cfg.Workspace.AllowedPaths = []string{dataDir}
	// Drop CPU/memory limits to avoid cgroup-config issues on macOS Podman.
	cfg.Container.Limits.CPU = ""
	cfg.Container.Limits.Memory = ""
	cfg.Container.Limits.TimeoutSeconds = 30

	pc := workspace.NewPodmanClient()
	ok, err := pc.ImageExists(context.Background(), cfg.Container.Image)
	if err != nil {
		t.Skipf("podman image exists failed: %v", err)
	}
	if !ok {
		t.Skipf("runtime image %q not built locally; run `data-toolbox-mcp build-runtime` first", cfg.Container.Image)
	}

	mgr := workspace.NewManager(cfg, pc)
	defer mgr.Cleanup(context.Background())

	ctx := context.Background()

	// 1) load_data
	res, err := tools.LoadData(ctx, mgr, cfg, json.RawMessage(`{
		"workspace_id":"itest",
		"file_path":"`+csv+`",
		"table_name":"sample"
	}`))
	if err != nil {
		t.Fatalf("load_data: %v", err)
	}
	ld := res.(tools.LoadDataResult)
	if ld.RowsLoaded != 3 {
		t.Errorf("load_data rows_loaded = %d, want 3", ld.RowsLoaded)
	}
	if len(ld.Schema) != 2 {
		t.Errorf("load_data schema has %d cols, want 2", len(ld.Schema))
	}

	// 2) query_data
	res, err = tools.QueryData(ctx, mgr, cfg, json.RawMessage(`{
		"workspace_id":"itest",
		"sql":"SELECT a, b FROM sample ORDER BY a"
	}`))
	if err != nil {
		t.Fatalf("query_data: %v", err)
	}
	qd := res.(tools.QueryDataResult)
	if qd.RowCount != 3 {
		t.Errorf("query_data row_count = %d, want 3", qd.RowCount)
	}
	if qd.LimitReached {
		t.Errorf("limit unexpectedly reached on a 3-row result")
	}

	// 3) execute_code (positive path)
	res, err = tools.ExecuteCode(ctx, mgr, cfg, json.RawMessage(`{
		"workspace_id":"itest",
		"language":"python",
		"code":"import duckdb\ncon=duckdb.connect('/work/analysis.duckdb')\nprint(con.execute('SELECT count(*) FROM sample').fetchone()[0])\n"
	}`))
	if err != nil {
		t.Fatalf("execute_code: %v", err)
	}
	ec := res.(tools.ExecuteCodeResult)
	if ec.ExitCode != 0 {
		t.Errorf("execute_code exit code = %d, want 0; stderr=%q", ec.ExitCode, ec.Stderr)
	}
	if !strings.Contains(ec.Stdout, "3") {
		t.Errorf("execute_code stdout did not contain row count: %q", ec.Stdout)
	}

	// 4) execute_code rejects unsupported language
	_, err = tools.ExecuteCode(ctx, mgr, cfg, json.RawMessage(`{
		"workspace_id":"itest",
		"language":"bash",
		"code":"echo hi"
	}`))
	if err == nil || !strings.Contains(err.Error(), "unsupported_language") {
		t.Errorf("expected unsupported_language error, got: %v", err)
	}

	// 5) load_data rejects path outside allowed_paths
	outside := filepath.Join(t.TempDir(), "outside.csv")
	os.WriteFile(outside, []byte("a,b\n1,x\n"), 0o644)
	_, err = tools.LoadData(ctx, mgr, cfg, json.RawMessage(`{
		"workspace_id":"itest",
		"file_path":"`+outside+`",
		"table_name":"outside"
	}`))
	if err == nil || !strings.Contains(err.Error(), "path_not_allowed") {
		t.Errorf("expected path_not_allowed error, got: %v", err)
	}
}
