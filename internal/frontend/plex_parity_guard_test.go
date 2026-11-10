package frontend

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Plex is a frontend like every other: the shared layer has nothing named for
// it, it mounts from the generic Host alone, and it reaches optional
// abilities only through generic capability interfaces, exactly as
// mediabrowser does. This guards the end state of the Prism migration.
func TestPlexUsesOnlyTheGenericHost(t *testing.T) {
	fset := token.NewFileSet()
	// 1. No Plex-specific contract in the shared layer.
	shared, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range shared {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil && strings.HasPrefix(d.Name.Name, "Plex") {
					t.Errorf("%s: shared func %s is Plex-specific", path, d.Name.Name)
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					switch s := s.(type) {
					case *ast.TypeSpec:
						if strings.HasPrefix(s.Name.Name, "Plex") {
							t.Errorf("%s: shared type %s is Plex-specific", path, s.Name.Name)
						}
						if s.Name.Name == "Host" {
							if st, ok := s.Type.(*ast.StructType); ok {
								for _, field := range st.Fields.List {
									for _, n := range field.Names {
										if strings.Contains(n.Name, "Plex") {
											t.Errorf("%s: Host.%s is a Plex-only host capability", path, n.Name)
										}
									}
								}
							}
						}
					case *ast.ValueSpec:
						for _, n := range s.Names {
							if strings.HasPrefix(n.Name, "Plex") {
								t.Errorf("%s: shared value %s is Plex-specific", path, n.Name)
							}
						}
					}
				}
			}
		}
	}
	// 2. The Plex frontend has no back doors: no anonymous interface
	// assertions and no exported Use* injection on its server.
	root := filepath.Join("..", "frontends", "plex")
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.TypeAssertExpr:
				if _, anon := n.Type.(*ast.InterfaceType); anon {
					t.Errorf("%s: anonymous interface assertion reaches around the host contract", fset.Position(n.Pos()))
				}
			case *ast.FuncDecl:
				if n.Recv != nil && strings.HasPrefix(n.Name.Name, "Use") && ast.IsExported(n.Name.Name) {
					t.Errorf("%s: exported %s injects a dependency outside Mount", fset.Position(n.Pos()), n.Name.Name)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
