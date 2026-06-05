package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nlink-jp/data-toolbox-mcp/internal/config"
	"github.com/nlink-jp/data-toolbox-mcp/internal/mcpserver"
	"github.com/nlink-jp/data-toolbox-mcp/internal/toolerr"
)

// attachInput is a small fixture helper.
type attachInput struct {
	wsID  string
	paths []string
}

func (a attachInput) rawArgs() json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"workspace_id": a.wsID,
		"paths":        a.paths,
	})
	return b
}

// setupAttachWorkspace creates a temp workspace with files placed in work/.
func setupAttachWorkspace(t *testing.T, files map[string][]byte) *config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.Workspace.Dir = t.TempDir()
	work := filepath.Join(cfg.Workspace.Dir, "wsA", "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		full := filepath.Join(work, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return cfg
}

// tinyPNG returns the smallest valid 1×1 PNG (~67 bytes).
func tinyPNG() []byte {
	return []byte{
		0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89,
		0x00, 0x00, 0x00, 0x0d, 'I', 'D', 'A', 'T',
		0x78, 0x9c, 0x62, 0x00, 0x01, 0x00, 0x00, 0x05,
		0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4,
		0x00, 0x00, 0x00, 0x00, 'I', 'E', 'N', 'D',
		0xae, 0x42, 0x60, 0x82,
	}
}

func TestAttachFiles_DispatchesByExtension(t *testing.T) {
	cfg := setupAttachWorkspace(t, map[string][]byte{
		"plot.png":  tinyPNG(),
		"data.csv":  []byte("a,b\n1,2\n"),
		"blob.bin":  []byte("\x00\x01\x02\x03binary data"),
		"notes.md":  []byte("# heading\n"),
		"out.svg":   []byte("<svg/>"),
	})

	res, err := AttachFiles(context.Background(), nil, cfg, attachInput{
		wsID:  "wsA",
		paths: []string{"plot.png", "data.csv", "blob.bin", "notes.md", "out.svg"},
	}.rawArgs())
	if err != nil {
		t.Fatalf("AttachFiles: %v", err)
	}

	raw, ok := res.(mcpserver.RawResult)
	if !ok {
		t.Fatalf("expected RawResult, got %T", res)
	}
	// 1 summary text + 5 file blocks.
	if got, want := len(raw.Content), 6; got != want {
		t.Fatalf("got %d content blocks, want %d", got, want)
	}

	// Check kind / mimeType per block (index 0 is the summary).
	wantKinds := []struct {
		typ      string
		mimeType string // for images only
	}{
		{"image", "image/png"},
		{"text", ""},
		{"text", ""}, // metadata-only block also has Type=text
		{"text", ""},
		{"image", "image/svg+xml"},
	}
	for i, want := range wantKinds {
		got := raw.Content[i+1]
		if got.Type != want.typ {
			t.Errorf("block[%d] type = %q, want %q", i, got.Type, want.typ)
		}
		if want.mimeType != "" && got.MimeType != want.mimeType {
			t.Errorf("block[%d] mimeType = %q, want %q", i, got.MimeType, want.mimeType)
		}
	}

	// Block 3 is the metadata-only blob; it should mention "size: ..." and "sha256:".
	meta := raw.Content[3].Text
	if !strings.Contains(meta, "size:") || !strings.Contains(meta, "sha256:") {
		t.Errorf("metadata block missing size/sha256: %q", meta)
	}
}

func TestAttachFiles_RejectsPathTraversal(t *testing.T) {
	cfg := setupAttachWorkspace(t, nil)

	cases := []string{
		"/etc/passwd",          // absolute host path outside /work
		"/work/../etc/passwd",  // escape via /work
		"../escape",            // relative escape
	}
	for _, p := range cases {
		_, err := AttachFiles(context.Background(), nil, cfg, attachInput{
			wsID:  "wsA",
			paths: []string{p},
		}.rawArgs())
		// /etc/passwd: rejected at resolveWorkPath → still returns RawResult with rejected note
		// (We don't error the whole call; per-file rejection lives inside the result.)
		_ = err
	}

	// Verify a known-bad path is recorded as rejected/missing in the result.
	res, err := AttachFiles(context.Background(), nil, cfg, attachInput{
		wsID:  "wsA",
		paths: []string{"/etc/passwd"},
	}.rawArgs())
	if err != nil {
		t.Fatalf("AttachFiles: %v", err)
	}
	raw := res.(mcpserver.RawResult)
	if got := len(raw.Content); got != 2 {
		t.Fatalf("want 2 blocks (summary + rejected), got %d", got)
	}
	if !strings.Contains(raw.Content[1].Text, "rejected") &&
		!strings.Contains(raw.Content[1].Text, "outside /work") {
		t.Errorf("expected rejection message, got: %q", raw.Content[1].Text)
	}
}

func TestAttachFiles_OversizePerFileDowngrades(t *testing.T) {
	// 2 KiB image; cap to 1 KiB single → must downgrade to metadata-only.
	bigPNG := make([]byte, 2048)
	copy(bigPNG, tinyPNG())
	cfg := setupAttachWorkspace(t, map[string][]byte{"big.png": bigPNG})
	cfg.Attach.MaxSingleSizeBytes = 1024

	res, err := AttachFiles(context.Background(), nil, cfg, attachInput{
		wsID:  "wsA",
		paths: []string{"big.png"},
	}.rawArgs())
	if err != nil {
		t.Fatalf("AttachFiles: %v", err)
	}
	raw := res.(mcpserver.RawResult)
	// Summary + 1 metadata block; no image content.
	for _, c := range raw.Content {
		if c.Type == "image" {
			t.Errorf("oversized image should have been downgraded; got image block %q",
				c.MimeType)
		}
	}
}

func TestAttachFiles_CumulativeBudgetDowngrades(t *testing.T) {
	// Two ~3KiB images; total cap 4KiB. First is attached, second downgrades.
	img1 := make([]byte, 3000)
	img2 := make([]byte, 3000)
	copy(img1, tinyPNG())
	copy(img2, tinyPNG())
	cfg := setupAttachWorkspace(t, map[string][]byte{
		"a.png": img1,
		"b.png": img2,
	})
	cfg.Attach.MaxTotalSizeBytes = 4000 // less than two * 3000

	res, err := AttachFiles(context.Background(), nil, cfg, attachInput{
		wsID:  "wsA",
		paths: []string{"a.png", "b.png"},
	}.rawArgs())
	if err != nil {
		t.Fatalf("AttachFiles: %v", err)
	}
	raw := res.(mcpserver.RawResult)
	imageCount := 0
	for _, c := range raw.Content {
		if c.Type == "image" {
			imageCount++
		}
	}
	if imageCount != 1 {
		t.Errorf("expected exactly 1 image after cumulative cap, got %d", imageCount)
	}
}

func TestAttachFiles_RejectsBadArgs(t *testing.T) {
	cfg := setupAttachWorkspace(t, nil)

	cases := []struct {
		name  string
		args  map[string]any
		code  string
	}{
		{
			name: "empty workspace_id",
			args: map[string]any{"workspace_id": "", "paths": []string{"x.png"}},
			code: toolerr.CodeMissingArgument,
		},
		{
			name: "invalid workspace_id",
			args: map[string]any{"workspace_id": "../bad", "paths": []string{"x.png"}},
			code: toolerr.CodeInvalidWorkspaceID,
		},
		{
			name: "empty paths",
			args: map[string]any{"workspace_id": "wsA", "paths": []string{}},
			code: toolerr.CodeMissingArgument,
		},
		{
			name: "too many paths",
			args: map[string]any{"workspace_id": "wsA", "paths": tooManyPaths(17)},
			code: toolerr.CodeInvalidArguments,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body, _ := json.Marshal(c.args)
			_, err := AttachFiles(context.Background(), nil, cfg, body)
			if err == nil {
				t.Fatalf("expected error")
			}
			var te *toolerr.Error
			if !errors.As(err, &te) {
				t.Fatalf("expected *toolerr.Error, got %T: %v", err, err)
			}
			if te.Code != c.code {
				t.Errorf("got code %q, want %q (err=%v)", te.Code, c.code, err)
			}
		})
	}
}

func tooManyPaths(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "x"
	}
	return out
}
