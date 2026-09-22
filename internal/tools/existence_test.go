package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nlink-jp/data-toolbox-mcp/internal/config"
	"github.com/nlink-jp/data-toolbox-mcp/internal/toolerr"
)

// Whether a file exists is never the difference between two answers. Each case
// names one path twice — once while a file is there and once after it is
// removed — and the whole answer must be the same both times; where the path
// is one the floor refuses, both must be that refusal. Otherwise "not found"
// against "refused" tells the caller which secrets exist (knowledge:
// security.md, "Compare places by identity, not by name").
//
// The layer observed is the tool call, the answer a caller receives. The home
// directory is a temporary one: nothing is created, read or written in a real
// credential directory (pathguard still lists the account's own, for the links
// inside them).
func TestExistenceIsNotRevealedByLoadData(t *testing.T) {
	base := realDir(t, t.TempDir())
	home := filepath.Join(base, "home")
	t.Setenv("HOME", home)
	dot := filepath.Join(home, "dotfiles", "config")
	sync := filepath.Join(base, "sync")
	other := filepath.Join(base, "other")
	for _, d := range []string{
		filepath.Join(dot, "gcloud"), filepath.Join(home, ".aws"), filepath.Join(home, ".docker"),
		filepath.Join(home, ".ssh"), sync, other,
	} {
		mkdirAll(t, d)
	}
	symlinkOrSkip(t, dot, filepath.Join(home, ".config"))
	symlinkOrSkip(t, filepath.Join(sync, "ssh_config"), filepath.Join(home, ".ssh", "config"))
	symlinkOrSkip(t, filepath.Join(home, ".aws", "planted.csv"), filepath.Join(other, "lnk_file.csv"))
	symlinkOrSkip(t, filepath.Join(home, ".aws"), filepath.Join(other, "lnk_dir"))

	// work_dir is left out: a path the floor lets through is then answered
	// work_dir_required, before anything reaches a container.
	answer := func(file string) string {
		raw, _ := json.Marshal(map[string]any{"workspace_id": "w", "file_path": file, "table_name": "t"})
		_, err := LoadData(context.Background(), nil, config.Default(), raw)
		return errAnswer(err)
	}
	for _, c := range []struct{ name, arg, leaf, link string }{
		{"in a credential directory", filepath.Join(home, ".aws", "data.csv"), filepath.Join(home, ".aws", "data.csv"), ""},
		{"through a dotfiles-linked ~/.config", filepath.Join(home, ".config", "gcloud", "data.csv"), filepath.Join(dot, "gcloud", "data.csv"), ""},
		{"a credential file", filepath.Join(home, ".docker", "config.json"), filepath.Join(home, ".docker", "config.json"), ""},
		{"a planted link to a credential file", filepath.Join(other, "lnk_file.csv"), filepath.Join(home, ".aws", "planted.csv"), ""},
		{"through a planted link to a credential directory", filepath.Join(other, "lnk_dir", "via.csv"), filepath.Join(home, ".aws", "via.csv"), ""},
		{"where a link in ~/.ssh leads", filepath.Join(sync, "ssh_config"), filepath.Join(sync, "ssh_config"), ""},
		{"a .env file", filepath.Join(other, ".env"), filepath.Join(other, ".env"), ""},
		// The entry named is itself a link, there or not: the refusal must not
		// say where it leads.
		{"a credential entry that is a link", filepath.Join(home, ".ssh", "linked"), filepath.Join(home, ".ssh", "linked"), filepath.Join(sync, "deep", "linked")},
		{"in a credential directory that is a link", filepath.Join(home, ".kube", "config"), filepath.Join(home, ".kube"), filepath.Join(dot, "kube")},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.link != "" {
				symlinkOrSkip(t, c.link, c.leaf)
			} else {
				writeFileAt(t, c.leaf, "a,b\n1,2\n")
			}
			e := answer(c.arg)
			if err := os.Remove(c.leaf); err != nil {
				t.Fatal(err)
			}
			m := answer(c.arg)
			if !strings.HasPrefix(e, toolerr.CodePathNotAllowed+" ") {
				t.Errorf("existing: %s\n  want path_not_allowed", e)
			}
			if e != m {
				t.Errorf("the answer tells them apart\n  existing: %s\n  missing:  %s", e, m)
			}
		})
	}
	// The control: an ordinary missing file is still reported, not refused.
	if a := answer(filepath.Join(other, "typo.csv")); strings.HasPrefix(a, toolerr.CodePathNotAllowed+" ") {
		t.Errorf("an ordinary missing file was refused: %s", a)
	}
}

// attach_files reads only inside the workspace's /work, through an os.Root. A
// place the floor refuses can still lie there — a .env, or the file a link in
// ~/.ssh leads to when the workspace is in that sync folder — and a link a
// caller plants there can point out.
func TestExistenceIsNotRevealedByAttachFiles(t *testing.T) {
	base := realDir(t, t.TempDir())
	home := filepath.Join(base, "home")
	t.Setenv("HOME", home)
	work := filepath.Join(base, "sync")
	wsWork := filepath.Join(work, "wsA", "work")
	for _, d := range []string{filepath.Join(home, ".aws"), filepath.Join(home, ".ssh"), filepath.Join(wsWork, "sub")} {
		mkdirAll(t, d)
	}
	// ~/.ssh/config links into the sync folder the work directory is in.
	symlinkOrSkip(t, filepath.Join(wsWork, "ssh_config.txt"), filepath.Join(home, ".ssh", "config"))
	symlinkOrSkip(t, filepath.Join(home, ".aws", "planted.txt"), filepath.Join(wsWork, "lnk_file.txt"))
	symlinkOrSkip(t, filepath.Join(home, ".aws"), filepath.Join(wsWork, "lnk_dir"))

	answer := func(p string) string {
		raw, _ := json.Marshal(map[string]any{"work_dir": work, "workspace_id": "wsA", "paths": []string{p}})
		res, err := AttachFiles(context.Background(), nil, config.Default(), raw)
		if err != nil {
			return errAnswer(err)
		}
		b, _ := json.Marshal(res)
		return string(b)
	}
	for _, c := range []struct{ name, arg, leaf string }{
		{"a .env file", "/work/sub/.env", filepath.Join(wsWork, "sub", ".env")},
		{"where a link in ~/.ssh leads", "/work/ssh_config.txt", filepath.Join(wsWork, "ssh_config.txt")},
		{"a planted link to a credential file", "/work/lnk_file.txt", filepath.Join(home, ".aws", "planted.txt")},
		{"through a planted link to a credential directory", "/work/lnk_dir/via.txt", filepath.Join(home, ".aws", "via.txt")},
	} {
		t.Run(c.name, func(t *testing.T) {
			writeFileAt(t, c.leaf, "SECRET=1\n")
			e := answer(c.arg)
			if err := os.Remove(c.leaf); err != nil {
				t.Fatal(err)
			}
			m := answer(c.arg)
			if !strings.Contains(e, `rejected: `+c.arg) || strings.Contains(e, "SECRET") {
				t.Errorf("existing: %s\n  want it rejected, unread", e)
			}
			if e != m {
				t.Errorf("the answer tells them apart\n  existing: %s\n  missing:  %s", e, m)
			}
		})
	}
}

func errAnswer(err error) string {
	if err == nil {
		return "accepted"
	}
	var te *toolerr.Error
	if !errors.As(err, &te) {
		return "untyped: " + err.Error()
	}
	d, _ := json.Marshal(te.Details)
	return fmt.Sprintf("%s | %s | %s", te.Code, te.Message, d)
}

func realDir(t *testing.T, d string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(d)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func mkdirAll(t *testing.T, d string) {
	t.Helper()
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFileAt(t *testing.T, p, body string) {
	t.Helper()
	mkdirAll(t, filepath.Dir(p))
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A planted link whose target climbs with .. past a component that is a
// directory, a file or missing gets one answer in all three cases: existence
// is asked at the place, not re-walked from the spelling.
func TestPlacementCornersOfLoadData(t *testing.T) {
	base := realDir(t, t.TempDir())
	home := filepath.Join(base, "home")
	t.Setenv("HOME", home)
	other := filepath.Join(base, "other")
	plant := filepath.Join(base, "plant")
	for _, d := range []string{filepath.Join(home, ".aws"), filepath.Join(other, "probe_d"), plant} {
		mkdirAll(t, d)
	}
	writeFileAt(t, filepath.Join(other, "probe_f"), "x")
	answer := func(file string) string {
		raw, _ := json.Marshal(map[string]any{"workspace_id": "w", "file_path": file, "table_name": "t"})
		_, err := LoadData(context.Background(), nil, config.Default(), raw)
		return errAnswer(err)
	}
	for _, target := range []string{filepath.Join(home, ".aws", "c.csv"), filepath.Join(plant, "ordinary.csv")} {
		writeFileAt(t, target, "a\n1\n")
		rel, err := filepath.Rel(other, target)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, k := range []string{"d", "f", "m"} {
			// Written as a string: filepath.Join would clean the ".." away.
			link := filepath.Join(other, "probe_"+k) + string(filepath.Separator) + ".." + string(filepath.Separator) + rel
			at := filepath.Join(plant, "L_"+k+"_"+filepath.Base(target))
			symlinkOrSkip(t, link, at)
			got[k] = strings.ReplaceAll(answer(at), "L_"+k+"_", "L_?_")
		}
		if got["d"] != got["f"] || got["d"] != got["m"] {
			t.Errorf("a link climbing past a directory / a file / nothing to %s:\n  %s\n  %s\n  %s", target, got["d"], got["f"], got["m"])
		}
	}
}
