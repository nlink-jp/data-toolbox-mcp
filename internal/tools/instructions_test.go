package tools

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/nlink-jp/data-toolbox-mcp/internal/mcpserver"
)

// declaredProperties returns the argument names a tool's input schema
// declares.
func declaredProperties(t *testing.T, tool mcpserver.Tool) map[string]bool {
	t.Helper()
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
		t.Fatalf("%s: input schema is not valid JSON: %v", tool.Name, err)
	}
	out := map[string]bool{}
	for name := range schema.Properties {
		out[name] = true
	}
	return out
}

// snakeName matches a snake_case word, which is how every tool and argument
// name on this server is spelled.
var snakeName = regexp.MustCompile(`\b[a-z][a-z0-9]*(?:_[a-z0-9]+)+\b`)

// A tool name in the instructions is an instruction to call it. Prose is not
// compiled, so a renamed or withdrawn tool would keep being recommended
// there — and a model that calls it gets "unknown tool". Every snake_case
// word the instructions use must therefore be a registered tool or an
// argument some registered tool declares; that list is read from the server,
// not written down here.
func TestInstructionsNameOnlyRegisteredToolsAndArguments(t *testing.T) {
	isTool := map[string]bool{}
	isArg := map[string]bool{}
	for _, tool := range registeredTools(t) {
		isTool[tool.Name] = true
		for name := range declaredProperties(t, tool) {
			isArg[name] = true
		}
	}
	toolsNamed := 0
	for _, word := range snakeName.FindAllString(Instructions, -1) {
		switch {
		case isTool[word]:
			toolsNamed++
		case isArg[word]:
		default:
			t.Errorf("the instructions name %q, which is neither a registered tool "+
				"nor an argument any registered tool declares", word)
		}
	}
	// The floor: instructions that name no tool pass the loop above without
	// having been checked against anything.
	if toolsNamed == 0 {
		t.Error("the instructions name no registered tool, so nothing above was checked")
	}
}

// describe_runtime is the tool a model calls to learn what the container can
// do instead of finding out by trial and error; the instructions are where it
// learns to call it.
func TestInstructionsPointAtDescribeRuntime(t *testing.T) {
	if !strings.Contains(Instructions, "Call describe_runtime") {
		t.Error("the initialize instructions do not tell the model to call describe_runtime")
	}
}
