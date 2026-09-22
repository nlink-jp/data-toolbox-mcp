package tools

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nlink-jp/data-toolbox-mcp/internal/toolerr"
)

// The layer these tests observe is resolveWorkDir — the single function every
// tool in this package calls to turn a `work_dir` argument into a validated
// directory, denied list and all. The resolver's own denial mechanics are
// covered a layer below, in internal/workdir. A test that built its own
// Resolver{Denied: ...} would prove the mechanism and keep passing with the
// wiring deleted, which is the defect this file exists to catch: eight tools
// each wrote `workdir.Resolver{}` and all eight ran with an empty Denied.

// wantDenied asserts that dir is refused with work_dir_denied. It reports an
// accepted directory in those words, because "accepted" is the failure the
// reader needs, not "nil is not a structured error".
func wantDenied(t *testing.T, dir string) {
	t.Helper()
	_, err := resolveWorkDir(context.Background(), dir)
	if err == nil {
		t.Fatalf("resolveWorkDir(%q) accepted the server's own directory; want %s",
			dir, toolerr.CodeWorkDirDenied)
	}
	var te *toolerr.Error
	if !errors.As(err, &te) {
		t.Fatalf("resolveWorkDir(%q) = %v, which is not a structured tool error", dir, err)
	}
	if te.Code != toolerr.CodeWorkDirDenied {
		t.Errorf("resolveWorkDir(%q) = %s, want %s", dir, te.Code, toolerr.CodeWorkDirDenied)
	}
}

// serverConfigDir points HOME at a directory this test owns and creates the
// server's own config directory inside it. Nothing here restates the denied
// list — it asks the same expression the server uses.
func serverConfigDir(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	dirs := serverOwnedDirs()
	if len(dirs) == 0 {
		t.Fatal("serverOwnedDirs() is empty with HOME set")
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dirs[0]
}

func TestWorkDirRefusesServerConfigDir(t *testing.T) {
	wantDenied(t, serverConfigDir(t))
}

// A subdirectory is the obvious way around a check that only compares the
// directory itself.
func TestWorkDirRefusesInsideServerConfigDir(t *testing.T) {
	inside := filepath.Join(serverConfigDir(t), "ws")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	wantDenied(t, inside)
}

func TestWorkDirAcceptsOrdinaryDir(t *testing.T) {
	serverConfigDir(t)
	ordinary := t.TempDir()
	got, err := resolveWorkDir(context.Background(), ordinary)
	if err != nil {
		t.Fatalf("resolveWorkDir(%q) = %v, want accepted", ordinary, err)
	}
	want, err := filepath.EvalSymlinks(ordinary)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("resolveWorkDir = %q, want the symlink-resolved %q", got, want)
	}
}

// TestOnlyOnePlaceConstructsAResolver closes the class rather than the eight
// instances of it. Every tool once built its own `workdir.Resolver{}` with an
// empty Denied list and none of them denied anything. The rule is a property
// of the package — one construction site, a workdir.NewResolver call in
// workdir.go, and no Resolver literal anywhere (a literal is the zero value,
// which refuses every call) — and this walks the syntax trees to hold it, so
// the next tool cannot reintroduce the defect by copying its neighbour.
func TestOnlyOnePlaceConstructsAResolver(t *testing.T) {
	const allowed = "workdir.go"

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) == 0 {
		t.Fatal("parsed no packages; the test is watching nothing")
	}

	isWorkdir := func(sel *ast.SelectorExpr, name string) bool {
		id, ok := sel.X.(*ast.Ident)
		return ok && id.Name == "workdir" && sel.Sel.Name == name
	}
	calls, literals := map[string]int{}, map[string]int{}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CallExpr:
					if sel, ok := x.Fun.(*ast.SelectorExpr); ok && isWorkdir(sel, "NewResolver") {
						calls[filepath.Base(name)]++
					}
				case *ast.CompositeLit:
					if sel, ok := x.Type.(*ast.SelectorExpr); ok && isWorkdir(sel, "Resolver") {
						literals[filepath.Base(name)]++
					}
				}
				return true
			})
		}
	}

	if calls[allowed] == 0 {
		t.Errorf("no workdir.NewResolver call in %s; the single construction "+
			"point moved and this test no longer guards anything", allowed)
	}
	for name, n := range calls {
		if name != allowed {
			t.Errorf("%s calls workdir.NewResolver %d time(s); every tool must "+
				"go through resolveWorkDir in %s", name, n, allowed)
		}
	}
	for name, n := range literals {
		t.Errorf("%s writes %d workdir.Resolver literal(s): the zero value refuses "+
			"every call; go through resolveWorkDir in %s", name, n, allowed)
	}
}
