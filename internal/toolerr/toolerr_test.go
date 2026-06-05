package toolerr_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/nlink-jp/data-toolbox-mcp/internal/toolerr"
)

func TestErrorString(t *testing.T) {
	e := toolerr.New(toolerr.CodePathNotAllowed, "outside allowed_paths")
	if got := e.Error(); got != "path_not_allowed: outside allowed_paths" {
		t.Errorf("got %q", got)
	}
}

func TestErrorIsByCode(t *testing.T) {
	sentinel := toolerr.New(toolerr.CodePathNotAllowed, "")
	actual := toolerr.Newf(toolerr.CodePathNotAllowed, "blocked: %s", "/etc/passwd")
	if !errors.Is(actual, sentinel) {
		t.Errorf("errors.Is should match by Code")
	}
	other := toolerr.New(toolerr.CodeInvalidTableName, "")
	if errors.Is(actual, other) {
		t.Errorf("errors.Is should not match a different Code")
	}
}

func TestErrorWrappedIs(t *testing.T) {
	inner := toolerr.New(toolerr.CodeScriptFailed, "exit 1")
	wrapped := fmt.Errorf("podman exec: %w", inner)
	if !errors.Is(wrapped, toolerr.New(toolerr.CodeScriptFailed, "")) {
		t.Errorf("errors.Is should walk wrapper chain")
	}
}

func TestErrorJSONMarshal(t *testing.T) {
	e := toolerr.New(toolerr.CodeScriptFailed, "boom").WithDetails(map[string]any{
		"exit_code": 1,
		"stderr":    "Traceback...",
	})
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"code":"script_failed"`, `"message":"boom"`, `"exit_code":1`} {
		if !strings.Contains(s, want) {
			t.Errorf("marshaled error missing %q: %s", want, s)
		}
	}
}

func TestWithDetailsDoesNotMutate(t *testing.T) {
	e := toolerr.New(toolerr.CodePathNotAllowed, "x")
	_ = e.WithDetails(map[string]any{"k": "v"})
	if e.Details != nil {
		t.Errorf("WithDetails should not mutate receiver")
	}
}
