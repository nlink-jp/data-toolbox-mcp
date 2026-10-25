package e2e

// This file carries no build tag on purpose. The suite itself runs only on
// request (it needs podman and a built image), so from v0.6.0 to v0.8.4
// nothing noticed that the config it wrote had stopped loading: ADR-0011 removed
// workspace.workspace_dir and workspace.allowed_paths, the server refused to
// start, and every e2e test failed in a hundredth of a second for anyone who
// opted in. `go test ./...` compiles this file and loads the config through
// the server's own loader, so the next key the server stops accepting fails
// `make test` instead.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/nlink-jp/data-toolbox-mcp/internal/config"
)

// e2eImage is the runtime image the suite drives:
// DATA_TOOLBOX_TEST_IMAGE (the same variable the internal/workspace
// integration tests read), else the server's default.
func e2eImage() string {
	if v := os.Getenv("DATA_TOOLBOX_TEST_IMAGE"); v != "" {
		return v
	}
	return config.Default().Container.Image
}

// writeConfig writes the server config the e2e tests start with and returns
// its path. Workspaces are not configured here: each tool call names its own
// work_dir (ADR-0011).
func writeConfig(t *testing.T, timeoutSec int) string {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	body := fmt.Sprintf(`
[container]
image = %q
stop_on_exit = true

[container.limits]
timeout_seconds = %d
network = "none"

[query]
default_row_limit = 20000
`, e2eImage(), timeoutSec)
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return cfgPath
}

// TestE2EConfigLoads runs in `make test`: the config the e2e suite starts the
// server with must be one the server accepts, and must say what the suite
// relies on.
func TestE2EConfigLoads(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, 7))
	if err != nil {
		t.Fatalf("the e2e suite's config no longer loads, so every e2e test would fail at startup: %v", err)
	}
	if cfg.Container.Limits.TimeoutSeconds != 7 {
		t.Errorf("timeout_seconds = %d, want 7", cfg.Container.Limits.TimeoutSeconds)
	}
	if cfg.Container.Image != e2eImage() {
		t.Errorf("image = %q, want %q", cfg.Container.Image, e2eImage())
	}
	if cfg.Container.Limits.Network != "none" {
		t.Errorf("network = %q, want none (TestE2E_v020_WorkspaceLifecycle asserts it)", cfg.Container.Limits.Network)
	}
}
