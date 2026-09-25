package content

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOverrides_nameExistingSymbols(t *testing.T) {
	t.Parallel()
	for id := range overrides {
		_, ok := apiIndex[id]
		assert.True(t, ok, "override for %s, which is not in the API", id)
	}
}

func TestAPI_callsNameExistingSymbols(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isAPICall(call.Fun) || len(call.Args) == 0 {
				return true
			}
			pos := fset.Position(call.Pos()).String()
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !assert.True(t, ok && lit.Kind == token.STRING, "%s: API takes a string literal", pos) {
				return true
			}
			sym, err := strconv.Unquote(lit.Value)
			require.NoError(t, err)
			_, known := apiIndex[sym]
			assert.True(t, known, "%s: API(%q) names no exported symbol", pos, sym)
			return true
		})
		return nil
	})
	require.NoError(t, err)
}

func isAPICall(fun ast.Expr) bool {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name == "API" || f.Name == "APIText"
	case *ast.SelectorExpr:
		x, ok := f.X.(*ast.Ident)
		return ok && x.Name == "content" && (f.Sel.Name == "API" || f.Sel.Name == "APIText")
	}
	return false
}
