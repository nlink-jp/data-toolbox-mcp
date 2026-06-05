//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// TestE2E_Timeout verifies that a long-running script is interrupted by the
// configured container.limits.timeout_seconds, that the response still arrives
// (no hung MCP request), and that exit_code reflects the forced termination.
//
// We set timeout=3 sec and run a script that sleeps 30 sec.
func TestE2E_Timeout(t *testing.T) {
	binary, ok := requireBinary(t)
	if !ok {
		return
	}
	requirePodman(t)

	cfg := writeConfig(t, t.TempDir(), t.TempDir(), 3)
	h := Start(t, binary, cfg)

	if _, err := h.Call("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e", "version": "1"},
	}, 5*time.Second); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	start := time.Now()
	body, isErr, err := h.CallTool("execute_code", map[string]any{
		"workspace_id": "e2eto",
		"language":     "python",
		"code":         "import time\ntime.sleep(30)\nprint('done')\n",
	}, 30*time.Second)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("execute_code (sleep): %v", err)
	}
	// The response must arrive in well under the script's 30s sleep
	// (timeout is 3s + container startup + a bit of slack).
	if elapsed > 25*time.Second {
		t.Errorf("response took %v, but timeout should have triggered around 3s", elapsed)
	}
	// Either the tool reports isError=true (timeout surfaced as an error) or
	// the result has a non-zero exit_code. Both are acceptable signals that
	// the long-running script was killed.
	if isErr {
		return
	}
	if !strings.Contains(string(body), `"exit_code":0`) {
		return // non-zero exit_code, as expected
	}
	t.Errorf("expected timeout to surface as isError or non-zero exit_code; got: %s", body)
}
