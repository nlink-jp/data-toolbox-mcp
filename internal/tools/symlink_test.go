package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	symlinkOrSkip(t, secret, filepath.Join(wsWork, "leak.txt"))
	symlinkOrSkip(t, secret, filepath.Join(wsWork, "leak.bin"))
	symlinkOrSkip(t, filepath.Dir(secret), filepath.Join(wsWork, "outside"))

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
	symlinkOrSkip(t, filepath.Join("sub", "real.txt"), filepath.Join(wsWork, "alias.txt"))
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
		workDir, hostWorkDir := newWorkFixture(t)
		outside := t.TempDir()
		symlinkOrSkip(t, outside, filepath.Join(hostWorkDir, "_upload"))
		if err := stageUpload(workDir, "ws", src); err == nil {
			t.Error("stageUpload followed _upload out of the workspace")
		}
		if entries, _ := os.ReadDir(outside); len(entries) != 0 {
			t.Errorf("a file was written outside the workspace: %v", entries)
		}
	})

	t.Run("the destination file is a link to a host file", func(t *testing.T) {
		workDir, hostWorkDir := newWorkFixture(t)
		victim := filepath.Join(t.TempDir(), "victim")
		if err := os.WriteFile(victim, []byte("untouched"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(hostWorkDir, "_upload"), 0o755); err != nil {
			t.Fatal(err)
		}
		symlinkOrSkip(t, victim, filepath.Join(hostWorkDir, "_upload", "data.csv"))
		if err := stageUpload(workDir, "ws", src); err == nil {
			t.Error("stageUpload wrote through a link planted at the destination")
		}
		if got, _ := os.ReadFile(victim); string(got) != "untouched" {
			t.Errorf("the host file was overwritten: %q", got)
		}
	})

	t.Run("an ordinary workspace still gets its copy", func(t *testing.T) {
		workDir, hostWorkDir := newWorkFixture(t)
		if err := stageUpload(workDir, "ws", src); err != nil {
			t.Fatalf("stageUpload: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(hostWorkDir, "_upload", "data.csv"))
		if err != nil || string(got) != "a,b\n1,2\n" {
			t.Errorf("copy = %q, %v", got, err)
		}
	})
}

func TestWriteInWork_DoesNotFollowALinkedDirectory(t *testing.T) {
	workDir, hostWorkDir := newWorkFixture(t)
	outside := t.TempDir()
	symlinkOrSkip(t, outside, filepath.Join(hostWorkDir, "_code"))
	err := writeInWork(workDir, "ws", "_code", "exec-0000.py", strings.NewReader("print(1)"))
	if err == nil {
		t.Fatal("writeInWork followed _code out of the workspace")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("a script was written outside the workspace: %v", entries)
	}
	// The workspace is unusable until the link goes; the error has to say so.
	if !strings.Contains(err.Error(), "/work/_code") || !strings.Contains(err.Error(), "remove it on the host") {
		t.Errorf("the error does not tell the operator what is in the way: %v", err)
	}
}

// The work directory itself can be the link: a caller that names a work_dir
// inside another workspace's /work hands sandboxed code the chance to plant
// <work_dir>/<id>, or <work_dir>/<id>/work. Opening the work directory by its
// path followed it; it is reached through a root on work_dir instead.
func TestWorkDirectoryItselfMayNotBeALink(t *testing.T) {
	secret := plantSecret(t)
	for _, tc := range []struct{ name, link string }{
		{"the workspace directory is a link", "wsA"},
		{"its work directory is a link", filepath.Join("wsA", "work")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			work := t.TempDir()
			if tc.link != "wsA" {
				if err := os.Mkdir(filepath.Join(work, "wsA"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			target := filepath.Dir(secret)
			if tc.link == "wsA" { // the link must lead somewhere that has a work/ with the secret in it
				target = t.TempDir()
				if err := os.Mkdir(filepath.Join(target, "work"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(target, "work", "secret.txt"), []byte(hostSecret), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			symlinkOrSkip(t, target, filepath.Join(work, tc.link))

			if got := attachText(t, work, "secret.txt"); strings.Contains(got, hostSecret) {
				t.Errorf("attach_files read through the linked work directory:\n%s", got)
			}
			if err := writeInWork(work, "wsA", "_code", "x.py", strings.NewReader("print(1)")); err == nil {
				t.Error("writeInWork wrote through the linked work directory")
			}
		})
	}
}

func TestAttachFiles_RejectsWhatIsNotARegularFile(t *testing.T) {
	_, work := setupAttachWorkspace(t, nil)
	fifo := filepath.Join(work, "wsA", "work", "pipe.txt")
	if err := mkfifo(fifo); err != nil {
		t.Skipf("no FIFOs here: %v", err)
	}
	done := make(chan string, 1)
	go func() { done <- attachText(t, work, "pipe.txt") }()
	select {
	case got := <-done:
		if !strings.Contains(got, "not a regular file") {
			t.Errorf("want a rejection, got:\n%s", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("attach_files blocked reading a FIFO; the server answers one request at a time")
	}
}

// newWorkFixture returns a work_dir holding an ensured-looking workspace "ws",
// and the path of its work directory.
func newWorkFixture(t *testing.T) (workDir, hostWorkDir string) {
	t.Helper()
	workDir = t.TempDir()
	hostWorkDir = filepath.Join(workDir, "ws", "work")
	if err := os.MkdirAll(hostWorkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return workDir, hostWorkDir
}

// symlinkOrSkip plants a link, or skips where the platform will not let an
// unprivileged process make one.
func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}
}

// TestWorkspaceFilesGoThroughARoot closes the class rather than the four
// sites found: this package reaches a workspace's files through an os.Root and
// never by joining a path, because a path cannot tell a link from a file. The
// one path-based open left is the host source file of load_data, which is
// guarded by the credential blacklist instead (ResolveInput).
func TestWorkspaceFilesGoThroughARoot(t *testing.T) {
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
// it exists to refuse — including the spellings a deny-list would miss.
func TestPathBasedFileCheckFindsThem(t *testing.T) {
	dir := t.TempDir()
	src := `package tools

import (
	stdos "os"
	"path/filepath"
	"syscall"
)

func a(p string) { _ = stdos.MkdirAll(p, 0o755) }       // aliased import
func b(p string) { _ = stdos.Chmod(p, 0o644) }          // not on any deny-list
func c(p string) { _ = filepath.Walk(p, nil) }          // opens by path, outside os
func d(p string) { _, _ = stdos.OpenRoot(p) }           // a second root, not the one from openWorkRoot
func e(p string) { _ = syscall.Unlink(p) }
func stageUpload(p string) { _, _ = stdos.Open(p) }     // allowed, here only
func openWorkRoot(p string) { _, _ = stdos.OpenRoot(p) } // allowed, here only
func f(p string) string { return filepath.Join(p, "x") } // path arithmetic is fine
`
	if err := os.WriteFile(filepath.Join(dir, "bad.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	violations, _, err := pathBasedFileCalls(dir)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(violations, "\n")
	for _, want := range []string{"a uses os.MkdirAll", "b uses os.Chmod", "c uses path/filepath.Walk", "d uses os.OpenRoot", "imports syscall"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the check did not object to %q; it said:\n%s", want, joined)
		}
	}
	if len(violations) != 5 {
		t.Errorf("want exactly the five refusals above, got %d:\n%s", len(violations), joined)
	}
}
