// Package config loads and validates the TOML configuration file.
//
// Unknown keys are rejected at load time (memory feedback_strict_json_decode)
// so typos surface immediately instead of silently using the default.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config is the parsed contents of config.toml.
type Config struct {
	Server    ServerConfig    `toml:"server"`
	Workspace WorkspaceConfig `toml:"workspace"`
	Container ContainerConfig `toml:"container"`
	Query     QueryConfig     `toml:"query"`
	Attach    AttachConfig    `toml:"attach"`
}

type ServerConfig struct {
	LogLevel string `toml:"log_level"`
	LogFile  string `toml:"log_file"`
}

type WorkspaceConfig struct {
	Dir          string   `toml:"workspace_dir"`
	AllowedPaths []string `toml:"allowed_paths"`
}

type ContainerConfig struct {
	Image      string          `toml:"image"`
	StopOnExit bool            `toml:"stop_on_exit"`
	Limits     ContainerLimits `toml:"limits"`
}

type ContainerLimits struct {
	CPU            string `toml:"cpu"`
	Memory         string `toml:"memory"`
	TimeoutSeconds int    `toml:"timeout_seconds"`
	Network        string `toml:"network"`
}

type QueryConfig struct {
	DefaultRowLimit int `toml:"default_row_limit"`
}

// AttachConfig controls attach_files response size caps (ADR-0008).
type AttachConfig struct {
	// MaxSingleSizeBytes caps the size of a single attached file. Files
	// larger than this are downgraded to a metadata-only text block.
	MaxSingleSizeBytes int64 `toml:"max_single_size_bytes"`
	// MaxTotalSizeBytes caps the cumulative byte budget for one attach_files
	// call. Once cumulative attached bytes reach this threshold, remaining
	// files are downgraded to metadata-only.
	MaxTotalSizeBytes int64 `toml:"max_total_size_bytes"`
}

// Default returns a fully populated Config with the values documented in
// docs/{en,ja}/reference/architecture.md §6.3 and ADR-0005.
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			LogLevel: "info",
		},
		Workspace: WorkspaceConfig{
			Dir: ExpandHome("~/.data-toolbox"),
		},
		Container: ContainerConfig{
			Image:      "localhost/data-toolbox-runtime:latest",
			StopOnExit: true,
			Limits: ContainerLimits{
				CPU:            "1.0",
				Memory:         "2GB",
				TimeoutSeconds: 60,
				Network:        "none",
			},
		},
		Query: QueryConfig{
			DefaultRowLimit: 20000,
		},
		Attach: AttachConfig{
			MaxSingleSizeBytes: 10 * 1024 * 1024, // 10 MiB
			MaxTotalSizeBytes:  20 * 1024 * 1024, // 20 MiB
		},
	}
}

// Load reads and decodes the config file. Unknown keys are rejected.
// Missing fields keep the values from Default().
func Load(path string) (*Config, error) {
	cfg := Default()
	meta, err := toml.DecodeFile(path, cfg)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return nil, fmt.Errorf("unknown config keys: %v", undecoded)
	}
	cfg.Workspace.Dir = ExpandHome(cfg.Workspace.Dir)
	cfg.Server.LogFile = ExpandHome(cfg.Server.LogFile)
	for i, p := range cfg.Workspace.AllowedPaths {
		cfg.Workspace.AllowedPaths[i] = ExpandHome(p)
	}
	return cfg, nil
}

// ExpandHome expands a leading ~ in p using the current user's home directory.
// If $HOME cannot be determined, p is returned unchanged.
func ExpandHome(p string) string {
	if p == "" || p[0] != '~' {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	if len(p) > 1 && p[1] == '/' {
		return filepath.Join(home, p[2:])
	}
	return p
}
