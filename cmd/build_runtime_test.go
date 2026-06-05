package cmd

import (
	"strings"
	"testing"

	dtmruntime "github.com/nlink-jp/data-toolbox-mcp/runtime"
)

// TestEmbeddedDockerfileContents pins the Dockerfile pieces that other
// components rely on: a slim Python base and the four required packages.
// If you bump the Dockerfile, update both this test and the Phase 1 plan.
func TestEmbeddedDockerfileContents(t *testing.T) {
	data, err := dtmruntime.FS.ReadFile("Dockerfile")
	if err != nil {
		t.Fatalf("read embedded Dockerfile: %v", err)
	}
	s := string(data)
	for _, want := range []string{"FROM python:", "duckdb", "pandas", "polars", "pyarrow"} {
		if !strings.Contains(s, want) {
			t.Errorf("embedded Dockerfile missing %q", want)
		}
	}
}
