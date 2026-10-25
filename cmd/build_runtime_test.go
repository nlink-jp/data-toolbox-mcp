package cmd

import (
	"regexp"
	"strings"
	"testing"

	"github.com/nlink-jp/data-toolbox-mcp/internal/runtime"
	dtmruntime "github.com/nlink-jp/data-toolbox-mcp/runtime"
)

// pipRequirement matches one requirement in the Dockerfile's pip install,
// quoted or not: duckdb~=1.1, 'pyarrow>=18.0'.
var pipRequirement = regexp.MustCompile(`'?([A-Za-z][A-Za-z0-9_.-]*)((?:~=|>=|==|<=|<|>)[0-9][0-9.]*)'?`)

// describe_runtime advertises the manifest's constraints, and the Dockerfile
// is what is installed. The only other check (the e2e drift test) compares
// names, so a pin changed in one place and not the other went unnoticed —
// and pyarrow and Pillow are floors on purpose (ADR-0007, amended v0.8.3), so
// "tidying" one back to ~= must fail here too.
func TestDockerfilePinsMatchManifest(t *testing.T) {
	data, err := dtmruntime.FS.ReadFile("Dockerfile")
	if err != nil {
		t.Fatalf("read embedded Dockerfile: %v", err)
	}
	s := string(data)
	start := strings.Index(s, "RUN pip install --no-cache-dir")
	if start < 0 {
		t.Fatal("the Dockerfile has no `RUN pip install --no-cache-dir`")
	}
	block := s[start:]
	if end := strings.Index(block, "\n\n"); end >= 0 {
		block = block[:end]
	}
	got := map[string]string{}
	for _, m := range pipRequirement.FindAllStringSubmatch(block, -1) {
		got[m[1]] = m[2]
	}
	want := map[string]string{}
	for _, p := range runtime.Default.Packages {
		want[p.Name] = p.VersionConstraint
	}
	for name, c := range want {
		if got[name] != c {
			t.Errorf("%s: Dockerfile has %q, manifest advertises %q", name, got[name], c)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("%s is installed but missing from the manifest", name)
		}
	}
	for _, name := range []string{"pyarrow", "Pillow"} {
		if !strings.HasPrefix(want[name], ">=") {
			t.Errorf("%s must be a floor (>=), not %q: its fixes ship only in its newest release line", name, want[name])
		}
	}
}

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
