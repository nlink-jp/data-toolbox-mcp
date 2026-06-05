//go:build e2e

package e2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestE2E_v040_QueryData_TruncatedTotal generates 20001 rows in a workspace,
// runs a no-LIMIT query, and verifies the result has truncated=true plus
// total=20001 (the extra COUNT picked up the real row count).
func TestE2E_v040_QueryData_TruncatedTotal(t *testing.T) {
	binary, ok := requireBinary(t)
	if !ok {
		return
	}
	requirePodman(t)

	cfg := writeConfig(t, t.TempDir(), t.TempDir(), 120)
	h := Start(t, binary, cfg)

	if _, err := h.Call("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-v040", "version": "1"},
	}, 5*time.Second); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// Seed 20001 rows.
	_, isErr, err := h.CallTool("execute_code", map[string]any{
		"workspace_id": "e2e-v040-tt",
		"language":     "python",
		"code": `import duckdb
con = duckdb.connect("/work/analysis.duckdb")
con.execute("CREATE OR REPLACE TABLE big AS SELECT i AS id FROM range(20001) tbl(i)")
print("ok")
`,
	}, 60*time.Second)
	if err != nil || isErr {
		t.Fatalf("seed execute_code: err=%v isErr=%v", err, isErr)
	}

	// Query without a LIMIT; the auto-LIMIT should trip and total should be
	// populated via the additional COUNT.
	body, isErr, err := h.CallTool("query_data", map[string]any{
		"workspace_id": "e2e-v040-tt",
		"sql":          "SELECT id FROM big",
	}, 60*time.Second)
	if err != nil || isErr {
		t.Fatalf("query: err=%v isErr=%v body=%s", err, isErr, body)
	}
	if !strings.Contains(string(body), `"truncated":true`) {
		t.Errorf("expected truncated:true; got: %s", body)
	}
	if !strings.Contains(string(body), `"total":20001`) {
		t.Errorf("expected total:20001; got: %s", body)
	}
}

// TestE2E_v040_QueryData_TableNotFoundHint loads two tables then queries a
// nonexistent one and asserts the structured error's details include
// missing_table + available_tables_in_this_workspace.
func TestE2E_v040_QueryData_TableNotFoundHint(t *testing.T) {
	binary, ok := requireBinary(t)
	if !ok {
		return
	}
	requirePodman(t)

	cfg := writeConfig(t, t.TempDir(), t.TempDir(), 60)
	h := Start(t, binary, cfg)

	if _, err := h.Call("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-v040", "version": "1"},
	}, 5*time.Second); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// Two existing tables via execute_code.
	if _, isErr, err := h.CallTool("execute_code", map[string]any{
		"workspace_id": "e2e-v040-hint",
		"language":     "python",
		"code": `import duckdb
con = duckdb.connect("/work/analysis.duckdb")
con.execute("CREATE OR REPLACE TABLE products AS SELECT 1 AS id, 'a' AS name")
con.execute("CREATE OR REPLACE TABLE logs AS SELECT 1 AS id, 'x' AS level")
print("ok")
`,
	}, 60*time.Second); err != nil || isErr {
		t.Fatalf("seed: err=%v isErr=%v", err, isErr)
	}

	// Query a missing table.
	body, isErr, err := h.CallTool("query_data", map[string]any{
		"workspace_id": "e2e-v040-hint",
		"sql":          "SELECT * FROM sales",
	}, 30*time.Second)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if !isErr {
		t.Fatalf("expected isError=true for missing table; got: %s", body)
	}
	for _, want := range []string{
		`"code":"script_failed"`,
		`"missing_table":"sales"`,
		`"available_tables_in_this_workspace"`,
		`"products"`,
		`"logs"`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("expected %q in error body; got: %s", want, body)
		}
	}
}

// TestE2E_v040_DeleteWorkspace_DryRun verifies that dry_run=true returns the
// preview without deleting, and that the same workspace is still usable
// afterwards.
func TestE2E_v040_DeleteWorkspace_DryRun(t *testing.T) {
	binary, ok := requireBinary(t)
	if !ok {
		return
	}
	requirePodman(t)

	cfg := writeConfig(t, t.TempDir(), t.TempDir(), 60)
	h := Start(t, binary, cfg)

	if _, err := h.Call("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-v040", "version": "1"},
	}, 5*time.Second); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// Seed via execute_code so the workspace has some on-disk bytes.
	if _, isErr, err := h.CallTool("execute_code", map[string]any{
		"workspace_id": "e2e-v040-dry",
		"language":     "python",
		"code":         "open('/work/seed.csv','w').write('a,b\\n1,2\\n')\nprint('ok')\n",
	}, 60*time.Second); err != nil || isErr {
		t.Fatalf("seed: err=%v isErr=%v", err, isErr)
	}

	// dry_run=true → preview only.
	body, isErr, err := h.CallTool("delete_workspace", map[string]any{
		"workspace_id": "e2e-v040-dry",
		"dry_run":      true,
	}, 30*time.Second)
	if err != nil || isErr {
		t.Fatalf("dry_run: err=%v isErr=%v body=%s", err, isErr, body)
	}
	for _, want := range []string{
		`"would_delete":true`,
		`"workspace_id":"e2e-v040-dry"`,
		`"container_state":`,
		`"host_paths":`,
		`"disk_usage_bytes":`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("expected %q in dry_run body; got: %s", want, body)
		}
	}
	// disk_usage_bytes should be > 0 because we wrote a seed file.
	var p struct {
		DiskUsage int64 `json:"disk_usage_bytes"`
	}
	_ = json.Unmarshal(body, &p)
	if p.DiskUsage == 0 {
		t.Errorf("expected disk_usage_bytes > 0; got 0")
	}

	// Verify the workspace is still alive afterwards.
	body, _, err = h.CallTool("list_workspaces", map[string]any{}, 10*time.Second)
	if err != nil {
		t.Fatalf("list_workspaces: %v", err)
	}
	if !strings.Contains(string(body), `"id":"e2e-v040-dry"`) {
		t.Errorf("workspace should still exist after dry_run; got: %s", body)
	}
}

// TestE2E_v040_DescribeWorkspace_RoundTrip loads two tables via execute_code
// then verifies describe_workspace returns both with their schemas.
func TestE2E_v040_DescribeWorkspace_RoundTrip(t *testing.T) {
	binary, ok := requireBinary(t)
	if !ok {
		return
	}
	requirePodman(t)

	cfg := writeConfig(t, t.TempDir(), t.TempDir(), 60)
	h := Start(t, binary, cfg)

	if _, err := h.Call("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-v040", "version": "1"},
	}, 5*time.Second); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	if _, isErr, err := h.CallTool("execute_code", map[string]any{
		"workspace_id": "e2e-v040-desc",
		"language":     "python",
		"code": `import duckdb
con = duckdb.connect("/work/analysis.duckdb")
con.execute("CREATE OR REPLACE TABLE sales AS SELECT 1 AS order_id, '2026-01-01'::DATE AS dt, 100 AS amount")
con.execute("CREATE OR REPLACE TABLE products AS SELECT 'P001' AS product_id, 'Widget' AS name")
print("ok")
`,
	}, 60*time.Second); err != nil || isErr {
		t.Fatalf("seed: err=%v isErr=%v", err, isErr)
	}

	body, isErr, err := h.CallTool("describe_workspace", map[string]any{
		"workspace_id": "e2e-v040-desc",
	}, 30*time.Second)
	if err != nil || isErr {
		t.Fatalf("describe_workspace: err=%v isErr=%v body=%s", err, isErr, body)
	}

	for _, want := range []string{
		`"workspace_id":"e2e-v040-desc"`,
		`"host_work_dir":"`,
		`"container_state":`,
		`"tables":`,
		`"name":"sales"`,
		`"name":"products"`,
		`"name":"order_id"`,
		`"name":"product_id"`,
		// Column types come from DuckDB's DESCRIBE; the exact type for
		// literal "100 AS amount" is INTEGER (not BIGINT). We only assert
		// that at least one INTEGER and one VARCHAR column appears.
		`"type":"INTEGER"`,
		`"type":"VARCHAR"`,
		// And the DATE column from "2026-01-01"::DATE.
		`"type":"DATE"`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("expected %q in describe_workspace body; got: %s", want, body)
		}
	}
}
