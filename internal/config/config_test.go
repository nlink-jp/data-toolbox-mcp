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
[workspace]
allowed_paths = ["/tmp/data"]
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
	if len(cfg.Workspace.AllowedPaths) != 1 || cfg.Workspace.AllowedPaths[0] != "/tmp/data" {
		t.Errorf("allowed_paths: got %v", cfg.Workspace.AllowedPaths)
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
