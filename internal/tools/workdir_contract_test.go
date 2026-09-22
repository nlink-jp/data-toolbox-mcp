package tools

import (
	"encoding/json"
	"io"
	"regexp"
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
	Register(srv, workspace.NewManager(cfg, workspace.NewPodmanClient(), WorkspaceCheck), cfg)
	tools := srv.Tools()
	// The floor under every per-tool loop: with no tools registered, each
	// contract would pass without having examined anything.
	if len(tools) == 0 {
		t.Fatal("no tools are registered, so every per-tool contract would pass without examining one")
	}
	return tools
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

// The initialize instructions are the first thing the model reads about this
// server — before any tool list — so the contract has to be stated there in
// the terms the schemas use, or a model that reads only this omits an
// argument nearly every tool requires.
func TestInstructionsNameTheWorkDirContract(t *testing.T) {
	for _, want := range []string{"work_dir", "absolute", "required", "no default", "<work_dir>/<workspace_id>/"} {
		if !strings.Contains(Instructions, want) {
			t.Errorf("the initialize instructions do not mention %q", want)
		}
	}
}

func TestInstructionsCarryNoRetiredWorkDirName(t *testing.T) {
	for _, old := range retiredWorkDirNames {
		if strings.Contains(Instructions, old) {
			t.Errorf("the initialize instructions name %q; the name is work_dir", old)
		}
	}
}

// exceptClause is the sentence in which the instructions say which tools do
// not take work_dir.
var exceptClause = regexp.MustCompile(`Every tool except ([a-z_]+(?:(?:, | and )[a-z_]+)*) takes work_dir`)

// "Every tool except X takes work_dir" is a claim about the schemas, and it
// goes false silently: a tool added without work_dir, or X gaining one,
// changes the schemas and leaves the sentence as it was. The exceptions it
// names must be exactly the registered tools that declare no work_dir.
func TestInstructionsNameEveryToolThatTakesNoWorkDir(t *testing.T) {
	m := exceptClause.FindStringSubmatch(Instructions)
	if m == nil {
		t.Fatalf("the instructions no longer say which tools take work_dir in the form %q", exceptClause)
	}
	claimed := map[string]bool{}
	for _, name := range regexp.MustCompile(`, | and `).Split(m[1], -1) {
		claimed[name] = true
	}
	actual := map[string]bool{}
	for _, tool := range registeredTools(t) {
		if !declaredProperties(t, tool)["work_dir"] {
			actual[tool.Name] = true
		}
	}
	for name := range actual {
		if !claimed[name] {
			t.Errorf("tool %q declares no work_dir, but the instructions say every tool except %s takes one", name, m[1])
		}
	}
	for name := range claimed {
		if !actual[name] {
			t.Errorf("the instructions name %q as taking no work_dir, but no registered tool by that name lacks one", name)
		}
	}
}
