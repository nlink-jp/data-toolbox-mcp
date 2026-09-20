package tools

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// pathBasedFileCalls reports every call in dir's non-test sources to an os
// function that opens, reads, writes or stats a file by path.
func pathBasedFileCalls(dir string) (violations []string, files int, err error) {
	refused := map[string]bool{
		"Open": true, "OpenFile": true, "Create": true, "ReadFile": true, "WriteFile": true,
		"Mkdir": true, "MkdirAll": true, "Stat": true, "Lstat": true, "ReadDir": true,
		"Remove": true, "RemoveAll": true, "Rename": true, "Symlink": true, "Link": true,
	}
	// stageUpload opens the HOST source file, which ResolveInput has already
	// held to the credential blacklist; it is not a workspace path.
	allowed := map[string]string{"stageUpload": "Open"}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			return nil, files, err
		}
		files++
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || pkg.Name != "os" || !refused[sel.Sel.Name] {
					return true
				}
				if allowed[fn.Name.Name] == sel.Sel.Name {
					return true
				}
				violations = append(violations, fmt.Sprintf(
					"%s: %s calls os.%s on a path; reach workspace files through an os.Root (writeInWork, or root.Open/Stat)",
					fset.Position(sel.Pos()), fn.Name.Name, sel.Sel.Name))
				return true
			})
		}
	}
	return violations, files, nil
}
