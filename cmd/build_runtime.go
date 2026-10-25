package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	dtmruntime "github.com/nlink-jp/data-toolbox-mcp/runtime"
	"github.com/spf13/cobra"
)

// runtimeImageName is the canonical local image name (ADR-0005).
// It is kept as a constant so cmd/doctor.go and cmd/build_runtime.go agree.
const runtimeImageName = "localhost/data-toolbox-runtime"

var buildRuntimeCmd = &cobra.Command{
	Use:   "build-runtime",
	Short: "Build the runtime container image via Podman",
	Long: `Unpack the embedded Dockerfile to a temp directory and run ` + "`podman build`" + ` to
produce localhost/data-toolbox-runtime:<version> (and the :latest tag).

Every build starts from scratch, without the layer cache, and pulls the base
image when the registry has a newer one, so re-running build-runtime takes
Debian, Python and package security updates.

See ADR-0005 for the rationale (local-build distribution).`,
	RunE: runBuildRuntime,
}

func runBuildRuntime(cmd *cobra.Command, args []string) error {
	tmp, err := os.MkdirTemp("", "data-toolbox-mcp-build-*")
	if err != nil {
		return fmt.Errorf("mkdir tempdir: %w", err)
	}
	defer os.RemoveAll(tmp)

	// Unpack every embedded file to the tempdir, preserving paths.
	walkErr := fs.WalkDir(dtmruntime.FS, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		data, err := dtmruntime.FS.ReadFile(path)
		if err != nil {
			return err
		}
		dst := filepath.Join(tmp, path)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if walkErr != nil {
		return fmt.Errorf("unpack embedded runtime files: %w", walkErr)
	}

	tags := []string{
		runtimeImageName + ":" + Version,
		runtimeImageName + ":latest",
	}
	podmanArgs := buildArgs(tmp, tags)

	fmt.Fprintln(cmd.ErrOrStderr(), "+ podman", podmanArgs)
	pb := exec.CommandContext(cmd.Context(), "podman", podmanArgs...)
	pb.Stdout = cmd.OutOrStdout()
	pb.Stderr = cmd.ErrOrStderr()
	if err := pb.Run(); err != nil {
		return fmt.Errorf("podman build failed: %w", err)
	}

	fmt.Fprintln(cmd.OutOrStdout(), "")
	fmt.Fprintln(cmd.OutOrStdout(), "Built runtime image with tags:")
	for _, t := range tags {
		fmt.Fprintln(cmd.OutOrStdout(), " -", t)
	}
	return nil
}

// buildArgs is the argv after `podman` for building the runtime image.
//
// A rebuild exists to take security updates, and the embedded Dockerfile does
// not change between rebuilds, so the build bypasses the layer cache
// (--no-cache) — with the cache the apt and pip layers are reused and nothing
// is updated. The base is a tag, not a digest, so --pull=newer also refreshes
// a local python:3.12-slim that the registry has since replaced (ADR-0005,
// amended v0.8.3).
func buildArgs(contextDir string, tags []string) []string {
	args := []string{"build", "--no-cache", "--pull=newer"}
	for _, t := range tags {
		args = append(args, "-t", t)
	}
	return append(args, contextDir)
}

func init() {
	rootCmd.AddCommand(buildRuntimeCmd)
}
