//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestE2E_ErrorPaths covers the three error categories the user (LLM client)
// is likely to hit:
//   - unsupported_language (execute_code with language != "python")
//   - path_not_allowed (load_data with file_path outside allowed_paths)
//   - unknown tool name
func TestE2E_ErrorPaths(t *testing.T) {
	binary, ok := requireBinary(t)
	if !ok {
		return
	}
	requirePodman(t)

	dataDir := t.TempDir()
	cfg := writeConfig(t, dataDir, t.TempDir(), 30)
	h := Start(t, binary, cfg)

	if _, err := h.Call("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e", "version": "1"},
	}, 5*time.Second); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// 1) unsupported_language. The tool error travels as isError=true with the
	//    message inside the content block; not a JSON-RPC error.
	body, isErr, err := h.CallTool("execute_code", map[string]any{
		"workspace_id": "e2eerr",
		"language":     "bash",
		"code":         "echo hi",
	}, 30*time.Second)
	if err != nil {
		t.Fatalf("execute_code (bash): %v", err)
	}
	if !isErr {
		t.Errorf("expected isError=true for unsupported language; got: %s", body)
	}
	if !strings.Contains(string(body), "unsupported_language") {
		t.Errorf("expected message to mention unsupported_language; got: %s", body)
	}

	// 2) path_not_allowed
	outside := filepath.Join(t.TempDir(), "outside.csv")
	if err := os.WriteFile(outside, []byte("a,b\n1,x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body, isErr, err = h.CallTool("load_data", map[string]any{
		"workspace_id": "e2eerr",
		"file_path":    outside,
		"table_name":   "outside",
	}, 30*time.Second)
	if err != nil {
		t.Fatalf("load_data (outside): %v", err)
	}
	if !isErr {
		t.Errorf("expected isError=true for path outside allowed_paths; got: %s", body)
	}
	if !strings.Contains(string(body), "path_not_allowed") {
		t.Errorf("expected path_not_allowed in error message; got: %s", body)
	}

	// 3) unknown tool name → JSON-RPC error (method not found)
	if _, _, err := h.CallTool("does_not_exist", map[string]any{}, 5*time.Second); err == nil {
		t.Errorf("expected unknown tool to return an RPC error")
	} else if !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("expected 'unknown tool' in error; got: %v", err)
	}
}
