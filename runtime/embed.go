// Package runtime exposes the embedded Dockerfile used by
// `data-toolbox-mcp build-runtime` (see ADR-0005).
//
// The Dockerfile is shipped inside the Go binary via go:embed so the user does
// not need to manage a separate file on disk.
package runtime

import "embed"

// requirements.lock is the only package list the image installs from;
// requirements.in is its input for `make runtime-lock` and is embedded so the
// two travel together (ADR-0007, amended v0.8.4).
//
//go:embed Dockerfile requirements.lock requirements.in
var FS embed.FS
