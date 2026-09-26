package content_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"html"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/content"
	"go-via.dev/site/site"
)

func served(t *testing.T, path string) string {
	t.Helper()
	app, mux := site.New(site.Options{})
	t.Cleanup(app.Close)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	require.Equal(t, http.StatusOK, rec.Code, path)
	return rec.Body.String()
}

type linkPage struct{ body []h.H }

func (p *linkPage) View() h.H { return h.Div(p.body...) }

// An unknown name renders without a link. at maps each symbol to where it
// was named.
func requireLinked(t *testing.T, at map[string]string) {
	t.Helper()
	var body []h.H
	for sym := range at {
		body = append(body, content.API(sym))
	}
	srv := httptest.NewServer(via.Handler(linkPage{body: body}))
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	page := string(b)
	for sym, pos := range at {
		assert.Contains(t, page, `href="reference#`+html.EscapeString(sym)+`"`, "%s: %s names no exported symbol", pos, sym)
	}
}

// Test files are skipped: they pass API a variable, and nothing in them is
// shown on the site.
func TestAPI_callsNameExistingSymbols(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	at := map[string]string{}
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
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
			at[sym] = pos
			return true
		})
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, at)
	requireLinked(t, at)
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
