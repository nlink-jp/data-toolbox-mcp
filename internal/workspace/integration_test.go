package workspace_test

import (
	"context"
	"os"
	"testing"

	"github.com/nlink-jp/data-toolbox-mcp/internal/config"
	"github.com/nlink-jp/data-toolbox-mcp/internal/workspace"
)

// TestIntegrationEnsureRelease drives Ensure/Release against the actual podman
// binary. Skipped unless DATA_TOOLBOX_TEST_PODMAN=1 is set.
//
// Prerequisites:
//   - podman is on PATH and (on macOS) `podman machine` is running
//   - the runtime image is pulled locally (set DATA_TOOLBOX_TEST_IMAGE to
//     override; defaults to docker.io/library/alpine:latest as a small,
//     widely-available test image — we are only testing lifecycle, not the
//     runtime contents).
func TestIntegrationEnsureRelease(t *testing.T) {
	if os.Getenv("DATA_TOOLBOX_TEST_PODMAN") != "1" {
		t.Skip("set DATA_TOOLBOX_TEST_PODMAN=1 to run podman integration tests")
	}

	image := os.Getenv("DATA_TOOLBOX_TEST_IMAGE")
	if image == "" {
		image = "docker.io/library/alpine:latest"
	}

	cfg := config.Default()
	work := t.TempDir()
	cfg.Container.Image = image
	// alpine doesn't accept --memory without cgroup setup on some hosts; keep limits empty here.
	cfg.Container.Limits.CPU = ""
	cfg.Container.Limits.Memory = ""

	pc := workspace.NewPodmanClient()

	// Skip cleanly if the image isn't present locally.
	ok, err := pc.ImageExists(context.Background(), image)
	if err != nil {
		t.Skipf("podman image exists failed (is podman running?): %v", err)
	}
	if !ok {
		t.Skipf("test image %q not present locally; run `podman pull %s` first", image, image)
	}

	m := workspace.NewManager(cfg, pc, func(string) error { return nil })
	defer m.Cleanup(context.Background())

	ctx := context.Background()
	w, err := m.Ensure(ctx, work, "itest")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if w.ContainerID == "" {
		t.Fatalf("empty container ID after Ensure")
	}

	// Idempotency: second Ensure should return the same handle without
	// starting a new container.
	w2, err := m.Ensure(ctx, work, "itest")
	if err != nil {
		t.Fatalf("Ensure (second call): %v", err)
	}
	if w2.ContainerID != w.ContainerID {
		t.Errorf("Ensure not idempotent: first=%q, second=%q", w.ContainerID, w2.ContainerID)
	}

	if err := m.Release(ctx, work, "itest"); err != nil {
		t.Errorf("Release: %v", err)
	}
}

// TestIntegrationDeleteTouchesOnlyItsOwnWorkspace asks the real podman the
// question the unit tests ask a fake: does Delete find the container Ensure
// made, and only that one? What only podman can answer is whether the
// anchored name filter means what exactName says it means — an unanchored
// `name=` matches a longer id and the same id under another work directory.
func TestIntegrationDeleteTouchesOnlyItsOwnWorkspace(t *testing.T) {
	if os.Getenv("DATA_TOOLBOX_TEST_PODMAN") != "1" {
		t.Skip("set DATA_TOOLBOX_TEST_PODMAN=1 to run podman integration tests")
	}
	image := os.Getenv("DATA_TOOLBOX_TEST_IMAGE")
	if image == "" {
		image = "docker.io/library/alpine:latest"
	}
	cfg := config.Default()
	cfg.Container.Image = image
	cfg.Container.Limits.CPU = ""
	cfg.Container.Limits.Memory = ""

	pc := workspace.NewPodmanClient()
	ctx := context.Background()
	if ok, err := pc.ImageExists(ctx, image); err != nil {
		t.Skipf("podman image exists failed (is podman running?): %v", err)
	} else if !ok {
		t.Skipf("test image %q not present locally; run `podman pull %s` first", image, image)
	}

	m := workspace.NewManager(cfg, pc, func(string) error { return nil })
	workA, workB := t.TempDir(), t.TempDir()
	// Delete is the cleanup too: it removes the container whether or not the
	// manager still has a handle for it.
	t.Cleanup(func() {
		_ = m.Delete(ctx, workA, "itestdel")
		_ = m.Delete(ctx, workB, "itestdel")
		_ = m.Delete(ctx, workA, "itestdel2")
	})

	first, err := m.Ensure(ctx, workA, "itestdel")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if _, err := m.Ensure(ctx, workB, "itestdel"); err != nil {
		t.Fatalf("Ensure (same id, other work_dir): %v", err)
	}
	if _, err := m.Ensure(ctx, workA, "itestdel2"); err != nil {
		t.Fatalf("Ensure (longer id): %v", err)
	}

	preview, err := m.PreviewDelete(ctx, workA, "itestdel")
	if err != nil {
		t.Fatalf("PreviewDelete: %v", err)
	}
	if preview.ContainerID == "" || preview.ContainerState == "absent" {
		t.Errorf("PreviewDelete did not find the workspace's container: id=%q state=%q",
			preview.ContainerID, preview.ContainerState)
	}

	if err := m.Delete(ctx, workA, "itestdel"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	for _, c := range []struct {
		label, workDir, id string
		wantAbsent         bool
	}{
		{"the deleted workspace", workA, "itestdel", true},
		{"same id, other work_dir", workB, "itestdel", false},
		{"longer id", workA, "itestdel2", false},
	} {
		state, err := m.ContainerStateOf(ctx, c.workDir, c.id)
		if err != nil {
			t.Fatalf("%s: state: %v", c.label, err)
		}
		if (state == "absent") != c.wantAbsent {
			t.Errorf("%s: container state %q after Delete", c.label, state)
		}
	}

	again, err := m.Ensure(ctx, workA, "itestdel")
	if err != nil {
		t.Fatalf("Ensure after Delete: %v", err)
	}
	if again.ContainerID == first.ContainerID {
		t.Errorf("Ensure after Delete returned the deleted container's handle %q", again.ContainerID)
	}
}
