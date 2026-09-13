package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nlink-jp/data-toolbox-mcp/internal/config"
)

func TestLoadFillsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `
[query]
default_row_limit = 20000
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if cfg.Container.Image != "localhost/data-toolbox-runtime:latest" {
		t.Errorf("default container.image: got %q", cfg.Container.Image)
	}
	if cfg.Query.DefaultRowLimit != 20000 {
		t.Errorf("default_row_limit: got %d, want 20000", cfg.Query.DefaultRowLimit)
	}
	if cfg.Container.Limits.Network != "none" {
		t.Errorf("default network: got %q", cfg.Container.Limits.Network)
	}
	if cfg.Workspace.Dir != "" || len(cfg.Workspace.AllowedPaths) != 0 {
		t.Errorf("the server owns no workspace root and no allowlist any more: %+v", cfg.Workspace)
	}
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `
[workspace]
totally_made_up_key = "oops"
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected unknown-key error, got nil")
	}
	if !strings.Contains(err.Error(), "unknown config keys") {
		t.Errorf("error missing expected message: %v", err)
	}
}

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	cases := map[string]string{
		"~":        home,
		"~/x":      filepath.Join(home, "x"),
		"/abs/p":   "/abs/p",
		"relative": "relative",
		"":         "",
	}
	for in, want := range cases {
		if got := config.ExpandHome(in); got != want {
			t.Errorf("ExpandHome(%q) = %q, want %q", in, got, want)
		}
	}
}

// The two removed keys fail the load by name. An operator who wrote a
// containment list and had it silently dropped would believe it was in force
// (ADR-0011).
func TestLoadRejectsRemovedWorkspaceKeys(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"workspace_dir", "[workspace]\nworkspace_dir = \"~/.data-toolbox\"\n", "workspace.workspace_dir was removed"},
		{"allowed_paths", "[workspace]\nallowed_paths = [\"/tmp/data\"]\n", "workspace.allowed_paths was removed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := config.Load(path)
			if err == nil {
				t.Fatal("a config carrying the removed key must fail to load")
			}
			if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "ADR-0011") {
				t.Errorf("error should name the key and the record: %v", err)
			}
		})
	}
}
