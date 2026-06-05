package tools

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nlink-jp/data-toolbox-mcp/internal/config"
	"github.com/nlink-jp/data-toolbox-mcp/internal/mcpserver"
	"github.com/nlink-jp/data-toolbox-mcp/internal/toolerr"
	"github.com/nlink-jp/data-toolbox-mcp/internal/workspace"
)

type attachFilesArgs struct {
	WorkspaceID string   `json:"workspace_id"`
	Paths       []string `json:"paths"`
}

const (
	attachMaxPaths        = 16
	attachSha256SizeLimit = 100 * 1024 * 1024 // skip sha256 above this size
)

// imageExts maps a lowercase extension to its MIME type. Files matching one
// of these are returned as inline MCP image content blocks (base64).
var imageExts = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".bmp":  "image/bmp",
	".svg":  "image/svg+xml",
}

// textExts is the set of extensions returned as inline text content blocks.
var textExts = map[string]bool{
	".csv":    true,
	".tsv":    true,
	".json":   true,
	".jsonl":  true,
	".ndjson": true,
	".txt":    true,
	".md":     true,
	".log":    true,
	".yaml":   true,
	".yml":    true,
	".toml":   true,
}

// AttachFiles implements the attach_files MCP tool (ADR-0008).
//
// Resolves each path under the workspace's <host_work_dir>, dispatches by
// file extension into image / text / metadata-only, enforces per-file and
// cumulative byte caps, and returns a mcpserver.RawResult with a summary
// text block followed by one content block per file. MCP clients (Claude
// Desktop) render image blocks inline.
//
// The workspace is NOT Ensure'd (no Podman call). attach_files is pure host
// filesystem inspection.
func AttachFiles(_ context.Context, _ *workspace.Manager, cfg *config.Config, rawArgs json.RawMessage) (any, error) {
	var args attachFilesArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, toolerr.Newf(toolerr.CodeInvalidArguments, "invalid arguments: %v", err)
	}
	if args.WorkspaceID == "" {
		return nil, toolerr.New(toolerr.CodeMissingArgument, "workspace_id is required")
	}
	if err := workspace.ValidateID(args.WorkspaceID); err != nil {
		return nil, err
	}
	if len(args.Paths) == 0 {
		return nil, toolerr.New(toolerr.CodeMissingArgument, "paths must contain at least 1 entry")
	}
	if len(args.Paths) > attachMaxPaths {
		return nil, toolerr.Newf(toolerr.CodeInvalidArguments,
			"too many paths: got %d, max %d", len(args.Paths), attachMaxPaths)
	}

	hostWorkDir := filepath.Join(cfg.Workspace.Dir, args.WorkspaceID, "work")

	maxSingle := cfg.Attach.MaxSingleSizeBytes
	if maxSingle <= 0 {
		maxSingle = 10 * 1024 * 1024
	}
	maxTotal := cfg.Attach.MaxTotalSizeBytes
	if maxTotal <= 0 {
		maxTotal = 20 * 1024 * 1024
	}

	var blocks []mcpserver.ContentBlock
	var totalBytes int64
	reports := make([]attachReport, 0, len(args.Paths))

	for _, p := range args.Paths {
		rep := attachReport{Input: p}

		absPath, rerr := resolveWorkPath(hostWorkDir, p)
		if rerr != nil {
			rep.Status = "rejected"
			rep.Reason = rerr.Error()
			blocks = append(blocks, mcpserver.ContentBlock{
				Type: "text",
				Text: fmt.Sprintf("rejected: %s — %s\n", p, rerr.Error()),
			})
			reports = append(reports, rep)
			continue
		}
		rep.HostPath = absPath

		fi, err := os.Stat(absPath)
		if err != nil {
			rep.Status = "missing"
			rep.Reason = err.Error()
			blocks = append(blocks, mcpserver.ContentBlock{
				Type: "text",
				Text: fmt.Sprintf("missing: %s\n", absPath),
			})
			reports = append(reports, rep)
			continue
		}
		if fi.IsDir() {
			rep.Status = "rejected"
			rep.Reason = "is a directory"
			blocks = append(blocks, mcpserver.ContentBlock{
				Type: "text",
				Text: fmt.Sprintf("rejected: %s is a directory\n", absPath),
			})
			reports = append(reports, rep)
			continue
		}

		rep.Size = fi.Size()
		ext := strings.ToLower(filepath.Ext(absPath))
		rep.Extension = ext

		switch {
		case imageExts[ext] != "":
			rep.Kind = "image"
		case textExts[ext]:
			rep.Kind = "text"
		default:
			rep.Kind = "metadata"
		}

		// Per-file cap: downgrade to metadata-only.
		if rep.Size > maxSingle && rep.Kind != "metadata" {
			rep.Kind = "metadata"
			rep.Reason = fmt.Sprintf("size %d > max_single_size_bytes %d", rep.Size, maxSingle)
		}
		// Cumulative cap: downgrade once budget would be exceeded.
		if totalBytes+rep.Size > maxTotal && rep.Kind != "metadata" {
			rep.Kind = "metadata"
			rep.Reason = fmt.Sprintf("cumulative budget exhausted (used %d, this %d, max_total %d)",
				totalBytes, rep.Size, maxTotal)
		}

		switch rep.Kind {
		case "image":
			data, err := os.ReadFile(absPath)
			if err != nil {
				rep.Status = "read_error"
				rep.Reason = err.Error()
				blocks = append(blocks, mcpserver.ContentBlock{
					Type: "text",
					Text: fmt.Sprintf("read_error: %s — %s\n", absPath, err.Error()),
				})
				break
			}
			blocks = append(blocks, mcpserver.ContentBlock{
				Type:     "image",
				Data:     base64.StdEncoding.EncodeToString(data),
				MimeType: imageExts[ext],
			})
			totalBytes += rep.Size
			rep.Status = "attached"
		case "text":
			data, err := os.ReadFile(absPath)
			if err != nil {
				rep.Status = "read_error"
				rep.Reason = err.Error()
				blocks = append(blocks, mcpserver.ContentBlock{
					Type: "text",
					Text: fmt.Sprintf("read_error: %s — %s\n", absPath, err.Error()),
				})
				break
			}
			blocks = append(blocks, mcpserver.ContentBlock{
				Type: "text",
				Text: string(data),
			})
			totalBytes += rep.Size
			rep.Status = "attached"
		case "metadata":
			blocks = append(blocks, mcpserver.ContentBlock{
				Type: "text",
				Text: metadataText(absPath, fi, rep.Reason),
			})
			rep.Status = "metadata"
		}

		reports = append(reports, rep)
	}

	out := make([]mcpserver.ContentBlock, 0, 1+len(blocks))
	out = append(out, mcpserver.ContentBlock{
		Type: "text",
		Text: buildSummary(args.WorkspaceID, hostWorkDir, reports),
	})
	out = append(out, blocks...)

	return mcpserver.RawResult{Content: out}, nil
}

// attachReport is internal bookkeeping for the per-call summary text.
type attachReport struct {
	Input     string
	HostPath  string
	Size      int64
	Extension string
	Kind      string // image / text / metadata
	Status    string // attached / metadata / rejected / missing / read_error
	Reason    string
}

// resolveWorkPath maps a user-supplied path to an absolute host path inside
// hostWorkDir, with path-traversal defense in depth.
//
// Accepted forms:
//   - "/work/<sub>"  → <hostWorkDir>/<sub>
//   - "<sub>" (no leading "/") → <hostWorkDir>/<sub>
//
// Any other absolute path is rejected.
func resolveWorkPath(hostWorkDir, p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	var sub string
	switch {
	case strings.HasPrefix(p, "/work/"):
		sub = strings.TrimPrefix(p, "/work/")
	case p == "/work":
		sub = ""
	case strings.HasPrefix(p, "/"):
		return "", fmt.Errorf("absolute path outside /work is not allowed: %q", p)
	default:
		sub = p
	}
	cleanedHWD := filepath.Clean(hostWorkDir)
	full := filepath.Clean(filepath.Join(cleanedHWD, sub))
	// Defense in depth: re-verify full is within cleanedHWD via filepath.Rel.
	rel, err := filepath.Rel(cleanedHWD, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes the workspace /work: %q", p)
	}
	return full, nil
}

func metadataText(absPath string, fi os.FileInfo, reason string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "file at %s\n", absPath)
	fmt.Fprintf(&sb, "size: %d bytes\n", fi.Size())
	fmt.Fprintf(&sb, "modified: %s\n", fi.ModTime().UTC().Format("2006-01-02T15:04:05Z"))
	if fi.Size() <= attachSha256SizeLimit {
		if sum, err := fileSha256(absPath); err == nil {
			fmt.Fprintf(&sb, "sha256: %s\n", sum)
		}
	} else {
		sb.WriteString("sha256: [omitted: file too large]\n")
	}
	if reason != "" {
		fmt.Fprintf(&sb, "reason: %s\n", reason)
	}
	return sb.String()
}

func fileSha256(absPath string) (string, error) {
	f, err := os.Open(absPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func buildSummary(workspaceID, hostWorkDir string, reps []attachReport) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "attach_files: workspace=%s host_work_dir=%s\n",
		workspaceID, hostWorkDir)
	fmt.Fprintf(&sb, "%d file(s) processed:\n", len(reps))
	for _, r := range reps {
		fmt.Fprintf(&sb, "- %s [%s, %d bytes, %s]",
			r.Input, statusLabel(r), r.Size, r.Kind)
		if r.Reason != "" {
			fmt.Fprintf(&sb, " — %s", r.Reason)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func statusLabel(r attachReport) string {
	if r.Status == "" {
		return "?"
	}
	return r.Status
}
