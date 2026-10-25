//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestE2E_ErrorPaths covers the error categories the user (LLM client)
// is likely to hit:
//   - unsupported_language (execute_code with language != "python")
//   - path_not_allowed (load_data with a credential-like file_path)
//   - work_dir_required (a call that names no work_dir, ADR-0011)
//   - unknown tool name
func TestE2E_ErrorPaths(t *testing.T) {
	binary := requireE2E(t)

	dataDir := t.TempDir()
	cfg := writeConfig(t, 30)
	work := t.TempDir()
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
		"work_dir":     work,
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

	// 2) path_not_allowed. There is no allowlist any more (ADR-0011); what
	//    load_data refuses is the credential blacklist, and a .env file is on it
	//    whether or not it exists.
	secret := filepath.Join(dataDir, ".env")
	if err := os.WriteFile(secret, []byte("TOKEN=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	body, isErr, err = h.CallTool("load_data", map[string]any{
		"work_dir":     work,
		"workspace_id": "e2eerr",
		"file_path":    secret,
		"table_name":   "secret",
	}, 30*time.Second)
	if err != nil {
		t.Fatalf("load_data (.env): %v", err)
	}
	if !isErr {
		t.Errorf("expected isError=true for a .env file; got: %s", body)
	}
	if !strings.Contains(string(body), "path_not_allowed") {
		t.Errorf("expected path_not_allowed in error message; got: %s", body)
	}

	// 2b) work_dir_required. The suite's own config sets no workspace root,
	//     so a call without work_dir has nowhere to go.
	body, isErr, err = h.CallTool("query_data", map[string]any{
		"workspace_id": "e2eerr",
		"sql":          "SELECT 1",
	}, 30*time.Second)
	if err != nil {
		t.Fatalf("query_data (no work_dir): %v", err)
	}
	if !isErr || !strings.Contains(string(body), "work_dir_required") {
		t.Errorf("expected isError=true with work_dir_required; got isError=%v body=%s", isErr, body)
	}

	// 3) unknown tool name → JSON-RPC error (method not found)
	if _, _, err := h.CallTool("does_not_exist", map[string]any{}, 5*time.Second); err == nil {
		t.Errorf("expected unknown tool to return an RPC error")
	} else if !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("expected 'unknown tool' in error; got: %v", err)
	}
}
