package snippet_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/snippet"
)

type page struct{ body h.H }

func (p *page) View() h.H { return p.body }

func render(t *testing.T, body h.H) string {
	t.Helper()
	app := via.NewRouter()
	t.Cleanup(app.Close)
	via.Mount(app, "/", page{body: body})

	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(b)
}

// text is what the reader copies: the block's markup with every tag dropped.
func text(t *testing.T, body string) string {
	t.Helper()
	start := strings.Index(body, "<pre")
	end := strings.Index(body, "</pre>")
	require.True(t, start >= 0 && end > start, "no <pre> in %s", body)
	var b strings.Builder
	in := false
	for _, r := range body[start:end] {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return strings.NewReplacer("&#34;", `"`, "&lt;", "<", "&gt;", ">", "&amp;", "&", "&#39;", "'").Replace(b.String())
}

func TestShow_rendersTheWholeFileWithoutItsMarkers(t *testing.T) {
	t.Parallel()

	body := render(t, snippet.Show("counter/main.go"))
	got := text(t, body)

	assert.True(t, strings.HasPrefix(got, "package main\n"), "got %q", got)
	assert.Contains(t, got, "func main() {")
	assert.NotContains(t, got, "snippet:", "region markers are for the page, not the reader")
	assert.NotContains(t, got, "\n\n\n", "dropping a marker line leaves no double blank line")
}

func TestShow_labelsTheBlockWithTheFileName(t *testing.T) {
	t.Parallel()

	body := render(t, snippet.Show("counter/main.go"))

	assert.Contains(t, body, `<div class="code code-file"><div class="code-name">main.go</div><pre`)
}

func TestTitle_replacesTheLabel(t *testing.T) {
	t.Parallel()

	body := render(t, snippet.Show("counter/main.go", snippet.Title("cmd/app/main.go")))

	assert.Contains(t, body, `<div class="code-name">cmd/app/main.go</div>`)
}

func TestTitle_emptyLeavesTheLabelOut(t *testing.T) {
	t.Parallel()

	body := render(t, snippet.Show("counter/main.go", snippet.Title("")))

	assert.NotContains(t, body, "code-name")
	assert.Contains(t, body, `<div class="code"><pre class="c-chroma">`)
}

func TestRegion_rendersOnlyTheRegion(t *testing.T) {
	t.Parallel()

	got := text(t, render(t, snippet.Region("counter/main.go", "teaser")))

	assert.True(t, strings.HasPrefix(got, "type Counter struct"), "got %q", got)
	assert.True(t, strings.HasSuffix(got, "}\n"), "a region ends on its last code line: %q", got)
	assert.NotContains(t, got, "package main")
	assert.NotContains(t, got, "func main")
	assert.NotContains(t, got, "snippet:", "a nested region's markers are dropped too")
}

func TestRegion_nestsInsideAnother(t *testing.T) {
	t.Parallel()

	got := text(t, render(t, snippet.Region("counter/main.go", "view")))

	assert.True(t, strings.HasPrefix(got, "func (c *Counter) View() h.H {"), "got %q", got)
	assert.NotContains(t, got, "Inc(")
}

func TestRegion_panicsOnAnUnknownName(t *testing.T) {
	t.Parallel()

	assert.PanicsWithValue(t, `snippet: counter/main.go has no region "nope"`, func() {
		snippet.Region("counter/main.go", "nope")
	})
}

func TestShow_panicsOnAnUnknownFile(t *testing.T) {
	t.Parallel()

	assert.PanicsWithValue(t, "snippet: no embedded file named nope.go", func() { snippet.Show("nope.go") })
}

func TestShow_servesTheDemosPackage(t *testing.T) {
	t.Parallel()

	got := text(t, render(t, snippet.Show("demos/counter.go")))

	assert.True(t, strings.HasPrefix(got, "package demos\n"), "got %q", got)
}

func TestMark_highlightsEveryLineContainingIt(t *testing.T) {
	t.Parallel()

	body := render(t, snippet.Region("counter/main.go", "view", snippet.Mark("on.Click")))

	assert.Equal(t, 2, strings.Count(body, `<span class="ln hl">`))
	assert.Contains(t, body, `<pre class="c-chroma code-hl">`)
	assert.Equal(t, "func (c *Counter) View() h.H {", strings.SplitN(text(t, body), "\n", 2)[0],
		"line wrappers change no text the reader copies")
}

func TestMark_panicsWhenNoLineMatches(t *testing.T) {
	t.Parallel()

	assert.PanicsWithValue(t, `snippet: counter/main.go#view: Mark "on.Submit" matches no line`, func() {
		snippet.Region("counter/main.go", "view", snippet.Mark("on.Submit"))
	})
}

func TestSnippet_callSitesNameEmbeddedFilesAndRegions(t *testing.T) {
	t.Parallel()

	// Parsed rather than rendered: a typo panics only when its page renders,
	// and this finds it without building the site.
	var calls int
	for _, dir := range []string{filepath.Join("..", "content"), filepath.Join("..", "demo")} {
		files, err := os.ReadDir(dir)
		require.NoError(t, err)
		fset := token.NewFileSet()
		for _, f := range files {
			if f.IsDir() || filepath.Ext(f.Name()) != ".go" {
				continue
			}
			file, err := parser.ParseFile(fset, filepath.Join(dir, f.Name()), nil, 0)
			require.NoError(t, err)
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "snippet" {
					return true
				}
				args := literals(t, call.Args)
				switch {
				case sel.Sel.Name == "Show" && len(args) >= 1:
					calls++
					assert.NotPanics(t, func() { snippet.Show(args[0]) }, "%s: snippet.Show(%q)", fset.Position(call.Pos()), args[0])
				case sel.Sel.Name == "Region" && len(args) >= 2:
					calls++
					assert.NotPanics(t, func() { snippet.Region(args[0], args[1]) }, "%s: snippet.Region(%q, %q)", fset.Position(call.Pos()), args[0], args[1])
				}
				return true
			})
		}
	}
	require.NotZero(t, calls, "no snippet call sites found")
}

// literals is the leading run of string-literal arguments.
func literals(t *testing.T, args []ast.Expr) []string {
	t.Helper()
	var out []string
	for _, a := range args {
		lit, ok := a.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			break
		}
		s, err := strconv.Unquote(lit.Value)
		require.NoError(t, err)
		out = append(out, s)
	}
	return out
}
