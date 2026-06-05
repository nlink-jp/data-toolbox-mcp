package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/nlink-jp/data-toolbox-mcp/internal/config"
	"github.com/nlink-jp/data-toolbox-mcp/internal/toolerr"
)

// loadFromWorkInput is a small fixture helper.
type loadFromWorkInput struct {
	wsID  string
	path  string
	table string
}

func (a loadFromWorkInput) rawArgs() json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"workspace_id": a.wsID,
		"file_path":    a.path,
		"table_name":   a.table,
	})
	return b
}

func TestLoadFromWork_RejectsNonWorkPath(t *testing.T) {
	cfg := config.Default()
	cfg.Workspace.Dir = t.TempDir()

	cases := []struct {
		name     string
		filePath string
	}{
		{"absolute host path", "/etc/passwd"},
		{"relative path", "data.csv"},
		{"absolute outside work", "/tmp/foo.csv"},
		{"work without slash", "/work"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := LoadFromWork(context.Background(), nil, cfg, loadFromWorkInput{
				wsID: "wsA", path: c.filePath, table: "t",
			}.rawArgs())
			if err == nil {
				t.Fatalf("expected error for %q", c.filePath)
			}
			var te *toolerr.Error
			if !errors.As(err, &te) {
				t.Fatalf("expected *toolerr.Error, got %T: %v", err, err)
			}
			if te.Code != toolerr.CodeInvalidArguments {
				t.Errorf("got code %q, want %q", te.Code, toolerr.CodeInvalidArguments)
			}
		})
	}
}

// TestLoadFromWork_RejectsBadArgs validates required-argument and
// table-name handling at the same boundary as load_data.
func TestLoadFromWork_RejectsBadArgs(t *testing.T) {
	cfg := config.Default()
	cfg.Workspace.Dir = t.TempDir()

	cases := []struct {
		name  string
		input map[string]any
		code  string
	}{
		{
			name:  "missing workspace_id",
			input: map[string]any{"file_path": "/work/x.csv", "table_name": "t"},
			code:  toolerr.CodeMissingArgument,
		},
		{
			name:  "missing file_path",
			input: map[string]any{"workspace_id": "wsA", "table_name": "t"},
			code:  toolerr.CodeMissingArgument,
		},
		{
			name:  "missing table_name",
			input: map[string]any{"workspace_id": "wsA", "file_path": "/work/x.csv"},
			code:  toolerr.CodeMissingArgument,
		},
		{
			name:  "invalid table_name",
			input: map[string]any{"workspace_id": "wsA", "file_path": "/work/x.csv", "table_name": "1bad"},
			code:  toolerr.CodeInvalidTableName,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body, _ := json.Marshal(c.input)
			_, err := LoadFromWork(context.Background(), nil, cfg, body)
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
