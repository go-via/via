package site_test

import (
	"html"
	"net/http"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/site"
)

var cspCode = regexp.MustCompile(`<code class="(csp-has|csp-absent)">([^<]*)</code>`)

func TestSecurity_printsTheDirectivesItsResponseCarries(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	resp, body := get(t, srv, "/security", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	policy := resp.Header.Get("Content-Security-Policy")
	require.NotEmpty(t, policy)

	matches := cspCode.FindAllStringSubmatch(body, -1)
	require.NotEmpty(t, matches, "the page rendered no tagged directives")

	var present, absent int
	for _, m := range matches {
		directive := html.UnescapeString(m[2])
		if m[1] == "csp-absent" {
			absent++
			assert.NotContains(t, policy, directive, "the page says %q is absent", directive)
			continue
		}
		present++
		assert.Contains(t, policy, directive)
	}
	assert.NotZero(t, present, "the page listed no directive the header carries")
	assert.NotZero(t, absent, "the page listed no directive the header omits")
}
