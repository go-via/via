package main

import (
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/site"
)

func loadAll(t *testing.T) []symbol {
	t.Helper()
	syms, err := load(viaRoot())
	require.NoError(t, err)
	return syms
}

func TestGenerate_matchesCommittedFile(t *testing.T) {
	t.Parallel()
	src, err := render(loadAll(t))
	require.NoError(t, err)
	committed, err := os.ReadFile(out())
	require.NoError(t, err)
	stale, fresh := lineDiff(string(committed), string(src))
	assert.Empty(t, stale, "api_gen.go lines the source no longer produces: run go generate ./content")
	assert.Empty(t, fresh, "lines the source produces that api_gen.go lacks: run go generate ./content")
}

func lineDiff(a, b string) (onlyA, onlyB []string) {
	inA, inB := map[string]bool{}, map[string]bool{}
	for _, l := range strings.Split(a, "\n") {
		inA[l] = true
	}
	for _, l := range strings.Split(b, "\n") {
		inB[l] = true
		if !inA[l] {
			onlyB = append(onlyB, l)
		}
	}
	for _, l := range strings.Split(a, "\n") {
		if !inB[l] {
			onlyA = append(onlyA, l)
		}
	}
	return onlyA, onlyB
}

func TestLoad_describesEachKind(t *testing.T) {
	t.Parallel()
	byID := map[string]symbol{}
	for _, s := range loadAll(t) {
		byID[s.Pkg+"."+s.Name] = s
	}
	tests := []struct {
		name, id, kind, sig string
		deprecated          bool
	}{
		{"option func", "via.WithSessionTTL", "func", "func WithSessionTTL(d time.Duration) Option", false},
		{"method", "expr.Expr.Eq", "method", "func (e Expr) Eq(v any) Expr", false},
		{"deprecated func", "expr.Lit", "func", "func Lit(v any) Expr", true},
		{"sentinel var", "via.ErrNotFound", "var", "", false},
		{"struct type", "via.PageError", "type", "type PageError struct{ … }", false},
		{"typed const", "via.ReasonNotFound", "const", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, ok := byID[tt.id]
			require.True(t, ok, "no symbol %s", tt.id)
			assert.Equal(t, tt.kind, s.Kind)
			if tt.sig != "" {
				assert.Equal(t, tt.sig, s.Sig)
			}
			assert.NotContains(t, s.Sig, "\n")
			if !tt.deprecated {
				assert.NotEmpty(t, s.Doc)
			}
			assert.Equal(t, tt.deprecated, s.Deprecated != "")
		})
	}
}

func TestLoad_skipsTestsAndUnexported(t *testing.T) {
	t.Parallel()
	for _, s := range loadAll(t) {
		assert.NotContains(t, s.Name, "Test", s.Pkg+"."+s.Name)
		assert.Regexp(t, `^[A-Z]`, s.Name)
	}
}

func TestReference_hasARowForEverySymbol(t *testing.T) {
	t.Parallel()
	app, mux := site.New(site.Options{Version: "test"})
	t.Cleanup(app.Close)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL + "/reference")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	body := string(b)
	var missing []string
	for _, s := range loadAll(t) {
		id := s.Pkg + "." + s.Name
		if !strings.Contains(body, `id="`+html.EscapeString(id)+`"`) {
			missing = append(missing, id)
		}
	}
	assert.Empty(t, missing, "symbols with no /reference row")
}
