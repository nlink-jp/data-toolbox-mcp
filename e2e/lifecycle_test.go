//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestE2E_FullLifecycle exercises the protocol from initialize through every
// tool's happy path. Skipped unless DATA_TOOLBOX_TEST_PODMAN=1.
func TestE2E_FullLifecycle(t *testing.T) {
	binary, ok := requireBinary(t)
	if !ok {
		return
	}
	requirePodman(t)

	dataDir := t.TempDir()
	csv := filepath.Join(dataDir, "sample.csv")
	if err := os.WriteFile(csv, []byte("a,b\n1,foo\n2,bar\n3,baz\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := writeConfig(t, dataDir, t.TempDir(), 30)
	h := Start(t, binary, cfg)

	// initialize
	if _, err := h.Call("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e", "version": "1"},
	}, 5*time.Second); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	_ = h.Notify("notifications/initialized")

	// tools/list
	listRes, err := h.Call("tools/list", nil, 5*time.Second)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	var listOut struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(listRes, &listOut); err != nil {
		t.Fatalf("parse tools/list: %v", err)
	}
	wantTools := map[string]bool{"load_data": true, "query_data": true, "execute_code": true}
	for _, tl := range listOut.Tools {
		delete(wantTools, tl.Name)
	}
	if len(wantTools) != 0 {
		t.Errorf("tools/list missing: %v", wantTools)
	}

	// tools/call load_data
	loadRes, isErr, err := h.CallTool("load_data", map[string]any{
		"workspace_id": "e2elc",
		"file_path":    csv,
		"table_name":   "sample",
	}, 60*time.Second)
	if err != nil {
		t.Fatalf("load_data: %v", err)
	}
	if isErr {
		t.Fatalf("load_data returned isError=true: %s", loadRes)
	}
	if !strings.Contains(string(loadRes), `"rows_loaded":3`) {
		t.Errorf("load_data result missing rows_loaded=3: %s", loadRes)
	}

	// tools/call query_data
	queryRes, isErr, err := h.CallTool("query_data", map[string]any{
		"workspace_id": "e2elc",
		"sql":          "SELECT a, b FROM sample ORDER BY a",
	}, 30*time.Second)
	if err != nil {
		t.Fatalf("query_data: %v", err)
	}
	if isErr {
		t.Fatalf("query_data returned isError=true: %s", queryRes)
	}
	if !strings.Contains(string(queryRes), `"row_count":3`) {
		t.Errorf("query_data row_count != 3: %s", queryRes)
	}
	if !strings.Contains(string(queryRes), `"a":1`) || !strings.Contains(string(queryRes), `"b":"foo"`) {
		t.Errorf("query_data missing row data: %s", queryRes)
	}

	// tools/call execute_code (use DuckDB inside python)
	execRes, isErr, err := h.CallTool("execute_code", map[string]any{
		"workspace_id": "e2elc",
		"language":     "python",
		"code": "import duckdb\n" +
			"con = duckdb.connect('/work/analysis.duckdb')\n" +
			"print(con.execute('SELECT count(*) FROM sample').fetchone()[0])\n",
	}, 30*time.Second)
	if err != nil {
		t.Fatalf("execute_code: %v", err)
	}
	if isErr {
		t.Fatalf("execute_code returned isError=true: %s", execRes)
	}
	if !strings.Contains(string(execRes), `"exit_code":0`) {
		t.Errorf("execute_code exit_code != 0: %s", execRes)
	}
	if !strings.Contains(string(execRes), "3") {
		t.Errorf("execute_code stdout missing row count: %s", execRes)
	}
}

func requireBinary(t *testing.T) (string, bool) {
	t.Helper()
	binary := os.Getenv("DATA_TOOLBOX_TEST_BINARY")
	if binary == "" {
		binary = "../dist/data-toolbox-mcp"
	}
	abs, err := filepath.Abs(binary)
	if err != nil {
		t.Fatalf("abs binary path: %v", err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Skipf("binary not found at %s: %v (run `make build`)", abs, err)
		return "", false
	}
	return abs, true
}

func requirePodman(t *testing.T) {
	t.Helper()
	if os.Getenv("DATA_TOOLBOX_TEST_PODMAN") != "1" {
		t.Skip("set DATA_TOOLBOX_TEST_PODMAN=1 to run podman-driven e2e tests")
	}
}

func writeConfig(t *testing.T, dataDir, wsDir string, timeoutSec int) string {
	t.Helper()
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.toml")
	body := fmt.Sprintf(`
[workspace]
workspace_dir = %q
allowed_paths = [%q]

[container]
image = "localhost/data-toolbox-runtime:latest"
stop_on_exit = true

[container.limits]
timeout_seconds = %d
network = "none"

[query]
default_row_limit = 20000
`, wsDir, dataDir, timeoutSec)
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return cfgPath
}
