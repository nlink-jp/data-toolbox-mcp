package cmd

import (
	"strings"
	"testing"

	dtmruntime "github.com/nlink-jp/data-toolbox-mcp/runtime"
)

// A rebuild exists to take security updates. The embedded Dockerfile never
// changes between rebuilds, so a cached build reuses the apt and pip layers
// and updates nothing; and the base is a tag, so without a pull policy podman
// keeps whatever python:3.12-slim it pulled first (ADR-0005, amended v0.8.3).
func TestBuildArgsBypassCacheAndRefreshBase(t *testing.T) {
	got := strings.Join(buildArgs("/tmp/ctx", []string{"img:v1", "img:latest"}), " ")
	want := "build --no-cache --pull=newer -t img:v1 -t img:latest /tmp/ctx"
	if got != want {
		t.Errorf("buildArgs = %q, want %q", got, want)
	}
}

// A rebuild must also take fixes to packages already in the base image, which
// installing fonts-noto-cjk alone never upgrades.
func TestDockerfileUpgradesBaseBeforeInstall(t *testing.T) {
	data, err := dtmruntime.FS.ReadFile("Dockerfile")
	if err != nil {
		t.Fatalf("read embedded Dockerfile: %v", err)
	}
	s := string(data)
	upgrade := strings.Index(s, "apt-get upgrade -y")
	install := strings.Index(s, "apt-get install -y --no-install-recommends")
	if upgrade < 0 {
		t.Fatal("the Dockerfile must run apt-get upgrade -y")
	}
	if install < 0 || upgrade > install {
		t.Error("apt-get upgrade must come before the apt-get install")
	}
	if !strings.Contains(s, "export DEBIAN_FRONTEND=noninteractive") {
		t.Error("DEBIAN_FRONTEND must cover every apt-get call in the RUN")
	}
}

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
