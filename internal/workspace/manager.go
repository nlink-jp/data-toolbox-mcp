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

// WorkspaceInfo is the shape returned by Manager.List, designed to match the
// `list_workspaces` MCP tool's contract (ADR-0006).
type WorkspaceInfo struct {
	ID             string    `json:"id"`
	LastUsed       time.Time `json:"last_used"`
	ContainerState string    `json:"container_state"` // "running" / "stopped" / "absent"
	// HostWorkDir is the absolute host path of the workspace's /work mount
	// (added in v0.2.1 per ADR-0006 amendment). Lets the LLM tell the user
	// where artifacts written to /work/<name> actually land on disk.
	HostWorkDir string `json:"host_work_dir"`
}

// List returns metadata for every workspace whose disk state is present under
// workspace_dir. Truth source is disk (ADR-0006); the in-memory map is not
// consulted so workspaces left over from previous server runs are also
// discovered. An absent workspace_dir is not an error — an empty slice is
// returned.
func (m *Manager) List(ctx context.Context) ([]WorkspaceInfo, error) {
	entries, err := os.ReadDir(m.cfg.Workspace.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []WorkspaceInfo{}, nil
		}
		return nil, fmt.Errorf("read workspace_dir: %w", err)
	}
	infos := make([]WorkspaceInfo, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		if err := ValidateID(id); err != nil {
			// Skip non-workspace directories (hidden files, stray dirs, etc.).
			continue
		}
		info := WorkspaceInfo{
			ID:          id,
			HostWorkDir: filepath.Join(m.cfg.Workspace.Dir, id, "work"),
		}
		// last_used: prefer the DuckDB file mtime; fall back to the directory mtime.
		dbPath := filepath.Join(m.cfg.Workspace.Dir, id, "work", "analysis.duckdb")
		if st, err := os.Stat(dbPath); err == nil {
			info.LastUsed = st.ModTime()
		} else if fi, err := e.Info(); err == nil {
			info.LastUsed = fi.ModTime()
		}
		// container_state: ask podman; "absent" is the safe default on error.
		if state, err := m.podman.ContainerState(ctx, "data-toolbox-mcp-"+id); err == nil {
			info.ContainerState = state
		} else {
			info.ContainerState = "absent"
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// ContainerStateOf returns the workspace container's state ("running" /
// "stopped" / "absent") without ensuring the workspace. Used by tools that
// want to surface state without side-effects.
func (m *Manager) ContainerStateOf(ctx context.Context, id string) (string, error) {
	return m.podman.ContainerState(ctx, "data-toolbox-mcp-"+id)
}

// DeletePreview describes what would be removed by Delete, without doing
// anything (ADR-0010 / v0.4.0). Returned by PreviewDelete.
type DeletePreview struct {
	WorkspaceID    string
	ContainerID    string // empty when absent
	ContainerState string // "running" / "stopped" / "absent"
	HostBaseDir    string
	HostWorkDir    string
	HostDBPath     string
	DiskUsageBytes int64
}

// PreviewDelete returns metadata describing what a Delete(id) call would
// remove, without removing anything. Same defense-in-depth ID + path-traversal
// checks as Delete, so it errors on bad input before doing any work.
func (m *Manager) PreviewDelete(ctx context.Context, id string) (*DeletePreview, error) {
	if err := ValidateID(id); err != nil {
		return nil, err
	}
	baseDir := filepath.Join(m.cfg.Workspace.Dir, id)
	cleaned := filepath.Clean(baseDir)
	parentClean := filepath.Clean(m.cfg.Workspace.Dir)
	if filepath.Dir(cleaned) != parentClean {
		return nil, fmt.Errorf("refused: %s is not a direct child of %s", cleaned, parentClean)
	}
	preview := &DeletePreview{
		WorkspaceID: id,
		HostBaseDir: cleaned,
		HostWorkDir: filepath.Join(cleaned, "work"),
		HostDBPath:  filepath.Join(cleaned, "work", "analysis.duckdb"),
	}
	containerName := "data-toolbox-mcp-" + id
	if cid, err := m.podman.FindByName(ctx, containerName); err == nil {
		preview.ContainerID = cid
	}
	if state, err := m.podman.ContainerState(ctx, containerName); err == nil {
		preview.ContainerState = state
	} else {
		preview.ContainerState = "absent"
	}
	preview.DiskUsageBytes = diskUsage(cleaned)
	return preview, nil
}

// diskUsage returns the cumulative byte count of all regular files under root.
// Missing root → 0 (not an error). Walk errors mid-way → best-effort partial.
func diskUsage(root string) int64 {
	var total int64
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// Delete tears down a workspace completely: the container (if any) is
// force-removed and the on-disk state (analysis.duckdb, work/, _upload/,
// _code/, ...) is wiped. Irreversible.
//
// Defense-in-depth: even though ValidateID restricts the id syntax, we
// recompute the cleaned absolute path and verify it is a direct child of
// workspace_dir before calling os.RemoveAll, to make path-traversal bugs
// reachable only by lying about workspace_dir itself.
func (m *Manager) Delete(ctx context.Context, id string) error {
	if err := ValidateID(id); err != nil {
		return err
	}

	baseDir := filepath.Join(m.cfg.Workspace.Dir, id)
	cleaned := filepath.Clean(baseDir)
	parentClean := filepath.Clean(m.cfg.Workspace.Dir)
	if filepath.Dir(cleaned) != parentClean {
		return fmt.Errorf("refused to delete: %s is not a direct child of %s", cleaned, parentClean)
	}

	// Remove the container if it exists. Use FindByName so an absent
	// container is not an error.
	name := "data-toolbox-mcp-" + id
	containerID, err := m.podman.FindByName(ctx, name)
	if err != nil {
		return err
	}
	if containerID != "" {
		if err := m.podman.Remove(ctx, containerID); err != nil {
			return fmt.Errorf("remove container: %w", err)
		}
	}

	// Drop from the in-memory map.
	m.mu.Lock()
	delete(m.workspaces, id)
	m.mu.Unlock()

	// Wipe disk state.
	if err := os.RemoveAll(cleaned); err != nil {
		return fmt.Errorf("remove disk state: %w", err)
	}
	return nil
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
