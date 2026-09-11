package via_test

import (
	"net/http"
	"net/http/httptest"
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

// The whole point of the struct: a declared shell reaches the document, the
// <html lang> lands on the element via itself opens, and Raw is emitted
// verbatim right after via's own <meta charset>.
func TestDocumentHead_rendersTheDeclaredShell(t *testing.T) {
	t.Parallel()
	_, body := headResp(t, via.Head{
		Title: "Forum",
		Lang:  "pt-PT",
		Raw:   `<meta name="viewport" content="width=device-width, initial-scale=1"><link rel="icon" href="/favicon.png">`,
	})
	for _, want := range []string{
		`<html lang="pt-PT">`,
		`<title>Forum</title>`,
		`<meta name="viewport" content="width=device-width, initial-scale=1">`,
		`<link rel="icon" href="/favicon.png">`,
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

// Title is configuration, not markup: a value carrying a quote or a tag must
// escape rather than break out of the element. Raw is NOT escaped — it is the
// app's own markup, emitted verbatim by design.
func TestDocumentHead_escapesTitleButNotRaw(t *testing.T) {
	t.Parallel()
	_, body := headResp(t, via.Head{
		Title: `</title><script>x</script>`,
		Raw:   `<meta property="og:title" content="a raw value">`,
	})
	assert.NotContains(t, body, "<script>x</script>")
	assert.Contains(t, body, `<meta property="og:title" content="a raw value">`)
}

// The head is also the CSP declaration: a stylesheet, script and font origin
// each widen exactly their own directive, and nothing else.
func TestDocumentHead_derivesTheCSPFromDeclaredOrigins(t *testing.T) {
	t.Parallel()
	resp, _ := headResp(t, via.Head{
		StyleOrigins:  []string{"https://fonts.googleapis.com"},
		ScriptOrigins: []string{"https://plausible.io"},
		FontOrigins:   []string{"https://fonts.gstatic.com"},
	})
	csp := resp.Header.Get("Content-Security-Policy")
	assert.Contains(t, csp, "style-src 'self' https://fonts.googleapis.com;")
	assert.Contains(t, csp, "https://plausible.io;")
	assert.Contains(t, csp, "font-src 'self' https://fonts.gstatic.com;")
	// A declared host must not leak into a directive it wasn't declared for.
	assert.NotContains(t, csp, "style-src 'self' https://plausible.io")
}

// An undeclared host stays blocked: only the lists named on Head widen the
// policy, so leaving them empty keeps the strict default.
func TestDocumentHead_noOriginsDoesNotWidenTheCSP(t *testing.T) {
	t.Parallel()
	resp, _ := headResp(t, via.Head{Raw: `<link rel="stylesheet" href="/app.css">`})
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
// otherwise be a blocked request nobody sees until later.
func TestDocumentHead_rejectsMalformedHeadsAtStartup(t *testing.T) {
	t.Parallel()
	for name, head := range map[string]via.Head{
		"bad lang":                {Lang: `en" onload="x`},
		"relative font origin":    {FontOrigins: []string{"/fonts"}},
		"relative script origin":  {ScriptOrigins: []string{"/scripts"}},
		"relative style origin":   {StyleOrigins: []string{"/styles"}},
		"unsafe scheme as origin": {ScriptOrigins: []string{"javascript:alert(1)"}},
		"style breakout":          {InlineStyle: "a{}</style><script>x</script>"},
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
		ScriptOrigins: []string{"https://cdn.example.com"},
		FontOrigins:   []string{"https://fonts.gstatic.com"},
	}
	r1, _ := headResp(t, head)
	r2, _ := headResp(t, head)
	require.NotEmpty(t, r1.Header.Get("Content-Security-Policy"))
	assert.Equal(t, r1.Header.Get("Content-Security-Policy"), r2.Header.Get("Content-Security-Policy"))
}
