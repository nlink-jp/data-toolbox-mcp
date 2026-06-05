package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/nlink-jp/data-toolbox-mcp/internal/config"
	"github.com/nlink-jp/data-toolbox-mcp/internal/workspace"
)

// execScript writes the given Python source to <ws>/work/_code/<uuid>.py and
// runs it inside the workspace's container under the configured timeout.
// Temp files are intentionally retained for debugging; a Phase 2 TTL cleanup
// is planned per architecture.md §3.3.
func execScript(ctx context.Context, mgr *workspace.Manager, w *workspace.Workspace, cfg *config.Config, prefix, script string) (*workspace.ExecResult, error) {
	codeDir := filepath.Join(w.HostWorkDir, "_code")
	if err := os.MkdirAll(codeDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir _code: %w", err)
	}
	name := prefix + "-" + randomHex() + ".py"
	hostPath := filepath.Join(codeDir, name)
	if err := os.WriteFile(hostPath, []byte(script), 0o644); err != nil {
		return nil, fmt.Errorf("write script: %w", err)
	}
	timeout := time.Duration(cfg.Container.Limits.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return mgr.Exec(ctx, w, []string{"python", "/work/_code/" + name}, timeout)
}

func randomHex() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
