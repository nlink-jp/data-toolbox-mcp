package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nlink-jp/data-toolbox-mcp/internal/config"
)

// fakeRunner records every command issued and returns canned results.
type fakeRunner struct {
	mu      sync.Mutex
	calls   [][]string
	respond func(args []string) (stdout, stderr []byte, code int, err error)
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{name}, args...))
	f.mu.Unlock()
	if f.respond != nil {
		return f.respond(args)
	}
	return nil, nil, 0, nil
}

func (f *fakeRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func newFakeClient(fr *fakeRunner) *PodmanClient {
	return &PodmanClient{binary: "podman", runner: fr}
}

func TestEnsureCreatesContainer(t *testing.T) {
	cfg := config.Default()
	cfg.Workspace.Dir = t.TempDir()
	cfg.Container.Image = "localhost/test:latest"

	fr := &fakeRunner{}
	fr.respond = func(args []string) ([]byte, []byte, int, error) {
		switch args[0] {
		case "ps":
			return []byte(""), nil, 0, nil // no existing container
		case "run":
			return []byte("abc123\n"), nil, 0, nil
		}
		return nil, nil, 0, nil
	}

	m := NewManager(cfg, newFakeClient(fr))
	w, err := m.Ensure(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if w.ContainerID != "abc123" {
		t.Errorf("container ID: got %q, want abc123", w.ContainerID)
	}
	if w.ContainerName != "data-toolbox-mcp-alpha" {
		t.Errorf("container name: got %q", w.ContainerName)
	}
}

func TestEnsureIsIdempotent(t *testing.T) {
	cfg := config.Default()
	cfg.Workspace.Dir = t.TempDir()

	fr := &fakeRunner{}
	fr.respond = func(args []string) ([]byte, []byte, int, error) {
		switch args[0] {
		case "ps":
			return []byte(""), nil, 0, nil
		case "run":
			return []byte("first\n"), nil, 0, nil
		}
		return nil, nil, 0, nil
	}

	m := NewManager(cfg, newFakeClient(fr))
	w1, err := m.Ensure(context.Background(), "beta")
	if err != nil {
		t.Fatalf("ensure 1: %v", err)
	}
	calls1 := fr.callCount()

	w2, err := m.Ensure(context.Background(), "beta")
	if err != nil {
		t.Fatalf("ensure 2: %v", err)
	}
	if w1 != w2 {
		t.Errorf("Ensure returned a new handle for the same ID")
	}
	if fr.callCount() != calls1 {
		t.Errorf("second Ensure issued extra podman calls (was %d, now %d)", calls1, fr.callCount())
	}
}

func TestEnsureReattachesExisting(t *testing.T) {
	cfg := config.Default()
	cfg.Workspace.Dir = t.TempDir()

	fr := &fakeRunner{}
	fr.respond = func(args []string) ([]byte, []byte, int, error) {
		switch args[0] {
		case "ps":
			return []byte("existing-id\n"), nil, 0, nil
		case "run":
			t.Errorf("Ensure should not have called podman run when container exists")
			return nil, nil, 1, nil
		}
		return nil, nil, 0, nil
	}

	m := NewManager(cfg, newFakeClient(fr))
	w, err := m.Ensure(context.Background(), "reattach")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if w.ContainerID != "existing-id" {
		t.Errorf("expected reattachment to existing-id, got %q", w.ContainerID)
	}
}

func TestEnsureRejectsInvalidID(t *testing.T) {
	cfg := config.Default()
	cfg.Workspace.Dir = t.TempDir()
	m := NewManager(cfg, newFakeClient(&fakeRunner{}))

	_, err := m.Ensure(context.Background(), "../bad")
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(err.Error(), "invalid workspace_id") {
		t.Errorf("expected 'invalid workspace_id' message: %v", err)
	}
}

func TestReleaseStopsAndRemoves(t *testing.T) {
	cfg := config.Default()
	cfg.Workspace.Dir = t.TempDir()

	fr := &fakeRunner{}
	fr.respond = func(args []string) ([]byte, []byte, int, error) {
		switch args[0] {
		case "ps":
			return []byte(""), nil, 0, nil
		case "run":
			return []byte("xyz\n"), nil, 0, nil
		case "stop", "rm":
			return nil, nil, 0, nil
		}
		return nil, nil, 0, nil
	}

	m := NewManager(cfg, newFakeClient(fr))
	if _, err := m.Ensure(context.Background(), "gamma"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := m.Release(context.Background(), "gamma"); err != nil {
		t.Fatalf("release: %v", err)
	}

	sawStop, sawRm := false, false
	for _, c := range fr.calls {
		if len(c) >= 2 && c[1] == "stop" {
			sawStop = true
		}
		if len(c) >= 2 && c[1] == "rm" {
			sawRm = true
		}
	}
	if !sawStop {
		t.Errorf("Release did not issue podman stop")
	}
	if !sawRm {
		t.Errorf("Release did not issue podman rm")
	}
}

// --- v0.2.0 tests (ADR-0006: list_workspaces / delete_workspace) ---

func TestListEmptyWhenDirAbsent(t *testing.T) {
	cfg := config.Default()
	cfg.Workspace.Dir = filepath.Join(t.TempDir(), "never-created")
	m := NewManager(cfg, newFakeClient(&fakeRunner{}))

	infos, err := m.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 0 {
		t.Errorf("expected 0 workspaces for absent dir, got %d", len(infos))
	}
}

func TestListReturnsExistingWorkspaces(t *testing.T) {
	cfg := config.Default()
	cfg.Workspace.Dir = t.TempDir()

	// Seed three workspace dirs and a stray non-workspace entry.
	for _, id := range []string{"alpha", "beta", "gamma"} {
		if err := os.MkdirAll(filepath.Join(cfg.Workspace.Dir, id, "work"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Stray: invalid name → must be skipped.
	if err := os.MkdirAll(filepath.Join(cfg.Workspace.Dir, "..stray"), 0o755); err != nil {
		t.Fatal(err)
	}

	fr := &fakeRunner{}
	fr.respond = func(args []string) ([]byte, []byte, int, error) {
		if args[0] == "ps" {
			// One running, one stopped, one absent — keyed by --filter name=.
			for i, a := range args {
				if a == "name=data-toolbox-mcp-alpha" {
					_ = i
					return []byte("running\n"), nil, 0, nil
				}
				if a == "name=data-toolbox-mcp-beta" {
					return []byte("exited\n"), nil, 0, nil
				}
				if a == "name=data-toolbox-mcp-gamma" {
					return []byte(""), nil, 0, nil
				}
			}
		}
		return nil, nil, 0, nil
	}
	m := NewManager(cfg, newFakeClient(fr))
	infos, err := m.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 3 {
		t.Fatalf("expected 3 workspaces, got %d: %+v", len(infos), infos)
	}

	got := map[string]string{}
	for _, i := range infos {
		got[i.ID] = i.ContainerState
	}
	for id, want := range map[string]string{"alpha": "running", "beta": "stopped", "gamma": "absent"} {
		if got[id] != want {
			t.Errorf("workspace %q container_state = %q, want %q", id, got[id], want)
		}
	}
}

func TestDeleteRemovesContainerAndDisk(t *testing.T) {
	cfg := config.Default()
	cfg.Workspace.Dir = t.TempDir()
	target := filepath.Join(cfg.Workspace.Dir, "doomed")
	if err := os.MkdirAll(filepath.Join(target, "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "work", "analysis.duckdb"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	fr := &fakeRunner{}
	fr.respond = func(args []string) ([]byte, []byte, int, error) {
		if args[0] == "ps" {
			return []byte("cid-doomed\n"), nil, 0, nil
		}
		return nil, nil, 0, nil
	}
	m := NewManager(cfg, newFakeClient(fr))

	if err := m.Delete(context.Background(), "doomed"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("disk state still present after Delete: %v", err)
	}
	sawRm := false
	for _, c := range fr.calls {
		if len(c) >= 2 && c[1] == "rm" {
			sawRm = true
		}
	}
	if !sawRm {
		t.Errorf("Delete did not issue podman rm")
	}
}

func TestDeleteIsIdempotentForAbsentContainer(t *testing.T) {
	cfg := config.Default()
	cfg.Workspace.Dir = t.TempDir()
	target := filepath.Join(cfg.Workspace.Dir, "lonely")
	if err := os.MkdirAll(filepath.Join(target, "work"), 0o755); err != nil {
		t.Fatal(err)
	}

	fr := &fakeRunner{}
	fr.respond = func(args []string) ([]byte, []byte, int, error) {
		if args[0] == "ps" {
			return []byte(""), nil, 0, nil // no container
		}
		if args[0] == "rm" {
			t.Errorf("Delete should not issue podman rm when no container exists")
		}
		return nil, nil, 0, nil
	}
	m := NewManager(cfg, newFakeClient(fr))
	if err := m.Delete(context.Background(), "lonely"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("disk state still present after Delete (no container case)")
	}
}

func TestDeleteRejectsInvalidID(t *testing.T) {
	cfg := config.Default()
	cfg.Workspace.Dir = t.TempDir()
	m := NewManager(cfg, newFakeClient(&fakeRunner{}))

	err := m.Delete(context.Background(), "../escape")
	if err == nil || !strings.Contains(err.Error(), "invalid workspace_id") {
		t.Errorf("expected invalid workspace_id error, got: %v", err)
	}
}
