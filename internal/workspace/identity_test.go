package workspace

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/nlink-jp/data-toolbox-mcp/internal/config"
)

// fakeHost is a podman that remembers its containers and answers
// `ps --filter name=` the way podman does: the value is an unanchored regular
// expression over the container name. The older fake in manager_test.go
// answers every `ps` with one canned id, whatever name was asked for — which
// is how Delete could look up a name Ensure never creates, for every release
// since the work directory joined the identity, with the suite green.
type fakeHost struct {
	mu         sync.Mutex
	next       int
	containers map[string]string // name -> id
	runs       int
}

func newFakeHost() *fakeHost { return &fakeHost{containers: map[string]string{}} }

func (h *fakeHost) Run(_ context.Context, _ string, args ...string) ([]byte, []byte, int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch args[0] {
	case "run":
		name := ""
		for i, a := range args {
			if a == "--name" && i+1 < len(args) {
				name = args[i+1]
			}
		}
		if _, exists := h.containers[name]; exists {
			return nil, []byte("name already in use"), 125, nil
		}
		h.next++
		h.runs++
		id := fmt.Sprintf("cid-%d", h.next)
		h.containers[name] = id
		return []byte(id + "\n"), nil, 0, nil
	case "ps":
		var re *regexp.Regexp
		format := ""
		for i, a := range args {
			if a == "--filter" && i+1 < len(args) && strings.HasPrefix(args[i+1], "name=") {
				re = regexp.MustCompile(strings.TrimPrefix(args[i+1], "name="))
			}
			if a == "--format" && i+1 < len(args) {
				format = args[i+1]
			}
		}
		var out []string
		for name, id := range h.containers {
			if re != nil && !re.MatchString(name) {
				continue
			}
			if format == "{{.State}}" {
				out = append(out, "running")
			} else {
				out = append(out, id)
			}
		}
		return []byte(strings.Join(out, "\n")), nil, 0, nil
	case "rm":
		id := args[len(args)-1]
		for name, cid := range h.containers {
			if cid == id {
				delete(h.containers, name)
				return nil, nil, 0, nil
			}
		}
		return nil, []byte("no such container " + strconv.Quote(id)), 1, nil
	}
	return nil, nil, 0, nil // stop, exec, ...
}

func (h *fakeHost) has(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, cid := range h.containers {
		if cid == id {
			return true
		}
	}
	return false
}

func newHostManager() (*Manager, *fakeHost) {
	h := newFakeHost()
	return NewManager(config.Default(), &PodmanClient{binary: "podman", runner: h}), h
}

func TestDeleteThenEnsureStartsAFreshContainer(t *testing.T) {
	ctx := context.Background()
	m, h := newHostManager()
	work := t.TempDir()

	first, err := m.Ensure(ctx, work, "gamma")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := m.Delete(ctx, work, "gamma"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if h.has(first.ContainerID) {
		t.Fatalf("Delete left the workspace's container %s running", first.ContainerID)
	}

	second, err := m.Ensure(ctx, work, "gamma")
	if err != nil {
		t.Fatalf("ensure after delete: %v", err)
	}
	if second.ContainerID == first.ContainerID {
		t.Errorf("Ensure after Delete returned the deleted workspace's handle (%s)", second.ContainerID)
	}
	if !h.has(second.ContainerID) {
		t.Errorf("Ensure after Delete returned container %s, which does not exist", second.ContainerID)
	}
	if st, err := os.Stat(filepath.Join(work, "gamma", "work")); err != nil || !st.IsDir() {
		t.Errorf("Ensure after Delete did not recreate the work directory: %v", err)
	}
}

func TestReleaseThenEnsureDoesNotReturnTheDeadHandle(t *testing.T) {
	ctx := context.Background()
	m, h := newHostManager()
	work := t.TempDir()

	first, err := m.Ensure(ctx, work, "gamma")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := m.Release(ctx, work, "gamma"); err != nil {
		t.Fatalf("release: %v", err)
	}
	second, err := m.Ensure(ctx, work, "gamma")
	if err != nil {
		t.Fatalf("ensure after release: %v", err)
	}
	if !h.has(second.ContainerID) {
		t.Errorf("Ensure after Release returned container %s (first was %s), which does not exist",
			second.ContainerID, first.ContainerID)
	}
}

// The same workspace_id under two work directories is two workspaces, and an
// id that merely starts the same way is a third. Deleting one must leave the
// others' containers and cached handles alone.
func TestDeleteTouchesOnlyItsOwnWorkspace(t *testing.T) {
	ctx := context.Background()
	m, h := newHostManager()
	workA, workB := t.TempDir(), t.TempDir()

	target, err := m.Ensure(ctx, workA, "gamma")
	if err != nil {
		t.Fatal(err)
	}
	sameIDElsewhere, err := m.Ensure(ctx, workB, "gamma")
	if err != nil {
		t.Fatal(err)
	}
	longerID, err := m.Ensure(ctx, workA, "gamma2")
	if err != nil {
		t.Fatal(err)
	}

	if err := m.Delete(ctx, workA, "gamma"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if h.has(target.ContainerID) {
		t.Errorf("the deleted workspace's container survived")
	}
	for label, w := range map[string]*Workspace{"same id, other work_dir": sameIDElsewhere, "longer id": longerID} {
		if !h.has(w.ContainerID) {
			t.Errorf("%s: Delete removed a container that was not its own", label)
		}
		runsBefore := h.runs
		again, err := m.Ensure(ctx, w.WorkDir, w.ID)
		if err != nil {
			t.Fatalf("%s: ensure: %v", label, err)
		}
		if again != w || h.runs != runsBefore {
			t.Errorf("%s: Delete evicted a cached handle that was not its own", label)
		}
	}
}

func TestPreviewDeleteReportsTheWorkspacesOwnContainer(t *testing.T) {
	ctx := context.Background()
	m, _ := newHostManager()
	work := t.TempDir()

	w, err := m.Ensure(ctx, work, "gamma")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Ensure(ctx, t.TempDir(), "gamma"); err != nil { // a decoy with the same id
		t.Fatal(err)
	}
	p, err := m.PreviewDelete(ctx, work, "gamma")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if p.ContainerID != w.ContainerID {
		t.Errorf("preview names container %q, the workspace's is %q", p.ContainerID, w.ContainerID)
	}
	if p.ContainerState != "running" {
		t.Errorf("preview state = %q, want running", p.ContainerState)
	}
}

// TestWorkspaceIdentityIsSpelledOnce closes the class rather than the four
// sites: a workspace is the pair (work_dir, id), and that pair has exactly one
// spelling as a cache key, one as a container name and one as a podman filter.
// Each of the defects fixed alongside this test was a second spelling.
func TestWorkspaceIdentityIsSpelledOnce(t *testing.T) {
	violations, files, err := identityViolations(".")
	if err != nil {
		t.Fatal(err)
	}
	if files == 0 {
		t.Fatal("parsed no source files: the test would pass without looking at anything")
	}
	for _, v := range violations {
		t.Error(v)
	}
}

// The check above has never failed on the tree it guards, so it is shown a
// file holding each of the three second spellings and must object to all of
// them. A check of absence is worth nothing until it has been seen to find
// what it looks for.
func TestIdentityCheckFindsEachSecondSpelling(t *testing.T) {
	dir := t.TempDir()
	src := `package workspace

func (m *Manager) evictByHand(id string)  { delete(m.workspaces, id) }
func legacyName(id string) string        { return "data-toolbox-mcp-" + id }
func looseFilter(name string) string     { return "name=" + name }
`
	if err := os.WriteFile(filepath.Join(dir, "bad.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	violations, files, err := identityViolations(dir)
	if err != nil {
		t.Fatal(err)
	}
	if files != 1 {
		t.Fatalf("parsed %d files, want 1", files)
	}
	for _, want := range []string{"evictByHand", "legacyName", "looseFilter"} {
		found := false
		for _, v := range violations {
			if strings.Contains(v, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("the check did not object to %s; violations: %v", want, violations)
		}
	}
}

// identityViolations reports every place in dir's non-test sources that
// spells a workspace's identity itself instead of going through the one
// function that owns that spelling.
func identityViolations(dir string) (violations []string, files int, err error) {
	mapTouchers := map[string]bool{"NewManager": true, "lookup": true, "remember": true, "forget": true, "Cleanup": true}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			return nil, files, err
		}
		files++
		for _, decl := range file.Decls {
			fn, _ := decl.(*ast.FuncDecl)
			name := "(package level)"
			if fn != nil {
				name = fn.Name.Name
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.SelectorExpr:
					if x.Sel.Name == "workspaces" && !mapTouchers[name] {
						violations = append(violations, fmt.Sprintf(
							"%s: %s touches m.workspaces directly; go through lookup/remember/forget",
							fset.Position(x.Pos()), name))
					}
				case *ast.BasicLit:
					if x.Kind != token.STRING {
						return true
					}
					lit, _ := strconv.Unquote(x.Value)
					if strings.HasPrefix(lit, "data-toolbox-mcp-") && name != "(package level)" {
						violations = append(violations, fmt.Sprintf(
							"%s: %s spells a container name by hand; use containerName(workDir, id)",
							fset.Position(x.Pos()), name))
					}
					if strings.HasPrefix(lit, "name=") && name != "exactName" {
						violations = append(violations, fmt.Sprintf(
							"%s: %s builds a podman name filter by hand; use exactName",
							fset.Position(x.Pos()), name))
					}
				}
				return true
			})
		}
	}
	return violations, files, nil
}
