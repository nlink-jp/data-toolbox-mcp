package runtime

import (
	"strings"
	"testing"
)

// describe_runtime hands this manifest to the model unchanged, so a name the
// server no longer accepts must not survive in it. ADR-0011 moved workspaces
// under the caller's work_dir, and the artifact-exchange note still described
// the host path as <workspace_dir>/… — a config key that now refuses to load.
func TestManifestNamesNoRetiredKey(t *testing.T) {
	m := Default
	text := strings.Join(m.Notes, "\n")
	for k, v := range m.MountPoints {
		text += "\n" + k + " " + v
	}
	for _, retired := range []string{"workspace_dir", "allowed_paths", "workspace_root"} {
		if strings.Contains(text, retired) {
			t.Errorf("manifest still names %q: a workspace lives under the "+
				"work_dir the call named (ADR-0011)", retired)
		}
	}
}

// The note is what tells the model how to hand a file back, so it has to name
// the argument the caller actually passes.
func TestArtifactExchangeNoteNamesWorkDir(t *testing.T) {
	m := Default
	for _, n := range m.Notes {
		if strings.Contains(n, "ARTIFACT EXCHANGE") {
			if !strings.Contains(n, "work_dir") || !strings.Contains(n, "host_work_dir") {
				t.Errorf("the artifact-exchange note must name work_dir and host_work_dir: %q", n)
			}
			return
		}
	}
	t.Error("the manifest no longer carries an ARTIFACT EXCHANGE note")
}
