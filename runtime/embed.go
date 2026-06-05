// Package runtime exposes the embedded Dockerfile used by
// `data-toolbox-mcp build-runtime` (see ADR-0005).
//
// The Dockerfile is shipped inside the Go binary via go:embed so the user does
// not need to manage a separate file on disk.
package runtime

import "embed"

//go:embed Dockerfile
var FS embed.FS
