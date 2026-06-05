package tools

import (
	"context"
	"encoding/json"

	"github.com/nlink-jp/data-toolbox-mcp/internal/config"
	"github.com/nlink-jp/data-toolbox-mcp/internal/runtime"
	"github.com/nlink-jp/data-toolbox-mcp/internal/workspace"
)

// DescribeRuntimeResult is the structured return of describe_runtime.
//
// It is intentionally a value type (not a pointer to runtime.Manifest) so
// the runtime package's Default constant remains uncorrupted by JSON tag
// reuse and so future fields (e.g. live values) can be layered on without
// changing runtime.Manifest.
type DescribeRuntimeResult struct {
	PythonVersion  string             `json:"python_version"`
	ContainerImage string             `json:"container_image"`
	Packages       []runtime.Package  `json:"packages"`
	Fonts          []string           `json:"fonts"`
	Network        string             `json:"network"`
	MountPoints    map[string]string  `json:"mount_points"`
	Notes          []string           `json:"notes"`
}

// DescribeRuntime implements the describe_runtime MCP tool (ADR-0006).
// Returns the static manifest plus the live network setting from config.
// No workspace is ensured, no podman call is made — pure metadata.
func DescribeRuntime(_ context.Context, _ *workspace.Manager, cfg *config.Config, _ json.RawMessage) (any, error) {
	m := runtime.Default
	// Honor a user-overridden container image from config.
	if cfg.Container.Image != "" {
		m.ContainerImage = cfg.Container.Image
	}
	return DescribeRuntimeResult{
		PythonVersion:  m.PythonVersion,
		ContainerImage: m.ContainerImage,
		Packages:       m.Packages,
		Fonts:          m.Fonts,
		Network:        cfg.Container.Limits.Network,
		MountPoints:    m.MountPoints,
		Notes:          m.Notes,
	}, nil
}
