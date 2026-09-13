// Package tools implements the three MCP tools (load_data, query_data,
// execute_code). All file-system access goes through this package's path
// validation helpers, so the blacklist floor is applied in exactly one place.
package tools

import (
	"path/filepath"

	"github.com/nlink-jp/data-toolbox-mcp/internal/toolerr"
	"github.com/nlink-jp/data-toolbox-mcp/internal/workdir"
)

// ErrPathNotAllowed is the sentinel for a refused host path.
// errors.Is(err, ErrPathNotAllowed) matches by Code so wrapped variants with
// the requested path baked into the message still satisfy it.
var ErrPathNotAllowed = toolerr.New(toolerr.CodePathNotAllowed, "path_not_allowed")

// ResolveInput returns the symlink-resolved absolute path of filePath, and
// refuses it only if it lands in a blacklisted location.
//
// There is no operator allowlist any more (ADR-0011). The one that existed
// could not express what it was for: prefix matching has no per-repository
// granularity, so covering a work root meant listing the home directory, which
// admits the files the list was there to keep out. What remains is a fixed
// blacklist of credential and agent-control locations, and it is a floor, not
// a boundary — bounding what this process may touch at all is a sandboxing
// proxy's job.
//
// Both spellings are checked, as given and symlink-resolved, against both
// spellings of every entry: a blacklisted directory may itself be a symlink.
func ResolveInput(filePath string) (string, error) {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		return "", toolerr.Newf(toolerr.CodeInvalidArguments, "filepath.Abs: %v", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", toolerr.Newf(toolerr.CodeInvalidArguments, "filepath.EvalSymlinks: %v", err)
	}
	real = filepath.Clean(real)
	if why := workdir.Sensitive(filePath, real); why != "" {
		return "", toolerr.Newf(toolerr.CodePathNotAllowed,
			"path_not_allowed: %s is refused: %s", filePath, why).WithDetails(map[string]any{
			"file_path": filePath,
			"resolved":  real,
		})
	}
	return real, nil
}
