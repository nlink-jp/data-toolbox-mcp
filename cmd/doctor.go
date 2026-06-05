package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/nlink-jp/data-toolbox-mcp/internal/config"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Diagnose environment (Podman, runtime image, config)",
	Long: `Verify that Podman is installed and reachable, that podman machine is
running (macOS), that the runtime container image is present locally, and that
the config file (if found) parses successfully. Returns a non-zero exit code
if any check fails.`,
	RunE: runDoctor,
}

func runDoctor(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	ok := true

	// --- Podman binary ---
	podmanOK := checkPodman(cmd, out, &ok)

	// --- Podman machine (macOS only) ---
	if podmanOK && runtime.GOOS == "darwin" {
		checkPodmanMachine(cmd, out, &ok)
	}

	// --- Runtime image ---
	if podmanOK {
		checkRuntimeImage(cmd, out, &ok)
	}

	// --- Config ---
	checkConfig(out, &ok)

	if !ok {
		return fmt.Errorf("doctor: one or more checks failed")
	}
	return nil
}

func checkPodman(cmd *cobra.Command, out io.Writer, ok *bool) bool {
	verOut, verErr := exec.CommandContext(cmd.Context(), "podman", "--version").CombinedOutput()
	if verErr != nil {
		fmt.Fprintln(out, "[FAIL] podman: not found on PATH. Install Podman: https://podman.io/docs/installation")
		*ok = false
		return false
	}
	fmt.Fprintln(out, "[OK]   podman:", strings.TrimSpace(string(verOut)))
	return true
}

// checkPodmanMachine parses `podman machine list --format json` and verifies
// that at least one machine is currently running. Only relevant on macOS.
func checkPodmanMachine(cmd *cobra.Command, out io.Writer, ok *bool) {
	raw, err := exec.CommandContext(cmd.Context(), "podman", "machine", "list", "--format", "json").Output()
	if err != nil {
		fmt.Fprintln(out, "[WARN] podman machine: query failed:", err)
		return
	}
	var machines []struct {
		Name    string `json:"Name"`
		Running bool   `json:"Running"`
		Default bool   `json:"Default"`
	}
	if err := json.Unmarshal(raw, &machines); err != nil {
		fmt.Fprintln(out, "[WARN] podman machine: unexpected output format:", err)
		return
	}
	if len(machines) == 0 {
		fmt.Fprintln(out, "[FAIL] podman machine: no machine configured. Run `podman machine init && podman machine start`.")
		*ok = false
		return
	}
	for _, m := range machines {
		if m.Running {
			fmt.Fprintln(out, "[OK]   podman machine:", m.Name, "running")
			return
		}
	}
	fmt.Fprintln(out, "[FAIL] podman machine: no running machine. Run `podman machine start`.")
	*ok = false
}

func checkRuntimeImage(cmd *cobra.Command, out io.Writer, ok *bool) {
	img := runtimeImageName + ":latest"
	c := exec.CommandContext(cmd.Context(), "podman", "image", "exists", img)
	c.Stdout = io.Discard
	c.Stderr = io.Discard
	switch perr := c.Run(); {
	case perr == nil:
		fmt.Fprintln(out, "[OK]   runtime image:", img, "present locally")
	case isExitCode(perr, 1):
		fmt.Fprintln(out, "[FAIL] runtime image:", img, "NOT present. Run `data-toolbox-mcp build-runtime`.")
		*ok = false
	default:
		fmt.Fprintln(out, "[WARN] runtime image: unexpected podman error:", perr)
		*ok = false
	}
}

// checkConfig tries the config file location used by `serve`. If found, it
// parses; absence is fine (defaults will be used). Parse errors fail doctor.
func checkConfig(out io.Writer, ok *bool) {
	path := findConfigPath()
	if path == "" {
		cfg := config.Default()
		fmt.Fprintln(out, "[OK]   config: not found, defaults will be used")
		fmt.Fprintln(out, "       workspace_dir=", cfg.Workspace.Dir, "image=", cfg.Container.Image, "default_row_limit=", cfg.Query.DefaultRowLimit)
		return
	}
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintln(out, "[FAIL] config: parse error at", path, ":", err)
		*ok = false
		return
	}
	fmt.Fprintln(out, "[OK]   config:", path)
	fmt.Fprintln(out, "       workspace_dir=", cfg.Workspace.Dir, "image=", cfg.Container.Image, "allowed_paths=", cfg.Workspace.AllowedPaths)
}

// findConfigPath mirrors resolveConfig's search order from cmd/serve.go.
func findConfigPath() string {
	if configPath != "" {
		return configPath
	}
	home, _ := os.UserHomeDir()
	for _, c := range []string{
		filepath.Join(home, ".config", "data-toolbox-mcp", "config.toml"),
		"config.toml",
	} {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func isExitCode(err error, code int) bool {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode() == code
	}
	return false
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
