package tools

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nlink-jp/data-toolbox-mcp/internal/toolerr"
)

// The guard on a host file is a blacklist floor now, not an operator
// allowlist (ADR-0011): an ordinary path is read, a credential location is
// not, and a symlink cannot smuggle one in.

func TestResolveInputTakesAnOrdinaryPath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "data.csv")
	if err := os.WriteFile(p, []byte("a,b\n1,2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveInput(p)
	if err != nil {
		t.Fatalf("ResolveInput: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	if got != resolved {
		t.Errorf("got %q, want the resolved %q", got, resolved)
	}
}

func TestResolveInputRefusesCredentialLocations(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	p := filepath.Join(home, ".ssh", "config")
	if _, err := os.Stat(p); err != nil {
		t.Skipf("no %s on this host: %v", p, err)
	}
	if _, err := ResolveInput(p); !errors.Is(err, ErrPathNotAllowed) {
		t.Errorf("err = %v, want path_not_allowed", err)
	}
}

// Resolution happens before the check, so a link planted in an ordinary
// directory cannot point into a blacklisted one.
func TestResolveInputFollowsSymlinksBeforeChecking(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	target := filepath.Join(home, ".ssh")
	if _, err := os.Stat(target); err != nil {
		t.Skipf("no %s on this host: %v", target, err)
	}
	link := filepath.Join(t.TempDir(), "innocent.csv")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ResolveInput(link); err == nil {
		t.Error("a symlink into a blacklisted directory must be rejected")
	}
}

func TestResolveInputRejectsMissingPath(t *testing.T) {
	_, err := ResolveInput(filepath.Join(t.TempDir(), "absent.csv"))
	if !errors.Is(err, toolerr.New(toolerr.CodeInvalidArguments, "")) {
		t.Errorf("err = %v, want invalid_arguments", err)
	}
}
