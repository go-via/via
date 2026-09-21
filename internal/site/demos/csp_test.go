package demos_test

import (
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/demos"
)

type cspPage struct{ Notes demos.CSPNotes }

func (p *cspPage) View() h.H { return via.Child(p.Notes) }

var cspCode = regexp.MustCompile(`<code class="(csp-has|csp-absent)">([^<]*)</code>`)

func cspRender(t *testing.T) (header, body string) {
	t.Helper()
	app := via.NewRouter()
	t.Cleanup(app.Close)
	via.Mount(app, "/", cspPage{})

	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.Header.Get("Content-Security-Policy"), string(b)
}

func TestCSPNotes_printsTheDirectivesTheResponseCarries(t *testing.T) {
	t.Parallel()

	policy, body := cspRender(t)
	require.NotEmpty(t, policy)

	matches := cspCode.FindAllStringSubmatch(body, -1)
	require.NotEmpty(t, matches, "the demo rendered no tagged directives")

	var present int
	for _, m := range matches {
		directive := html.UnescapeString(m[2])
		if m[1] == "csp-absent" {
			assert.NotContains(t, policy, directive, "the demo says %q is absent", directive)
			continue
		}
		present++
		assert.Contains(t, policy, directive)
	}
	assert.NotZero(t, present, "the demo listed no directive the header carries")
}
