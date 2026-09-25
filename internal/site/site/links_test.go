package site_test

import (
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"go-via.dev/site/shell"
	"go-via.dev/site/site"
)

var (
	hrefAttr = regexp.MustCompile(`\shref="([^"]*)"`)
	idAttr   = regexp.MustCompile(`\sid="([^"]*)"`)
)

func TestSite_everyInternalLinkResolvesToAPageAndAnchor(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	paths := []string{"/_ui"}
	for _, p := range shell.Pages() {
		paths = append(paths, p.Path)
	}
	bodies := map[string]string{}
	ids := map[string]map[string]bool{}
	for _, p := range paths {
		_, body := get(t, srv, p, nil)
		bodies[p] = body
		ids[p] = map[string]bool{}
		for _, m := range idAttr.FindAllStringSubmatch(body, -1) {
			ids[p][html.UnescapeString(m[1])] = true
		}
	}

	static := map[string]int{}
	for _, from := range paths {
		base := &url.URL{Path: from}
		for _, m := range hrefAttr.FindAllStringSubmatch(bodies[from], -1) {
			raw := html.UnescapeString(m[1])
			u, err := url.Parse(raw)
			if !assert.NoError(t, err, "%s: href %q", from, raw) {
				continue
			}
			if u.Scheme != "" || u.Host != "" {
				continue
			}
			to := base.ResolveReference(u)
			if strings.HasPrefix(to.Path, "/static/") {
				if _, seen := static[to.Path]; !seen {
					resp, _ := get(t, srv, to.Path, nil)
					static[to.Path] = resp.StatusCode
				}
				assert.Equal(t, http.StatusOK, static[to.Path], "%s: href %q", from, raw)
				continue
			}
			target, ok := ids[to.Path]
			if !assert.True(t, ok, "%s: href %q is not a page", from, raw) {
				continue
			}
			// "#top" scrolls to the top of any document (HTML spec), no id needed.
			if to.Fragment != "" && to.Fragment != "top" {
				assert.True(t, target[to.Fragment], "%s: href %q has no #%s on %s", from, raw, to.Fragment, to.Path)
			}
		}
	}
}
