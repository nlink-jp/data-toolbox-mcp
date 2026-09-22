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
// The path is placed first (workdir.Where: every link followed, a dangling
// one by its target) and judged there, as given and as placed, before
// anything asks whether it exists: a path that exists and one that does not
// get the same answer, message and details included, so no answer tells the
// caller which secrets exist. Existence is then asked of the place, not
// re-walked from the spelling.
func ResolveInput(filePath string) (string, error) {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		return "", toolerr.Newf(toolerr.CodeInvalidArguments, "filepath.Abs: %v", err)
	}
	where := workdir.Where(abs)
	if err := refused(filePath, where); err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(where)
	if err != nil {
		return "", toolerr.Newf(toolerr.CodeInvalidArguments, "filepath.EvalSymlinks: %v", err)
	}
	real = filepath.Clean(real)
	// It resolved somewhere other than it was placed: it changed in between.
	// Judge where it now leads.
	if real != where {
		if err := refused(filePath, real); err != nil {
			return "", err
		}
	}
	return real, nil
}

// refused is the floor on a host path, as given and at its place.
func refused(filePath, where string) error {
	if why := workdir.Sensitive(filePath, where); why != "" {
		return toolerr.Newf(toolerr.CodePathNotAllowed,
			"path_not_allowed: %s is refused: %s", filePath, why).WithDetails(map[string]any{
			"file_path": filePath,
			"resolved":  where,
		})
	}
	return nil
}
