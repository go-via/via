package via_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type headPage struct{}

func (headPage) View() h.H { return h.Div(h.Str("hi")) }

func headResp(t *testing.T, head via.Head) (*http.Response, string) {
	t.Helper()
	return do(t, headSrv(t, via.WithDocumentHead(head)), http.MethodGet, "/", "")
}

func headSrv(t *testing.T, opts ...via.Option) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(via.Register(headPage{}, opts...))
	t.Cleanup(srv.Close)
	return srv
}

// The whole point of the struct: a declared shell reaches the document, and the
// <html lang> lands on the element via itself opens.
func TestDocumentHead_rendersTheDeclaredShell(t *testing.T) {
	t.Parallel()
	_, body := headResp(t, via.Head{
		Title: "Forum",
		Lang:  "pt-PT",
		Meta:  []via.HeadMeta{{Name: "viewport", Content: "width=device-width, initial-scale=1"}},
		Links: []via.HeadLink{{Rel: "icon", Href: "/favicon.png", Type: "image/png"}},
	})
	for _, want := range []string{
		`<html lang="pt-PT">`,
		`<title>Forum</title>`,
		`<meta name="viewport" content="width=device-width, initial-scale=1">`,
		`<link rel="icon" href="/favicon.png" type="image/png">`,
	} {
		assert.Contains(t, body, want)
	}
}

// A zero Head must serve exactly what via served before the option existed —
// otherwise every existing app's document silently changes shape.
func TestDocumentHead_zeroValueChangesNothing(t *testing.T) {
	t.Parallel()
	_, with := headResp(t, via.Head{})
	_, without := do(t, headSrv(t), http.MethodGet, "/", "")
	assert.Equal(t, without, with)
	assert.Contains(t, with, "<html><head>")
	assert.NotContains(t, with, "<title>")
}

// A head is configuration, not markup: a value carrying a quote or a tag must
// escape rather than break out of its attribute.
func TestDocumentHead_escapesEveryInterpolatedValue(t *testing.T) {
	t.Parallel()
	_, body := headResp(t, via.Head{
		Title: `</title><script>x</script>`,
		Meta:  []via.HeadMeta{{Property: "og:title", Content: `" onload="x`}},
	})
	assert.NotContains(t, body, "<script>x</script>")
	assert.NotContains(t, body, `onload="x`)
	assert.Contains(t, body, `<meta property="og:title" content="&#34; onload=&#34;x">`)
}

// The head is also the CSP declaration: an off-origin stylesheet, script and
// font origin each widen exactly their own directive, and nothing else.
func TestDocumentHead_derivesTheCSPFromDeclaredOrigins(t *testing.T) {
	t.Parallel()
	resp, _ := headResp(t, via.Head{
		Links:       []via.HeadLink{{Rel: "stylesheet", Href: "https://fonts.googleapis.com/css2?family=X"}},
		Scripts:     []via.HeadScript{{Src: "https://plausible.io/js/script.js", Defer: true}},
		FontOrigins: []string{"https://fonts.gstatic.com"},
	})
	csp := resp.Header.Get("Content-Security-Policy")
	assert.Contains(t, csp, "style-src 'self' https://fonts.googleapis.com;")
	assert.Contains(t, csp, "https://plausible.io;")
	assert.Contains(t, csp, "font-src 'self' https://fonts.gstatic.com;")
	// The path is not part of an origin, and a declared host must not leak
	// into a directive it wasn't declared for.
	assert.NotContains(t, csp, "/js/script.js")
	assert.NotContains(t, csp, "style-src 'self' https://plausible.io")
}

// A same-origin reference must NOT widen the policy: 'self' already covers it,
// and every needless source in a policy is a real loss of protection.
func TestDocumentHead_sameOriginReferencesDoNotWidenTheCSP(t *testing.T) {
	t.Parallel()
	resp, _ := headResp(t, via.Head{
		Links:   []via.HeadLink{{Rel: "stylesheet", Href: "/app.css"}},
		Scripts: []via.HeadScript{{Src: "/app.js"}},
	})
	assert.Contains(t, resp.Header.Get("Content-Security-Policy"), "style-src 'self';")
}

// InlineStyle is the one inline case a strict policy can carry: it ships in the
// document and is admitted by the hash of its exact bytes.
func TestDocumentHead_inlineStyleIsAdmittedByItsHash(t *testing.T) {
	t.Parallel()
	const css = "body{margin:0}"
	resp, body := headResp(t, via.Head{InlineStyle: css})
	assert.Contains(t, body, "<style>"+css+"</style>")
	assert.Contains(t, resp.Header.Get("Content-Security-Policy"), hashSource(css))
}

// Malformed heads fail at startup, not in the browser. Each of these would
// otherwise be a missing element or a blocked request nobody sees until later.
func TestDocumentHead_rejectsMalformedHeadsAtStartup(t *testing.T) {
	t.Parallel()
	for name, head := range map[string]via.Head{
		"link without rel":     {Links: []via.HeadLink{{Href: "/x.css"}}},
		"link without href":    {Links: []via.HeadLink{{Rel: "stylesheet"}}},
		"unsafe link href":     {Links: []via.HeadLink{{Rel: "stylesheet", Href: "javascript:alert(1)"}}},
		"script without src":   {Scripts: []via.HeadScript{{Defer: true}}},
		"unsafe script src":    {Scripts: []via.HeadScript{{Src: "javascript:alert(1)"}}},
		"meta with neither":    {Meta: []via.HeadMeta{{Content: "x"}}},
		"meta with both":       {Meta: []via.HeadMeta{{Name: "a", Property: "b", Content: "x"}}},
		"bad lang":             {Lang: `en" onload="x`},
		"relative font origin": {FontOrigins: []string{"/fonts"}},
		"style breakout":       {InlineStyle: "a{}</style><script>x</script>"},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Panics(t, func() { via.Register(headPage{}, via.WithDocumentHead(head)) })
		})
	}
}

// Two apps configured identically must serve byte-identical policies — the same
// statelessness the hash-based CSP bought, preserved once the head can widen it.
func TestDocumentHead_cspStaysAPureFunctionOfTheConfig(t *testing.T) {
	t.Parallel()
	head := via.Head{
		Scripts:     []via.HeadScript{{Src: "https://cdn.example.com/a.js"}},
		FontOrigins: []string{"https://fonts.gstatic.com"},
	}
	r1, _ := headResp(t, head)
	r2, _ := headResp(t, head)
	require.NotEmpty(t, r1.Header.Get("Content-Security-Policy"))
	assert.Equal(t, r1.Header.Get("Content-Security-Policy"), r2.Header.Get("Content-Security-Policy"))
}

// A duplicate origin must appear once: a repeated source is noise in a header
// that has to stay readable and byte-stable.
func TestDocumentHead_deduplicatesOrigins(t *testing.T) {
	t.Parallel()
	resp, _ := headResp(t, via.Head{Scripts: []via.HeadScript{
		{Src: "https://cdn.example.com/a.js"},
		{Src: "https://cdn.example.com/b.js"},
	}})
	csp := resp.Header.Get("Content-Security-Policy")
	assert.Equal(t, 1, strings.Count(csp, "https://cdn.example.com"))
}
