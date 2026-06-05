// Package workspace manages the per-workspace_id lifecycle: Podman container,
// host-side directory layout, and DuckDB file path. Per ADR-0001, workspace_id
// is supplied by the LLM/client and scopes the state.
package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/nlink-jp/data-toolbox-mcp/internal/config"
)

// Workspace is the in-memory handle for a workspace_id.
type Workspace struct {
	ID            string
	ContainerID   string
	ContainerName string // data-toolbox-mcp-<id>
	HostBaseDir   string // <workspace_dir>/<id>
	HostWorkDir   string // <workspace_dir>/<id>/work
	HostDBPath    string // <workspace_dir>/<id>/analysis.duckdb
	LastUsed      time.Time
}

// Manager owns every workspace's lifecycle.
type Manager struct {
	mu         sync.Mutex
	cfg        *config.Config
	podman     *PodmanClient
	workspaces map[string]*Workspace
}

// NewManager wires a manager around the config and a podman client.
func NewManager(cfg *config.Config, podman *PodmanClient) *Manager {
	return &Manager{
		cfg:        cfg,
		podman:     podman,
		workspaces: make(map[string]*Workspace),
	}
}

// Ensure returns a running workspace for id, creating the container if needed.
// Idempotent: calling Ensure twice for the same id returns the same handle.
// If a container with the expected name already exists (e.g. across server
// restart), it is reattached rather than recreated.
func (m *Manager) Ensure(ctx context.Context, id string) (*Workspace, error) {
	if err := ValidateID(id); err != nil {
		return nil, err
	}

	m.mu.Lock()
	if w, ok := m.workspaces[id]; ok {
		w.LastUsed = time.Now()
		m.mu.Unlock()
		return w, nil
	}
	m.mu.Unlock()

	w := &Workspace{
		ID:            id,
		ContainerName: "data-toolbox-mcp-" + id,
		HostBaseDir:   filepath.Join(m.cfg.Workspace.Dir, id),
		HostWorkDir:   filepath.Join(m.cfg.Workspace.Dir, id, "work"),
		// DuckDB file lives inside the work/ directory so it is exposed via
		// the single bind-mount of work/ → /work, and DuckDB can create it
		// on first connect without us pre-touching an empty (invalid) file.
		HostDBPath: filepath.Join(m.cfg.Workspace.Dir, id, "work", "analysis.duckdb"),
	}
	if err := os.MkdirAll(w.HostWorkDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir workdir: %w", err)
	}

	// Reuse existing container if present.
	containerID, err := m.podman.FindByName(ctx, w.ContainerName)
	if err != nil {
		return nil, err
	}
	if containerID == "" {
		containerID, err = m.podman.Run(ctx, RunOpts{
			Image:       m.cfg.Container.Image,
			Name:        w.ContainerName,
			WorkspaceID: id,
			CPU:         m.cfg.Container.Limits.CPU,
			Memory:      m.cfg.Container.Limits.Memory,
			Network:     m.cfg.Container.Limits.Network,
			Userns:      defaultUserns(),
			Mounts: []Mount{
				// /work bind-mount alone exposes the DuckDB file at
				// /work/analysis.duckdb (it lives inside HostWorkDir).
				{HostPath: w.HostWorkDir, ContainerPath: "/work"},
			},
		})
		if err != nil {
			return nil, err
		}
	}
	w.ContainerID = containerID
	w.LastUsed = time.Now()

	m.mu.Lock()
	m.workspaces[id] = w
	m.mu.Unlock()
	return w, nil
}

// Release stops and removes the container for id. Disk state (analysis.duckdb,
// work/) is preserved so the workspace can be ensured again later.
func (m *Manager) Release(ctx context.Context, id string) error {
	m.mu.Lock()
	w, ok := m.workspaces[id]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	delete(m.workspaces, id)
	m.mu.Unlock()

	if err := m.podman.Stop(ctx, w.ContainerID); err != nil {
		// Continue to rm even if stop failed (container may already be stopped).
		_ = err
	}
	return m.podman.Remove(ctx, w.ContainerID)
}

// Exec runs cmd inside the container backing w, with an optional per-call
// timeout. A timeout of 0 means no timeout. Returns the container's stdout,
// stderr, and exit code. A non-zero exit code is not itself an error — the
// caller checks ExitCode (e.g. script errors are propagated via stderr).
func (m *Manager) Exec(ctx context.Context, w *Workspace, cmd []string, timeout time.Duration) (*ExecResult, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return m.podman.Exec(ctx, ExecOpts{
		ContainerID: w.ContainerID,
		Cmd:         cmd,
	})
}

// defaultUserns returns the --userns spec to pass to podman so the container
// can write to bind-mounted host files. On rootless Podman we map the current
// host user to the container's USER (uid 1000 per the runtime Dockerfile).
// Returns empty when running as root (no remap needed).
func defaultUserns() string {
	if os.Getuid() <= 0 {
		return ""
	}
	return "keep-id:uid=1000,gid=1000"
}

// Cleanup stops every tracked workspace. Intended for graceful shutdown.
// Returns the first error encountered but always attempts every workspace.
func (m *Manager) Cleanup(ctx context.Context) error {
	m.mu.Lock()
	ids := make([]string, 0, len(m.workspaces))
	for id := range m.workspaces {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	var firstErr error
	for _, id := range ids {
		if err := m.Release(ctx, id); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
