package workspace

import (
	"context"
	"errors"
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
	work := t.TempDir()
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

	m := NewManager(cfg, newFakeClient(fr), allowAll)
	w, err := m.Ensure(context.Background(), work, "alpha")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if w.ContainerID != "abc123" {
		t.Errorf("container ID: got %q, want abc123", w.ContainerID)
	}
	// The name carries the work directory's digest: the same workspace_id
	// under two work directories is two workspaces, and one long-lived
	// container cannot serve both.
	if want := containerName(work, "alpha"); w.ContainerName != want {
		t.Errorf("container name: got %q, want %q", w.ContainerName, want)
	}
	if other := containerName(t.TempDir(), "alpha"); other == w.ContainerName {
		t.Error("the same id in a different work_dir must not reuse the container")
	}
}

func TestEnsureIsIdempotent(t *testing.T) {
	cfg := config.Default()
	work := t.TempDir()

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

	m := NewManager(cfg, newFakeClient(fr), allowAll)
	w1, err := m.Ensure(context.Background(), work, "beta")
	if err != nil {
		t.Fatalf("ensure 1: %v", err)
	}
	calls1 := fr.callCount()

	w2, err := m.Ensure(context.Background(), work, "beta")
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
	work := t.TempDir()

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

	m := NewManager(cfg, newFakeClient(fr), allowAll)
	w, err := m.Ensure(context.Background(), work, "reattach")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if w.ContainerID != "existing-id" {
		t.Errorf("expected reattachment to existing-id, got %q", w.ContainerID)
	}
}

func TestEnsureRejectsInvalidID(t *testing.T) {
	cfg := config.Default()
	work := t.TempDir()
	m := NewManager(cfg, newFakeClient(&fakeRunner{}), allowAll)

	_, err := m.Ensure(context.Background(), work, "../bad")
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(err.Error(), "invalid workspace_id") {
		t.Errorf("expected 'invalid workspace_id' message: %v", err)
	}
}

func TestReleaseStopsAndRemoves(t *testing.T) {
	cfg := config.Default()
	work := t.TempDir()

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

	m := NewManager(cfg, newFakeClient(fr), allowAll)
	if _, err := m.Ensure(context.Background(), work, "gamma"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := m.Release(context.Background(), work, "gamma"); err != nil {
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
	work := filepath.Join(t.TempDir(), "never-created")
	m := NewManager(cfg, newFakeClient(&fakeRunner{}), allowAll)

	infos, err := m.List(context.Background(), work)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 0 {
		t.Errorf("expected 0 workspaces for absent dir, got %d", len(infos))
	}
}

func TestListReturnsExistingWorkspaces(t *testing.T) {
	cfg := config.Default()
	work := t.TempDir()

	// Seed three workspace dirs and a stray non-workspace entry.
	for _, id := range []string{"alpha", "beta", "gamma"} {
		if err := os.MkdirAll(filepath.Join(work, id, "work"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Stray: invalid name → must be skipped.
	if err := os.MkdirAll(filepath.Join(work, "..stray"), 0o755); err != nil {
		t.Fatal(err)
	}

	fr := &fakeRunner{}
	fr.respond = func(args []string) ([]byte, []byte, int, error) {
		if args[0] == "ps" {
			// One running, one stopped, one absent — keyed by the name filter.
			for _, a := range args {
				if a == exactName(containerName(work, "alpha")) {
					return []byte("running\n"), nil, 0, nil
				}
				if a == exactName(containerName(work, "beta")) {
					return []byte("exited\n"), nil, 0, nil
				}
				if a == exactName(containerName(work, "gamma")) {
					return []byte(""), nil, 0, nil
				}
			}
		}
		return nil, nil, 0, nil
	}
	m := NewManager(cfg, newFakeClient(fr), allowAll)
	infos, err := m.List(context.Background(), work)
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
	work := t.TempDir()
	target := filepath.Join(work, "doomed")
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
	m := NewManager(cfg, newFakeClient(fr), allowAll)

	if err := m.Delete(context.Background(), work, "doomed"); err != nil {
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
	work := t.TempDir()
	target := filepath.Join(work, "lonely")
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
	m := NewManager(cfg, newFakeClient(fr), allowAll)
	if err := m.Delete(context.Background(), work, "lonely"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("disk state still present after Delete (no container case)")
	}
}

func TestDeleteRejectsInvalidID(t *testing.T) {
	cfg := config.Default()
	work := t.TempDir()
	m := NewManager(cfg, newFakeClient(&fakeRunner{}), allowAll)

	err := m.Delete(context.Background(), work, "../escape")
	if err == nil || !strings.Contains(err.Error(), "invalid workspace_id") {
		t.Errorf("expected invalid workspace_id error, got: %v", err)
	}
}

// A directory under workspace_dir is not a workspace just because its name
// would pass ValidateID. The shipped config nests the log directory there
// (log_file = "<workspace_dir>/logs/server.log"), and "logs" was listed as a
// workspace with a host_work_dir that does not exist. An agent told to
// "discover prior workspaces" could then pick it and have execute_code create
// work/ and analysis.duckdb inside the log directory.
func TestListSkipsDirectoriesThatAreNotWorkspaces(t *testing.T) {
	cfg := config.Default()
	work := t.TempDir()

	// A real workspace: Ensure always creates <id>/work.
	if err := os.MkdirAll(filepath.Join(work, "analysis", "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The server's own log directory, holding only log files.
	logs := filepath.Join(work, "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logs, "server.log"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A directory an operator happened to leave there.
	if err := os.MkdirAll(filepath.Join(work, "samples-backup"), 0o755); err != nil {
		t.Fatal(err)
	}

	infos, err := NewManager(cfg, newFakeClient(&fakeRunner{}), allowAll).List(context.Background(), work)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 1 || infos[0].ID != "analysis" {
		ids := make([]string, 0, len(infos))
		for _, i := range infos {
			ids = append(ids, i.ID)
		}
		t.Fatalf("listed %v, want only the real workspace [analysis]", ids)
	}
	// Nothing was created on the way: listing must not materialise a
	// workspace out of a directory that was not one.
	if _, err := os.Stat(filepath.Join(logs, "work")); !os.IsNotExist(err) {
		t.Errorf("listing created work/ inside the log directory: %v", err)
	}
}

// allowAll stands for the server's check in tests of the manager's own
// mechanics; the check itself is workdir.Resolver.CheckBeneath's.
func allowAll(string) error { return nil }

// The directory a call actually uses — mounted, loaded from, deleted — is
// judged first: work_dir=~/.config with workspace_id=gh would otherwise mount
// ~/.config/gh into the container or delete it. The check sees
// <work_dir>/<workspace_id>, its refusal is returned as is, and podman is never
// asked. A Manager without a check refuses.
func TestEveryWorkspaceEntryJudgesTheDirectoryFirst(t *testing.T) {
	cfg := config.Default()
	work := t.TempDir()
	refusal := errors.New("refused")
	var seen []string
	fr := &fakeRunner{}
	m := NewManager(cfg, newFakeClient(fr), func(dir string) error { seen = append(seen, dir); return refusal })
	ctx := context.Background()
	if _, err := m.Ensure(ctx, work, "gh"); !errors.Is(err, refusal) {
		t.Errorf("Ensure = %v, want the check's refusal", err)
	}
	if _, err := m.PreviewDelete(ctx, work, "gh"); !errors.Is(err, refusal) {
		t.Errorf("PreviewDelete = %v, want the check's refusal", err)
	}
	if err := m.Delete(ctx, work, "gh"); !errors.Is(err, refusal) {
		t.Errorf("Delete = %v, want the check's refusal", err)
	}
	want := filepath.Join(work, "gh")
	for _, dir := range seen {
		if dir != want {
			t.Errorf("the check saw %q, want %q", dir, want)
		}
	}
	if len(seen) != 3 {
		t.Errorf("the check ran %d times, want 3", len(seen))
	}
	if len(fr.calls) != 0 {
		t.Errorf("podman was asked %d time(s) for a refused workspace", len(fr.calls))
	}
	if _, err := NewManager(cfg, newFakeClient(fr), nil).Ensure(ctx, work, "ws"); err == nil {
		t.Error("a Manager without a check ensured a workspace")
	}
}
