//go:build e2e

package e2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestE2E_v030_AttachFiles_RoundTrip exercises the full path: execute_code
// generates a matplotlib PNG inside /work, then attach_files returns it as
// an inline image content block.
func TestE2E_v030_AttachFiles_RoundTrip(t *testing.T) {
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
		"clientInfo":      map[string]any{"name": "e2e-v030", "version": "1"},
	}, 5*time.Second); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// 1) Generate the PNG.
	_, isErr, err := h.CallTool("execute_code", map[string]any{
		"workspace_id": "e2e-attach",
		"language":     "python",
		"code": `import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
fig, ax = plt.subplots(figsize=(2, 1))
ax.plot([1, 2, 3], [3, 1, 2])
ax.set_title("plot")
fig.savefig("/work/plot.png", dpi=80)
print("ok")
`,
	}, 60*time.Second)
	if err != nil || isErr {
		t.Fatalf("execute_code: err=%v isErr=%v", err, isErr)
	}

	// 2) Attach it. attach_files returns rich MCP content (not the usual
	//    single text block), so we pull the raw result here.
	res, err := h.Call("tools/call", map[string]any{
		"name": "attach_files",
		"arguments": map[string]any{
			"workspace_id": "e2e-attach",
			"paths":        []string{"/work/plot.png", "missing.txt"},
		},
	}, 30*time.Second)
	if err != nil {
		t.Fatalf("attach_files: %v", err)
	}

	var wrap struct {
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Data     string `json:"data"`
			MimeType string `json:"mimeType"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(res, &wrap); err != nil {
		t.Fatalf("parse tools/call result: %v", err)
	}
	if wrap.IsError {
		t.Fatalf("attach_files marked isError; content[0]=%q", wrap.Content[0].Text)
	}

	// Expect: summary text + 1 image (PNG) + 1 text (missing).
	if got, want := len(wrap.Content), 3; got != want {
		t.Fatalf("got %d content blocks, want %d (%+v)", got, want, wrap.Content)
	}
	if wrap.Content[1].Type != "image" {
		t.Errorf("block[1] type = %q, want \"image\"", wrap.Content[1].Type)
	}
	if wrap.Content[1].MimeType != "image/png" {
		t.Errorf("block[1] mimeType = %q, want image/png", wrap.Content[1].MimeType)
	}
	if len(wrap.Content[1].Data) < 100 {
		t.Errorf("block[1] data unexpectedly short (%d bytes b64)", len(wrap.Content[1].Data))
	}
	if !strings.Contains(strings.ToLower(wrap.Content[2].Text), "missing") {
		t.Errorf("block[2] should report missing; got %q", wrap.Content[2].Text)
	}
}

// TestE2E_v030_LoadFromWork_RoundTrip: execute_code emits a CSV in /work,
// load_from_work table-izes it, query_data confirms the count.
func TestE2E_v030_LoadFromWork_RoundTrip(t *testing.T) {
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
		"clientInfo":      map[string]any{"name": "e2e-v030", "version": "1"},
	}, 5*time.Second); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// 1) Write a tiny CSV via execute_code.
	_, isErr, err := h.CallTool("execute_code", map[string]any{
		"workspace_id": "e2e-lfw",
		"language":     "python",
		"code": `with open("/work/derived.csv", "w") as f:
    f.write("a,b\n1,10\n2,20\n3,30\n4,40\n")
print("ok")
`,
	}, 60*time.Second)
	if err != nil || isErr {
		t.Fatalf("execute_code: err=%v isErr=%v", err, isErr)
	}

	// 2) load_from_work it as a DuckDB table.
	loadBody, isErr, err := h.CallTool("load_from_work", map[string]any{
		"workspace_id": "e2e-lfw",
		"file_path":    "/work/derived.csv",
		"table_name":   "derived",
	}, 30*time.Second)
	if err != nil || isErr {
		t.Fatalf("load_from_work: err=%v isErr=%v body=%s", err, isErr, loadBody)
	}
	if !strings.Contains(string(loadBody), `"rows_loaded":4`) {
		t.Errorf("load_from_work rows_loaded != 4: %s", loadBody)
	}

	// 3) Confirm via query_data.
	queryBody, isErr, err := h.CallTool("query_data", map[string]any{
		"workspace_id": "e2e-lfw",
		"sql":          "SELECT SUM(b) AS total FROM derived",
	}, 30*time.Second)
	if err != nil || isErr {
		t.Fatalf("query_data: err=%v isErr=%v body=%s", err, isErr, queryBody)
	}
	if !strings.Contains(string(queryBody), `"total":100`) {
		t.Errorf("query_data total != 100: %s", queryBody)
	}
}

// TestE2E_v030_LoadFromWork_RejectsOutsideWork: the load_from_work tool
// must reject host paths (the load_data path) and any /work traversal.
func TestE2E_v030_LoadFromWork_RejectsOutsideWork(t *testing.T) {
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
		"clientInfo":      map[string]any{"name": "e2e-v030", "version": "1"},
	}, 5*time.Second); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	cases := []struct {
		name  string
		path  string
		hint  string
	}{
		{"host path", "/etc/passwd", "must start with /work/"},
		{"escape", "/work/../escape.csv", "escapes /work"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body, isErr, err := h.CallTool("load_from_work", map[string]any{
				"workspace_id": "e2e-lfw-reject",
				"file_path":    c.path,
				"table_name":   "x",
			}, 10*time.Second)
			if err != nil {
				t.Fatalf("CallTool: %v", err)
			}
			if !isErr {
				t.Errorf("expected isError=true for %q; got body: %s", c.path, body)
			}
			if !strings.Contains(string(body), c.hint) {
				t.Errorf("expected message containing %q; got: %s", c.hint, body)
			}
		})
	}
}
