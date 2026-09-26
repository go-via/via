package content_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOverrides_nameExistingSymbols(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "reference.go", nil, 0)
	require.NoError(t, err)
	at := map[string]string{}
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok || len(vs.Names) != 1 || vs.Names[0].Name != "overrides" || len(vs.Values) != 1 {
			return true
		}
		lit, ok := vs.Values[0].(*ast.CompositeLit)
		require.True(t, ok, "overrides is not a map literal")
		for _, e := range lit.Elts {
			kv := e.(*ast.KeyValueExpr)
			key, ok := kv.Key.(*ast.BasicLit)
			require.True(t, ok && key.Kind == token.STRING, "%s: override key is not a string literal", fset.Position(kv.Pos()))
			sym, err := strconv.Unquote(key.Value)
			require.NoError(t, err)
			at[sym] = fset.Position(kv.Pos()).String()
		}
		return false
	})
	require.NotEmpty(t, at, "no overrides map literal in reference.go")
	requireLinked(t, at)
}
