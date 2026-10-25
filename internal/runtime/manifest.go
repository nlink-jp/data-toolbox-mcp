package runtime

// Package runtime also exposes Manifest, the static record of what the
// runtime container ships. `describe_runtime` (ADR-0006) returns this plus
// the live network setting from config.
//
// The packages must stay in lock-step with runtime/requirements.lock, which is
// what the image installs. When `make runtime-lock` changes it, update Default
// below in the same commit (TestManifestMatchesLock compares exact versions,
// TestManifestNamesMatchInput the names against requirements.in) and amend
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

// Default is the baseline manifest matching the embedded runtime files.
// CHANGE THIS WHEN runtime/requirements.lock CHANGES; the unit tests in cmd
// fail until it agrees.
var Default = Manifest{
	PythonVersion:  "3.12",
	ContainerImage: "localhost/data-toolbox-runtime:latest",
	Packages: []Package{
		// Exact versions from runtime/requirements.lock, which the image
		// installs with --require-hashes (ADR-0007, amended v0.8.4).
		// TestManifestMatchesLock fails when the two disagree.
		{Name: "duckdb", VersionConstraint: "==1.5.6"},
		{Name: "pandas", VersionConstraint: "==2.3.3"},
		{Name: "polars", VersionConstraint: "==1.44.2"},
		{Name: "pyarrow", VersionConstraint: "==26.0.0"},
		{Name: "matplotlib", VersionConstraint: "==3.11.2"},
		{Name: "Pillow", VersionConstraint: "==13.0.0"},
	},
	Fonts: []string{
		"Noto Sans CJK JP",
	},
	MountPoints: map[string]string{
		"/work": "container directory bind-mounted to the host; files here appear on the host (see notes for the artifact-exchange pattern)",
	},
	Notes: []string{
		"matplotlibrc preconfigured with Noto Sans CJK JP as the first font.sans-serif entry; Japanese labels render without extra setup.",
		"DuckDB file lives at /work/analysis.duckdb inside the container.",
		"Container runs as uid 1000 (toolbox); host bind-mounts are mapped via --userns keep-id:uid=1000,gid=1000.",
		"ARTIFACT EXCHANGE: anything you write to /work/<name> inside the container appears on the host at <work_dir>/<workspace_id>/work/<name>, under the work_dir you passed to the call. The exact host path for the workspace you are using is returned as host_work_dir in the execute_code result and in each list_workspaces item. Use this to hand files back to the user (PNG plots, exported CSV / Parquet, generated reports — anything). Do NOT base64-encode and embed in the response: it wastes the response budget and the user can open the file directly.",
	},
}
