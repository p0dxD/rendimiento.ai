// Command undoc lists exported Go declarations without a doc comment, the
// ones `make docs-codemap` would show with an empty summary.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	n := 0
	_ = filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") || strings.Contains(p, "zz_generated") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, p, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		for _, decl := range f.Decls {
			switch x := decl.(type) {
			case *ast.FuncDecl:
				if x.Name.IsExported() && x.Doc == nil {
					fmt.Printf("%s:%d func %s\n", p, fset.Position(x.Pos()).Line, x.Name.Name)
					n++
				}
			case *ast.GenDecl:
				if x.Tok != token.TYPE {
					continue
				}
				for _, s := range x.Specs {
					ts := s.(*ast.TypeSpec)
					if ts.Name.IsExported() && ts.Doc == nil && x.Doc == nil {
						fmt.Printf("%s:%d type %s\n", p, fset.Position(ts.Pos()).Line, ts.Name.Name)
						n++
					}
				}
			}
		}
		return nil
	})
	fmt.Fprintf(os.Stderr, "%d exported declarations without a doc comment\n", n)
}
