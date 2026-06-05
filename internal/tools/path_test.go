package tools

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveAndCheck_AllowedDirect(t *testing.T) {
	root := t.TempDir()
	f := filepath.Join(root, "data.csv")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveAndCheck(f, []string{root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// On macOS, /var/folders is a symlink to /private/var/folders, so we
	// compare via EvalSymlinks on the expected too.
	wantAbs, _ := filepath.EvalSymlinks(f)
	if got != wantAbs {
		t.Errorf("got %q, want %q", got, wantAbs)
	}
}

func TestResolveAndCheck_OutsideRejected(t *testing.T) {
	allowed := t.TempDir()
	other := t.TempDir()
	f := filepath.Join(other, "secret.csv")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ResolveAndCheck(f, []string{allowed})
	if !errors.Is(err, ErrPathNotAllowed) {
		t.Errorf("expected ErrPathNotAllowed, got %v", err)
	}
}

func TestResolveAndCheck_SymlinkJailBreak(t *testing.T) {
	allowed := t.TempDir()
	other := t.TempDir()
	target := filepath.Join(other, "real-secret.csv")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// allowed/jailbreak.csv -> ../<other>/real-secret.csv
	link := filepath.Join(allowed, "jailbreak.csv")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	_, err := ResolveAndCheck(link, []string{allowed})
	if !errors.Is(err, ErrPathNotAllowed) {
		t.Errorf("symlink jailbreak should be rejected, got %v", err)
	}
}

func TestResolveAndCheck_AllowedIsItselfSymlink(t *testing.T) {
	real := t.TempDir()
	parent := t.TempDir()
	link := filepath.Join(parent, "alias")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(real, "data.csv")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pass the *symlinked* allowed path; ResolveAndCheck must resolve it.
	if _, err := ResolveAndCheck(f, []string{link}); err != nil {
		t.Errorf("should accept when allowed_paths entry is itself a symlink, got %v", err)
	}
}
