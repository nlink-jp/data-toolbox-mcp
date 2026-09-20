package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nlink-jp/data-toolbox-mcp/internal/mcpserver"
)

// /work is writable by whatever execute_code runs, so a symlink in it is input
// from the sandbox. Followed on the host, it points wherever that code chose.
// These tests plant the links the sandbox could plant.

const hostSecret = "HOST-SECRET-7f3a"

// plantSecret writes a file outside every workspace and returns its path.
func plantSecret(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(p, []byte(hostSecret), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func attachText(t *testing.T, work string, paths ...string) string {
	t.Helper()
	cfg, _ := setupAttachWorkspace(t, nil) // only for the config
	res, err := AttachFiles(context.Background(), nil, cfg, attachInput{work: work, wsID: "wsA", paths: paths}.rawArgs())
	if err != nil {
		t.Fatalf("AttachFiles: %v", err)
	}
	var sb strings.Builder
	for _, b := range res.(mcpserver.RawResult).Content {
		sb.WriteString(b.Text)
		sb.WriteString(b.Data)
		sb.WriteString("\n")
	}
	return sb.String()
}

func TestAttachFiles_DoesNotFollowASymlinkOutOfTheWorkspace(t *testing.T) {
	secret := plantSecret(t)
	_, work := setupAttachWorkspace(t, map[string][]byte{"ok.txt": []byte("fine")})
	wsWork := filepath.Join(work, "wsA", "work")

	// A text link, a link whose extension takes the metadata path (size,
	// mtime and sha256 of the target), and a linked directory on the way.
	if err := os.Symlink(secret, filepath.Join(wsWork, "leak.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(wsWork, "leak.bin")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(secret), filepath.Join(wsWork, "outside")); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{"leak.txt", "/work/leak.txt", "leak.bin", "outside/secret.txt"} {
		got := attachText(t, work, p)
		if strings.Contains(got, hostSecret) {
			t.Errorf("%s: the host file's content reached the result", p)
		}
		if strings.Contains(got, "sha256:") {
			t.Errorf("%s: the host file was hashed:\n%s", p, got)
		}
		if !strings.Contains(got, "rejected") {
			t.Errorf("%s: want a rejection, got:\n%s", p, got)
		}
	}
}

// A link that stays inside /work is the workspace's own business and keeps
// working; so does an ordinary file. Without this the test above would pass
// on a tool that rejected everything.
func TestAttachFiles_StillReadsInsideTheWorkspace(t *testing.T) {
	_, work := setupAttachWorkspace(t, map[string][]byte{"sub/real.txt": []byte("inside-content")})
	wsWork := filepath.Join(work, "wsA", "work")
	if err := os.Symlink(filepath.Join("sub", "real.txt"), filepath.Join(wsWork, "alias.txt")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"sub/real.txt", "alias.txt"} {
		if got := attachText(t, work, p); !strings.Contains(got, "inside-content") {
			t.Errorf("%s: want the file's content, got:\n%s", p, got)
		}
	}
}

func TestAttachFiles_NeverEnsuredWorkspaceReportsMissing(t *testing.T) {
	got := attachText(t, t.TempDir(), "anything.txt")
	if !strings.Contains(got, "missing") {
		t.Errorf("want missing, got:\n%s", got)
	}
}

func TestStageUpload_DoesNotWriteThroughASymlink(t *testing.T) {
	src := filepath.Join(t.TempDir(), "data.csv")
	if err := os.WriteFile(src, []byte("a,b\n1,2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("_upload is a link to a host directory", func(t *testing.T) {
		hostWorkDir, outside := t.TempDir(), t.TempDir()
		if err := os.Symlink(outside, filepath.Join(hostWorkDir, "_upload")); err != nil {
			t.Fatal(err)
		}
		if err := stageUpload(hostWorkDir, src); err == nil {
			t.Error("stageUpload followed _upload out of the workspace")
		}
		if entries, _ := os.ReadDir(outside); len(entries) != 0 {
			t.Errorf("a file was written outside the workspace: %v", entries)
		}
	})

	t.Run("the destination file is a link to a host file", func(t *testing.T) {
		hostWorkDir := t.TempDir()
		victim := filepath.Join(t.TempDir(), "victim")
		if err := os.WriteFile(victim, []byte("untouched"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(hostWorkDir, "_upload"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(victim, filepath.Join(hostWorkDir, "_upload", "data.csv")); err != nil {
			t.Fatal(err)
		}
		if err := stageUpload(hostWorkDir, src); err == nil {
			t.Error("stageUpload wrote through a link planted at the destination")
		}
		if got, _ := os.ReadFile(victim); string(got) != "untouched" {
			t.Errorf("the host file was overwritten: %q", got)
		}
	})

	t.Run("an ordinary workspace still gets its copy", func(t *testing.T) {
		hostWorkDir := t.TempDir()
		if err := stageUpload(hostWorkDir, src); err != nil {
			t.Fatalf("stageUpload: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(hostWorkDir, "_upload", "data.csv"))
		if err != nil || string(got) != "a,b\n1,2\n" {
			t.Errorf("copy = %q, %v", got, err)
		}
	})
}

func TestWriteInWork_DoesNotFollowALinkedDirectory(t *testing.T) {
	hostWorkDir, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(hostWorkDir, "_code")); err != nil {
		t.Fatal(err)
	}
	if err := writeInWork(hostWorkDir, "_code", "exec-0000.py", strings.NewReader("print(1)")); err == nil {
		t.Error("writeInWork followed _code out of the workspace")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("a script was written outside the workspace: %v", entries)
	}
}

// TestWorkspaceWritesGoThroughARoot closes the class rather than the four
// sites found: this package reaches a workspace's files through an os.Root and
// never by joining a path, because a path cannot tell a link from a file. The
// one path-based open left is the host source file of load_data, which is
// guarded by the credential blacklist instead (ResolveInput).
func TestWorkspaceWritesGoThroughARoot(t *testing.T) {
	violations, files, err := pathBasedFileCalls(".")
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

// The check has never failed on the tree it guards, so it is shown the calls
// it exists to refuse.
func TestPathBasedFileCheckFindsThem(t *testing.T) {
	dir := t.TempDir()
	src := `package tools

import "os"

func a(p string) { _ = os.MkdirAll(p, 0o755) }
func b(p string) { _ = os.WriteFile(p, nil, 0o644) }
func c(p string) { _, _ = os.ReadFile(p) }
func d(p string) { _, _ = os.Stat(p) }
func stageUpload(p string) { _, _ = os.Open(p) } // the one allowed opener
`
	if err := os.WriteFile(filepath.Join(dir, "bad.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	violations, _, err := pathBasedFileCalls(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 4 {
		t.Errorf("want the four path-based calls refused and stageUpload's os.Open allowed, got %d: %v",
			len(violations), violations)
	}
}
