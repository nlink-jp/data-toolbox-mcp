package runtime

// Package runtime also exposes Manifest, the static record of what the
// runtime container ships. `describe_runtime` (ADR-0006) returns this plus
// the live network setting from config.
//
// The manifest must stay in lock-step with runtime/Dockerfile. When the
// Dockerfile changes, update Default below in the same commit and amend
// ADR-0007 if the change is structural.

// Package is one entry in the runtime's pip install list.
type Package struct {
	Name              string `json:"name"`
	VersionConstraint string `json:"version_constraint"`
}

// Manifest is the static description of the runtime container's capabilities.
// The `Network` field is intentionally not stored here — it is read from
// config.Container.Limits.Network at request time, since it can be flipped
// without rebuilding the image.
type Manifest struct {
	PythonVersion  string            `json:"python_version"`
	ContainerImage string            `json:"container_image"`
	Packages       []Package         `json:"packages"`
	Fonts          []string          `json:"fonts"`
	MountPoints    map[string]string `json:"mount_points"`
	Notes          []string          `json:"notes"`
}

// Default is the baseline manifest matching the embedded Dockerfile.
// CHANGE THIS WHEN runtime/Dockerfile CHANGES. The e2e manifest-drift test
// pins this to the actually-installed packages.
var Default = Manifest{
	PythonVersion:  "3.12",
	ContainerImage: "localhost/data-toolbox-runtime:latest",
	Packages: []Package{
		{Name: "duckdb", VersionConstraint: "~=1.1"},
		{Name: "pandas", VersionConstraint: "~=2.2"},
		{Name: "polars", VersionConstraint: "~=1.8"},
		{Name: "pyarrow", VersionConstraint: "~=18.0"},
		{Name: "matplotlib", VersionConstraint: "~=3.10"},
		{Name: "Pillow", VersionConstraint: "~=11.0"},
	},
	Fonts: []string{
		"Noto Sans CJK JP",
	},
	MountPoints: map[string]string{
		"/work": "host workspace work directory; container can read and write here",
	},
	Notes: []string{
		"matplotlibrc preconfigured with Noto Sans CJK JP as the first font.sans-serif entry; Japanese labels render without extra setup.",
		"DuckDB file lives at /work/analysis.duckdb inside the container.",
		"Container runs as uid 1000 (toolbox); host bind-mounts are mapped via --userns keep-id:uid=1000,gid=1000.",
	},
}
