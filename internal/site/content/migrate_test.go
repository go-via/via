package content_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// changes is unexported, so its rows are read from source.
func TestChanges_matchMigrationMapping(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "migrate.go", nil, 0)
	require.NoError(t, err)
	consts := map[string]string{}
	var rows []string
	str := func(e ast.Expr) string {
		switch v := e.(type) {
		case *ast.BasicLit:
			s, err := strconv.Unquote(v.Value)
			require.NoError(t, err)
			return s
		case *ast.Ident:
			s, ok := consts[v.Name]
			require.True(t, ok, "%s: %s is not a string constant", fset.Position(v.Pos()), v.Name)
			return s
		}
		require.Failf(t, "unexpected expression", "%s", fset.Position(e.Pos()))
		return ""
	}
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
			return true
		}
		if lit, ok := vs.Values[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
			consts[vs.Names[0].Name] = str(lit)
			return false
		}
		if vs.Names[0].Name != "changes" {
			return true
		}
		lit, ok := vs.Values[0].(*ast.CompositeLit)
		require.True(t, ok, "changes is not a slice literal")
		for _, e := range lit.Elts {
			c := e.(*ast.CompositeLit)
			require.Len(t, c.Elts, 3, "%s: change is not {v07, v08, caught}", fset.Position(c.Pos()))
			rows = append(rows, str(c.Elts[0])+" → "+str(c.Elts[1])+". Caught by: "+str(c.Elts[2])+".")
		}
		return false
	})
	require.NotEmpty(t, rows, "no changes slice literal in migrate.go")

	md, err := os.ReadFile("../../../MIGRATION.md")
	require.NoError(t, err)
	_, section, ok := strings.Cut(string(md), "\n## Mapping\n")
	require.True(t, ok, "MIGRATION.md has no Mapping section")
	section, _, _ = strings.Cut(section, "\n## ")
	var items []string
	for line := range strings.SplitSeq(section, "\n") {
		switch {
		case strings.HasPrefix(line, "- "):
			items = append(items, line[2:])
		case strings.HasPrefix(line, "  ") && len(items) > 0:
			items[len(items)-1] += " " + strings.TrimSpace(line)
		}
	}
	missing := func(want, have []string) (out []string) {
		for _, w := range want {
			if !slices.Contains(have, w) {
				out = append(out, w)
			}
		}
		return out
	}
	require.Empty(t, missing(rows, items), "rows missing from MIGRATION.md's Mapping")
	require.Empty(t, missing(items, rows), "Mapping items missing from migrate.go")
	require.Equal(t, rows, items, "same rows, different order")
}
