package workspace

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Label is the Podman container label used to tag every container created by
// this server. Orphan detection (architecture §6.4) filters on this label.
const Label = "app=data-toolbox-mcp"

// runner abstracts external command execution so tests can substitute a fake.
// This is NOT a container-engine abstraction (forbidden by ADR-0002); it is
// purely a seam for exec.Command.
type runner interface {
	Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, exitCode int, err error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	return stdout.Bytes(), stderr.Bytes(), exitCode, err
}

// PodmanClient calls the local podman binary via exec. Per ADR-0002, there is
// no engine-abstraction interface (Docker is not supported in Phase 1).
type PodmanClient struct {
	binary string
	runner runner
}

// NewPodmanClient returns a client that invokes the `podman` binary on PATH.
func NewPodmanClient() *PodmanClient {
	return &PodmanClient{binary: "podman", runner: execRunner{}}
}

// Mount is a host->container bind mount.
type Mount struct {
	HostPath, ContainerPath string
	ReadOnly                bool
}

// RunOpts describes parameters for `podman run -d`.
type RunOpts struct {
	Image       string
	Name        string
	WorkspaceID string
	Mounts      []Mount
	CPU         string
	Memory      string
	Network     string
	// Userns maps host UID/GID into the container so bind-mounted host files
	// (owned by the invoking user) stay writable from inside. On macOS/Linux
	// rootless Podman this is typically "keep-id:uid=1000,gid=1000" so the
	// container's uid 1000 (USER directive in the runtime Dockerfile) is the
	// host user. See shell-agent-v2 ADR-0004.
	Userns string
}

// Run starts a detached container and returns its ID.
func (c *PodmanClient) Run(ctx context.Context, opts RunOpts) (string, error) {
	args := []string{"run", "-d",
		"--label", Label,
		"--label", "workspace_id=" + opts.WorkspaceID,
		"--name", opts.Name,
	}
	if opts.CPU != "" {
		args = append(args, "--cpus", opts.CPU)
	}
	if opts.Memory != "" {
		args = append(args, "--memory", opts.Memory)
	}
	if opts.Network != "" {
		args = append(args, "--network", opts.Network)
	}
	if opts.Userns != "" {
		args = append(args, "--userns", opts.Userns)
	}
	for _, m := range opts.Mounts {
		spec := fmt.Sprintf("%s:%s", m.HostPath, m.ContainerPath)
		if m.ReadOnly {
			spec += ":ro"
		}
		args = append(args, "-v", spec)
	}
	args = append(args, opts.Image)

	stdout, stderr, code, err := c.runner.Run(ctx, c.binary, args...)
	if err != nil || code != 0 {
		return "", fmt.Errorf("podman run failed (exit %d): %s: %w", code, string(stderr), err)
	}
	return strings.TrimSpace(string(stdout)), nil
}

// ExecOpts describes parameters for `podman exec`.
type ExecOpts struct {
	ContainerID string
	Cmd         []string
}

// ExecResult bundles the result of one exec call.
type ExecResult struct {
	Stdout, Stderr []byte
	ExitCode       int
}

// Exec runs a command inside the container.
// A non-zero ExitCode is NOT returned as an error; the caller inspects ExitCode
// (typical for Python script runs where exit != 0 is just a script failure).
// A returned error indicates podman itself failed (binary missing, container
// gone, etc.).
func (c *PodmanClient) Exec(ctx context.Context, opts ExecOpts) (*ExecResult, error) {
	args := append([]string{"exec", opts.ContainerID}, opts.Cmd...)
	stdout, stderr, code, err := c.runner.Run(ctx, c.binary, args...)
	if err != nil && code == -1 {
		return nil, fmt.Errorf("podman exec failed to start: %w", err)
	}
	return &ExecResult{Stdout: stdout, Stderr: stderr, ExitCode: code}, nil
}

// ContainerState reports whether the container named `name` is running,
// stopped (exited / created / paused), or absent. Used by list_workspaces
// (ADR-0006) to surface workspace state to the LLM without an Ensure.
func (c *PodmanClient) ContainerState(ctx context.Context, name string) (string, error) {
	stdout, stderr, code, err := c.runner.Run(ctx, c.binary,
		"ps", "-a", "--filter", "name="+name, "--format", "{{.State}}")
	if err != nil && code == -1 {
		return "", fmt.Errorf("podman ps: %w", err)
	}
	if code != 0 {
		return "", fmt.Errorf("podman ps (exit %d): %s", code, string(stderr))
	}
	state := strings.TrimSpace(string(stdout))
	switch state {
	case "":
		return "absent", nil
	case "running":
		return "running", nil
	default:
		// exited, created, paused, configured — all "not running".
		return "stopped", nil
	}
}

// FindByName returns the container ID for the given name, or "" if no
// container with that name exists.
func (c *PodmanClient) FindByName(ctx context.Context, name string) (string, error) {
	stdout, stderr, code, err := c.runner.Run(ctx, c.binary,
		"ps", "-a", "--filter", "name="+name, "--format", "{{.ID}}")
	if err != nil && code == -1 {
		return "", fmt.Errorf("podman ps: %w", err)
	}
	if code != 0 {
		return "", fmt.Errorf("podman ps (exit %d): %s", code, string(stderr))
	}
	return strings.TrimSpace(string(stdout)), nil
}

// Stop stops the container.
func (c *PodmanClient) Stop(ctx context.Context, containerID string) error {
	_, stderr, code, err := c.runner.Run(ctx, c.binary, "stop", containerID)
	if err != nil && code == -1 {
		return fmt.Errorf("podman stop: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("podman stop (exit %d): %s", code, string(stderr))
	}
	return nil
}

// Remove forcibly removes the container.
func (c *PodmanClient) Remove(ctx context.Context, containerID string) error {
	_, stderr, code, err := c.runner.Run(ctx, c.binary, "rm", "-f", containerID)
	if err != nil && code == -1 {
		return fmt.Errorf("podman rm: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("podman rm (exit %d): %s", code, string(stderr))
	}
	return nil
}

// ListByLabel returns container IDs matching the given label
// (e.g. "app=data-toolbox-mcp").
func (c *PodmanClient) ListByLabel(ctx context.Context, label string) ([]string, error) {
	stdout, stderr, code, err := c.runner.Run(ctx, c.binary,
		"ps", "-a", "--filter", "label="+label, "--format", "{{.ID}}")
	if err != nil && code == -1 {
		return nil, fmt.Errorf("podman ps: %w", err)
	}
	if code != 0 {
		return nil, fmt.Errorf("podman ps (exit %d): %s", code, string(stderr))
	}
	trimmed := strings.TrimSpace(string(stdout))
	if trimmed == "" {
		return nil, nil
	}
	return strings.Split(trimmed, "\n"), nil
}

// ImageExists reports whether the image is present locally.
func (c *PodmanClient) ImageExists(ctx context.Context, image string) (bool, error) {
	_, _, code, err := c.runner.Run(ctx, c.binary, "image", "exists", image)
	if err != nil && code == -1 {
		return false, fmt.Errorf("podman image exists: %w", err)
	}
	switch code {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, fmt.Errorf("podman image exists: unexpected exit %d", code)
	}
}
