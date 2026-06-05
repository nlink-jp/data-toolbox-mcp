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
	cfg.Workspace.Dir = t.TempDir()
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

	m := workspace.NewManager(cfg, pc)
	defer m.Cleanup(context.Background())

	ctx := context.Background()
	w, err := m.Ensure(ctx, "itest")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if w.ContainerID == "" {
		t.Fatalf("empty container ID after Ensure")
	}

	// Idempotency: second Ensure should return the same handle without
	// starting a new container.
	w2, err := m.Ensure(ctx, "itest")
	if err != nil {
		t.Fatalf("Ensure (second call): %v", err)
	}
	if w2.ContainerID != w.ContainerID {
		t.Errorf("Ensure not idempotent: first=%q, second=%q", w.ContainerID, w2.ContainerID)
	}

	if err := m.Release(ctx, "itest"); err != nil {
		t.Errorf("Release: %v", err)
	}
}
