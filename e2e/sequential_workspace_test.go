//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestE2E_SequentialWorkspaceIsolation verifies that two workspace IDs see
// independent DuckDB state: table t loaded in workspace A is not visible in
// workspace B. This is a sanity check for ADR-0001's per-workspace scoping.
func TestE2E_SequentialWorkspaceIsolation(t *testing.T) {
	binary, ok := requireBinary(t)
	if !ok {
		return
	}
	requirePodman(t)

	dataDir := t.TempDir()
	csv := filepath.Join(dataDir, "iso.csv")
	if err := os.WriteFile(csv, []byte("a\n1\n2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := writeConfig(t, dataDir, t.TempDir(), 30)
	h := Start(t, binary, cfg)

	if _, err := h.Call("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e", "version": "1"},
	}, 5*time.Second); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// Load into workspace A.
	if _, isErr, err := h.CallTool("load_data", map[string]any{
		"workspace_id": "wsA",
		"file_path":    csv,
		"table_name":   "shared",
	}, 60*time.Second); err != nil || isErr {
		t.Fatalf("load wsA: err=%v isErr=%v", err, isErr)
	}

	// Querying the same table from workspace B must fail — wsB has its own
	// DuckDB file where 'shared' does not exist.
	body, isErr, err := h.CallTool("query_data", map[string]any{
		"workspace_id": "wsB",
		"sql":          "SELECT * FROM shared",
	}, 30*time.Second)
	if err != nil {
		t.Fatalf("query wsB: %v", err)
	}
	if !isErr {
		t.Errorf("expected isError=true querying a nonexistent table in a fresh workspace; got: %s", body)
	}
	if !strings.Contains(strings.ToLower(string(body)), "shared") &&
		!strings.Contains(strings.ToLower(string(body)), "not exist") &&
		!strings.Contains(strings.ToLower(string(body)), "catalog") {
		t.Errorf("expected error message to mention the missing table; got: %s", body)
	}
}
