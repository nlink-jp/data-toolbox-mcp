package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/nlink-jp/data-toolbox-mcp/internal/config"
	"github.com/nlink-jp/data-toolbox-mcp/internal/tools"
	"github.com/nlink-jp/data-toolbox-mcp/internal/transport"
	"github.com/nlink-jp/data-toolbox-mcp/internal/workspace"
)

// The instructions only reach a model if the served server carries them:
// a constant that nothing sets is an instructions field that is never sent.
// This drives the server serve builds with an initialize request and reads
// back what a client would receive.
func TestServedServerAnswersInitializeWithTheInstructions(t *testing.T) {
	if tools.Instructions == "" {
		t.Fatal("tools.Instructions is empty; there is nothing to wire")
	}
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n")
	var out bytes.Buffer
	cfg := config.Default()
	srv := newServer(transport.NewStdioTransport(in, &out), nil,
		workspace.NewManager(cfg, workspace.NewPodmanClient()), cfg)
	if len(srv.Tools()) == 0 {
		t.Error("the served server registers no tools")
	}
	if err := srv.Serve(context.Background()); err != nil {
		t.Fatalf("serve: %v", err)
	}
	var resp struct {
		Result struct {
			Instructions string `json:"instructions"`
		} `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("initialize response is not one JSON object: %v\n%s", err, out.String())
	}
	if resp.Result.Instructions != tools.Instructions {
		t.Errorf("the served initialize result carries instructions %q, want tools.Instructions", resp.Result.Instructions)
	}
}

// The test above proves newServer is wired; it proves nothing about serve if
// serve stops calling it. So newServer must be the only place in this
// package that constructs a server, and runServe must call it.
func TestServeBuildsItsServerOnlyThroughNewServer(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) == 0 {
		t.Fatal("parsed no packages; the test is watching nothing")
	}

	constructedIn := map[string]int{} // enclosing function -> mcpserver.New calls
	runServeCallsNewServer := false
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					switch f := call.Fun.(type) {
					case *ast.SelectorExpr:
						if x, ok := f.X.(*ast.Ident); ok && x.Name == "mcpserver" && f.Sel.Name == "New" {
							constructedIn[fn.Name.Name]++
						}
					case *ast.Ident:
						if f.Name == "newServer" && fn.Name.Name == "runServe" {
							runServeCallsNewServer = true
						}
					}
					return true
				})
			}
		}
	}

	if constructedIn["newServer"] == 0 {
		t.Error("newServer does not call mcpserver.New; the construction point moved and this test guards nothing")
	}
	for fn, n := range constructedIn {
		if fn != "newServer" {
			t.Errorf("%s calls mcpserver.New %d time(s); only newServer may, or that server "+
				"is served without the initialize instructions", fn, n)
		}
	}
	if !runServeCallsNewServer {
		t.Error("runServe does not call newServer, so what the served binary answers is not what the test above checked")
	}
}
