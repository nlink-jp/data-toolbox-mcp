// Package tools implements the three MCP tools (load_data, query_data,
// execute_code). All file-system access goes through this package's path
// validation helpers so that allowed_paths is the single source of truth.
package tools

import (
	"path/filepath"
	"strings"

	"github.com/nlink-jp/data-toolbox-mcp/internal/toolerr"
)

// ErrPathNotAllowed is the sentinel for a host path outside allowed_paths.
// errors.Is(err, ErrPathNotAllowed) matches by Code so wrapped variants with
// the requested path baked into the message still satisfy it.
var ErrPathNotAllowed = toolerr.New(toolerr.CodePathNotAllowed, "path_not_allowed")

// ResolveAndCheck returns the symlink-resolved absolute path of filePath if
// (and only if) it is contained within one of allowedPaths. Symlinks inside
// allowedPaths are also resolved before comparison, so symlink jail-breaks
// like ~/data/leak -> /etc/passwd are caught.
//
// Per architecture.md §6.1.
func ResolveAndCheck(filePath string, allowedPaths []string) (string, error) {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		return "", toolerr.Newf(toolerr.CodeInvalidArguments, "filepath.Abs: %v", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", toolerr.Newf(toolerr.CodeInvalidArguments, "filepath.EvalSymlinks: %v", err)
	}
	real = filepath.Clean(real)

	for _, allowed := range allowedPaths {
		ar, err := filepath.EvalSymlinks(allowed)
		if err != nil {
			// allowed_paths entry may not exist on disk yet; fall back to its
			// literal form so it still functions as a prefix guard.
			ar = allowed
		}
		ar = filepath.Clean(ar)
		if real == ar || strings.HasPrefix(real, ar+string(filepath.Separator)) {
			return real, nil
		}
	}
	return "", toolerr.Newf(toolerr.CodePathNotAllowed,
		"path_not_allowed: %s is outside allowed_paths", filePath).WithDetails(map[string]any{
		"file_path":     filePath,
		"allowed_paths": allowedPaths,
	})
}
