//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestE2E_v020_WorkspaceLifecycle drives list / load / describe / delete via
// the live MCP server and verifies the workspace appears and disappears.
func TestE2E_v020_WorkspaceLifecycle(t *testing.T) {
	binary, ok := requireBinary(t)
	if !ok {
		return
	}
	requirePodman(t)

	dataDir := t.TempDir()
	csv := filepath.Join(dataDir, "tiny.csv")
	if err := os.WriteFile(csv, []byte("a,b\n1,x\n2,y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := writeConfig(t, dataDir, t.TempDir(), 30)
	h := Start(t, binary, cfg)

	if _, err := h.Call("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-v0.2.0", "version": "1"},
	}, 5*time.Second); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// 1) tools/list → 6 tools (3 existing + 3 new)
	listRes, err := h.Call("tools/list", nil, 5*time.Second)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	var list struct {
		Tools []struct{ Name string } `json:"tools"`
	}
	_ = json.Unmarshal(listRes, &list)
	want := map[string]bool{
		"load_data": true, "query_data": true, "execute_code": true,
		"list_workspaces": true, "delete_workspace": true, "describe_runtime": true,
	}
	for _, tl := range list.Tools {
		delete(want, tl.Name)
	}
	if len(want) > 0 {
		t.Errorf("tools/list missing: %v", want)
	}

	// 2) describe_runtime → check the manifest shape
	descBody, isErr, err := h.CallTool("describe_runtime", map[string]any{}, 5*time.Second)
	if err != nil || isErr {
		t.Fatalf("describe_runtime: err=%v isErr=%v body=%s", err, isErr, descBody)
	}
	for _, want := range []string{`"python_version":"3.12"`, `"duckdb"`, `"matplotlib"`, `"Noto Sans CJK JP"`, `"network":"none"`, `ARTIFACT EXCHANGE`} {
		if !strings.Contains(string(descBody), want) {
			t.Errorf("describe_runtime missing %q in %s", want, descBody)
		}
	}

	// 3) list_workspaces (before load) — must NOT contain our test id
	beforeBody, _, err := h.CallTool("list_workspaces", map[string]any{}, 10*time.Second)
	if err != nil {
		t.Fatalf("list_workspaces (before): %v", err)
	}
	if strings.Contains(string(beforeBody), `"id":"e2e-v020"`) {
		t.Errorf("e2e-v020 workspace present before load: %s", beforeBody)
	}

	// 4) load_data → creates the workspace
	if _, isErr, err := h.CallTool("load_data", map[string]any{
		"workspace_id": "e2e-v020",
		"file_path":    csv,
		"table_name":   "tiny",
	}, 60*time.Second); err != nil || isErr {
		t.Fatalf("load_data: err=%v isErr=%v", err, isErr)
	}

	// 5) list_workspaces (after load) — must contain our test id with running container
	//    AND its host_work_dir for the workspace (v0.2.1 amendment).
	afterBody, _, err := h.CallTool("list_workspaces", map[string]any{}, 10*time.Second)
	if err != nil {
		t.Fatalf("list_workspaces (after): %v", err)
	}
	if !strings.Contains(string(afterBody), `"id":"e2e-v020"`) ||
		!strings.Contains(string(afterBody), `"container_state":"running"`) {
		t.Errorf("workspace not surfaced as running after load: %s", afterBody)
	}
	if !strings.Contains(string(afterBody), `"host_work_dir":"`) ||
		!strings.Contains(string(afterBody), `e2e-v020/work`) {
		t.Errorf("list_workspaces missing host_work_dir for e2e-v020: %s", afterBody)
	}

	// 5b) execute_code — must return host_work_dir for the workspace (v0.2.1 amendment).
	execBody, isErr, err := h.CallTool("execute_code", map[string]any{
		"workspace_id": "e2e-v020",
		"language":     "python",
		"code":         "print('hello')\n",
	}, 30*time.Second)
	if err != nil || isErr {
		t.Fatalf("execute_code probe: err=%v isErr=%v body=%s", err, isErr, execBody)
	}
	if !strings.Contains(string(execBody), `"host_work_dir":"`) ||
		!strings.Contains(string(execBody), `e2e-v020/work`) {
		t.Errorf("execute_code result missing host_work_dir: %s", execBody)
	}

	// 6) delete_workspace
	if _, isErr, err := h.CallTool("delete_workspace", map[string]any{
		"workspace_id": "e2e-v020",
	}, 30*time.Second); err != nil || isErr {
		t.Fatalf("delete_workspace: err=%v isErr=%v", err, isErr)
	}

	// 7) list_workspaces (after delete) — must not contain our test id
	finalBody, _, err := h.CallTool("list_workspaces", map[string]any{}, 10*time.Second)
	if err != nil {
		t.Fatalf("list_workspaces (after delete): %v", err)
	}
	if strings.Contains(string(finalBody), `"id":"e2e-v020"`) {
		t.Errorf("e2e-v020 workspace still present after delete: %s", finalBody)
	}
}

// TestE2E_v020_JapaneseMatplotlib exercises the ADR-0007 font setup end-to-end
// by asking execute_code to render a Japanese-titled chart with strict warnings.
func TestE2E_v020_JapaneseMatplotlib(t *testing.T) {
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
		"clientInfo":      map[string]any{"name": "e2e-jp", "version": "1"},
	}, 5*time.Second); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	body, isErr, err := h.CallTool("execute_code", map[string]any{
		"workspace_id": "e2e-jp",
		"language":     "python",
		"code": `import warnings
warnings.filterwarnings("error", category=UserWarning)
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
fig, ax = plt.subplots(figsize=(3, 2))
ax.set_title("売上推移")
ax.set_xlabel("月")
ax.set_ylabel("金額")
ax.plot([1, 2, 3], [10, 20, 15])
fig.savefig("/work/jp.png", dpi=80)
print("ok")
`,
	}, 60*time.Second)
	if err != nil {
		t.Fatalf("execute_code: %v", err)
	}
	if isErr {
		t.Fatalf("execute_code marked isError: %s", body)
	}
	if !strings.Contains(string(body), `"exit_code":0`) {
		t.Errorf("Japanese matplotlib smoke failed; expected exit_code:0, got: %s", body)
	}
	if !strings.Contains(string(body), `"stdout":"ok\n"`) {
		t.Errorf("stdout missing 'ok'; got: %s", body)
	}
}

// TestE2E_v020_ManifestDrift compares the package set returned by
// describe_runtime against the actual `pip list` output inside the runtime
// image. Names only — version-pin drift is checked separately via the
// embed test in cmd/.
func TestE2E_v020_ManifestDrift(t *testing.T) {
	binary, ok := requireBinary(t)
	if !ok {
		return
	}
	requirePodman(t)

	cfg := writeConfig(t, t.TempDir(), t.TempDir(), 30)
	h := Start(t, binary, cfg)

	if _, err := h.Call("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-drift", "version": "1"},
	}, 5*time.Second); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	body, _, err := h.CallTool("describe_runtime", map[string]any{}, 5*time.Second)
	if err != nil {
		t.Fatalf("describe_runtime: %v", err)
	}
	var rt struct {
		Packages []struct {
			Name string `json:"name"`
		} `json:"packages"`
		ContainerImage string `json:"container_image"`
	}
	if err := json.Unmarshal(body, &rt); err != nil {
		t.Fatalf("parse describe_runtime: %v", err)
	}

	declared := map[string]bool{}
	for _, p := range rt.Packages {
		declared[strings.ToLower(p.Name)] = true
	}
	if len(declared) == 0 {
		t.Fatalf("describe_runtime returned no packages")
	}

	// Ask the live container for its installed packages.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "podman", "run", "--rm",
		rt.ContainerImage, "pip", "list", "--format=json").Output()
	if err != nil {
		t.Fatalf("podman pip list: %v", err)
	}
	var pipList []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(out, &pipList); err != nil {
		t.Fatalf("parse pip list: %v", err)
	}
	installed := map[string]bool{}
	for _, p := range pipList {
		installed[strings.ToLower(p.Name)] = true
	}

	// Every declared package must actually be installed.
	for name := range declared {
		if !installed[name] {
			t.Errorf("manifest declares %q but it is not installed in %s", name, rt.ContainerImage)
		}
	}
}
