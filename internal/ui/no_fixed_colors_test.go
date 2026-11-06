package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoFixedColorsInTheUI: the UI code builds no color value of its own (issue #51). Colors
// come from the tokens of package style (style.Color, style.Bind*, token names handed to
// widgets) or from package icons, both of which have a dark and a light table. A color.NRGBA
// literal or color.White/color.Black in internal/ui or internal/ui/widgets would stay as it is
// when the system switches the theme. color.Transparent has no variant and stays allowed.
func TestNoFixedColorsInTheUI(t *testing.T) {
	fixedLiterals := map[string]bool{"NRGBA": true, "NRGBA64": true, "RGBA": true, "RGBA64": true, "Gray": true, "Gray16": true, "CMYK": true}
	fixedValues := map[string]bool{"White": true, "Black": true, "Opaque": true}
	for _, dir := range []string{".", "widgets"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, file, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				switch v := n.(type) {
				case *ast.CompositeLit:
					if sel, ok := v.Type.(*ast.SelectorExpr); ok && isPkg(sel.X, "color") && fixedLiterals[sel.Sel.Name] {
						t.Errorf("%s: color.%s literal; use a token of package style", fset.Position(v.Pos()), sel.Sel.Name)
					}
				case *ast.SelectorExpr:
					if isPkg(v.X, "color") && fixedValues[v.Sel.Name] {
						t.Errorf("%s: color.%s; use a token of package style", fset.Position(v.Pos()), v.Sel.Name)
					}
				}
				return true
			})
		}
	}
}

func isPkg(x ast.Expr, name string) bool {
	id, ok := x.(*ast.Ident)
	return ok && id.Name == name
}
