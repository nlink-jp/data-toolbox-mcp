package tools

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/nlink-jp/data-toolbox-mcp/internal/config"
	"github.com/nlink-jp/data-toolbox-mcp/internal/mcpserver"
	"github.com/nlink-jp/data-toolbox-mcp/internal/transport"
	"github.com/nlink-jp/data-toolbox-mcp/internal/workspace"
)

// registeredTools is what tools/list shows the model.
func registeredTools(t *testing.T) []mcpserver.Tool {
	t.Helper()
	srv := mcpserver.New("data-toolbox-mcp", "test",
		transport.NewStdioTransport(strings.NewReader(""), io.Discard), nil)
	cfg := config.Default()
	Register(srv, workspace.NewManager(cfg, workspace.NewPodmanClient()), cfg)
	return srv.Tools()
}

// The work-directory contract (organization ADR-021, project ADR-0011) is a
// rule about every tool, not about one of them. Stated only in prose it gets
// re-decided by whoever adds the next tool — and prose drifts silently
// besides, because nothing compiles it. Both halves are pinned here.

var retiredWorkDirNames = []string{"workspace_root", "workspaceRoot", "workspace_dir"}

func TestNoToolSchemaOrDescriptionCarriesARetiredWorkDirName(t *testing.T) {
	for _, tool := range registeredTools(t) {
		for _, old := range retiredWorkDirNames {
			if strings.Contains(string(tool.InputSchema), `"`+old+`"`) {
				t.Errorf("tool %q declares %q; the name is work_dir", tool.Name, old)
			}
			if strings.Contains(tool.Description, old) {
				t.Errorf("tool %q describes itself with %q; the name is work_dir", tool.Name, old)
			}
		}
	}
}

// An optional work directory is an invitation to fall back to a server-owned
// default, which is the failure the contract removes. chrome-pilot shipped
// exactly that: handlers that required it, schemas that said optional.
func TestWorkDirIsRequiredWhereverItIsDeclared(t *testing.T) {
	for _, tool := range registeredTools(t) {
		var schema struct {
			Required   []string                   `json:"required"`
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatalf("%s: input schema is not valid JSON: %v", tool.Name, err)
		}
		if _, declared := schema.Properties["work_dir"]; !declared {
			continue
		}
		found := false
		for _, r := range schema.Required {
			if r == "work_dir" {
				found = true
			}
		}
		if !found {
			t.Errorf("tool %q declares work_dir but does not require it: an optional "+
				"work directory is an invitation to invent a default", tool.Name)
		}
	}
}

// TestEveryRequiredNameIsDeclared is the regression for a tool list that a
// strict client refuses outright. Vertex AI validates `required` against
// `properties` and answers a whole tools/list with
// "schema at top-level requires unspecified property 'work_dir'" — one bad
// schema and the session cannot start at all (2026-09-14, gem-agent).
//
// The existing contract test checks the other direction (declared => required)
// and is blind to this one; JSON Schema itself permits it, so nothing else
// catches it either.
func TestEveryRequiredNameIsDeclared(t *testing.T) {
	for _, tool := range registeredTools(t) {
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatalf("%s: input schema is not valid JSON: %v", tool.Name, err)
		}
		for _, name := range schema.Required {
			if _, ok := schema.Properties[name]; !ok {
				t.Errorf("tool %q requires %q but does not declare it in properties: "+
					"a strict client refuses the whole tool list", tool.Name, name)
			}
		}
	}
}
