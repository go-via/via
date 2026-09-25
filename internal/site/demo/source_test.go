package demo_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/demos"
)

func TestSource_namesOnlyEmbeddedFiles(t *testing.T) {
	t.Parallel()

	// Parsed rather than rendered: Source panics at render, so a typo in a
	// srcName is otherwise found only by opening the page it broke.
	dir := filepath.Join("..", "content")
	files, err := os.ReadDir(dir)
	require.NoError(t, err)

	fset := token.NewFileSet()
	var names []string
	for _, f := range files {
		if f.IsDir() || filepath.Ext(f.Name()) != ".go" {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, f.Name()), nil, 0)
		require.NoError(t, err)
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 4 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Card" {
				return true
			}
			lit, ok := call.Args[3].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			name, err := strconv.Unquote(lit.Value)
			require.NoError(t, err)
			names = append(names, name)
			return true
		})
	}
	require.NotEmpty(t, names, "no demo.Card call sites found")

	for _, name := range names {
		_, err := demos.FS.ReadFile(name)
		assert.NoError(t, err, "demo.Card(…, %q) names no file of the demos package", name)
	}
}

func TestFS_embedsNoTestFile(t *testing.T) {
	t.Parallel()

	entries, err := demos.FS.ReadDir(".")
	require.NoError(t, err)

	for _, e := range entries {
		assert.False(t, strings.HasSuffix(e.Name(), "_test.go"),
			"a _test.go in demos is embedded, highlighted at init and served from the Source tab; put demos tests in another package")
	}
}
