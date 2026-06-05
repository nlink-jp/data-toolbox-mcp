package cmd

import (
	"strings"
	"testing"

	dtmruntime "github.com/nlink-jp/data-toolbox-mcp/runtime"
)

// TestEmbeddedDockerfileContents pins the Dockerfile pieces that other
// components rely on. If you bump the Dockerfile, update this test, the
// runtime manifest (internal/runtime/manifest.go), and the v0.2.0 plan.
func TestEmbeddedDockerfileContents(t *testing.T) {
	data, err := dtmruntime.FS.ReadFile("Dockerfile")
	if err != nil {
		t.Fatalf("read embedded Dockerfile: %v", err)
	}
	s := string(data)
	want := []string{
		"FROM python:3.12-slim", // ADR-0007: pinned to 3.12-slim
		// Core data-analysis stack (ADR-0003)
		"duckdb", "pandas", "polars", "pyarrow",
		// Plotting + image stack (ADR-0007)
		"matplotlib", "Pillow",
		// OS-level requirements for CJK rendering (ADR-0007)
		"fonts-noto-cjk", "ca-certificates",
		// matplotlibrc font fallback (ADR-0007)
		"Noto Sans CJK JP", "MATPLOTLIBRC",
	}
	for _, w := range want {
		if !strings.Contains(s, w) {
			t.Errorf("embedded Dockerfile missing %q", w)
		}
	}
}
