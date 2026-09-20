package tools

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// pathBasedFileCalls reports every use, in dir's non-test sources, of a
// file-system entry point that takes a path. It is an allow-list: what this
// package may use from os and path/filepath is written down, and anything
// else is refused — a deny-list would have to know every way to open a file,
// and the next one added to the standard library would walk past it.
func pathBasedFileCalls(dir string) (violations []string, files int, err error) {
	allowed := map[string]map[string]string{ // package path -> name -> the only function that may use it ("" = any)
		"os": {
			"Root": "", "FileInfo": "", "PathSeparator": "", "IsNotExist": "",
			"OpenRoot": "openWorkRoot", // the one root, reached through work_dir
			"Open":     "stageUpload",  // the HOST source file, held to the blacklist by ResolveInput
		},
		"path/filepath": {
			// Pure path arithmetic, and the two resolvers ResolveInput needs.
			"Abs": "", "Base": "", "Clean": "", "Ext": "", "Join": "", "Rel": "", "EvalSymlinks": "",
		},
	}
	refusedImports := map[string]bool{"io/ioutil": true, "io/fs": true, "syscall": true, "golang.org/x/sys/unix": true}

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

		// Local name -> package path, so `import stdos "os"` is still os.
		local := map[string]string{}
		for _, imp := range file.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if refusedImports[path] {
				violations = append(violations, fmt.Sprintf("%s: imports %s, another way to reach the file system by path",
					fset.Position(imp.Pos()), path))
			}
			if _, watched := allowed[path]; !watched {
				continue
			}
			name := path[strings.LastIndex(path, "/")+1:]
			if imp.Name != nil {
				name = imp.Name.Name
			}
			local[name] = path
		}

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			where := "(package level)"
			if ok {
				where = fn.Name.Name
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				path, watched := local[pkg.Name]
				if !watched {
					return true
				}
				owner, known := allowed[path][sel.Sel.Name]
				if known && (owner == "" || owner == where) {
					return true
				}
				violations = append(violations, fmt.Sprintf(
					"%s: %s uses %s.%s; reach workspace files through the os.Root from openWorkRoot (writeInWork, root.Open/Stat/ReadFile)",
					fset.Position(sel.Pos()), where, path, sel.Sel.Name))
				return true
			})
		}
	}
	return violations, files, nil
}
