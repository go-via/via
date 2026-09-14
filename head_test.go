package via_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
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
	return do(t, headSrv(t, via.WithHead(head)), http.MethodGet, "/", "")
}

func headSrv(t *testing.T, opts ...via.Option) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(via.Handler(headPage{}, opts...))
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
			assert.Panics(t, func() { via.Handler(headPage{}, via.WithHead(head)) })
		})
	}
}

// Two apps configured identically must serve byte-identical policies — the same
// plainness the hash-based CSP bought, preserved once the head can widen it.
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

// --- Per-page title, declared by the ROOT composition.
//
// Head is router-wide, so every page in a multi-page app would otherwise share
// one <title>. Titler is the per-page override: a method, because a real title
// is data-dependent, read after OnInit and only from the mounted root.

type titledPage struct {
	title string
	Child titledChild
}

var _ via.Titler = (*titledPage)(nil)

func (p *titledPage) Title() string { return p.title }
func (p *titledPage) View() h.H     { return h.Div(h.Str("hi"), via.Embed(p.Child)) }

type titledChild struct{ title string }

func (c *titledChild) Title() string { return c.title }
func (c *titledChild) View() h.H     { return h.Span(h.Str("child")) }

// A title derived from data OnInit loads — the case a struct field could not
// serve, and the reason Title is a method read after the hook has run.
type loadedTitlePage struct {
	store   map[string]string
	subject string
}

var _ via.Initer = (*loadedTitlePage)(nil)
var _ via.Titler = (*loadedTitlePage)(nil)

func (p *loadedTitlePage) OnInit(*via.Ctx) error { p.subject = p.store["7"]; return nil }
func (p *loadedTitlePage) Title() string         { return "Ticket #7 — " + p.subject }
func (p *loadedTitlePage) View() h.H             { return h.Div(h.Str(p.subject)) }

func titledBody[T any, PT interface {
	*T
	View() h.H
}](t *testing.T, p T, opts ...via.Option) (*http.Response, string) {
	t.Helper()
	srv := httptest.NewServer(via.Handler[T, PT](p, opts...))
	t.Cleanup(srv.Close)
	return do(t, srv, http.MethodGet, "/", "")
}

func TestTitle_aRootPageOverridesTheRouterWideTitle(t *testing.T) {
	t.Parallel()
	_, body := titledBody(t, titledPage{title: "Ticket #7"}, via.WithHead(via.Head{Title: "Helpdesk"}))

	assert.Contains(t, body, "<title>Ticket #7</title>")
	assert.NotContains(t, body, "<title>Helpdesk</title>",
		"the per-page title replaces the router-wide one, it does not add a second")
}

func TestTitle_anEmptyTitleKeepsTheRouterWideOne(t *testing.T) {
	t.Parallel()
	_, body := titledBody(t, titledPage{}, via.WithHead(via.Head{Title: "Helpdesk"}))

	assert.Contains(t, body, "<title>Helpdesk</title>")
}

func TestTitle_readsDataLoadedByOnInit(t *testing.T) {
	t.Parallel()
	_, body := titledBody(t, loadedTitlePage{store: map[string]string{"7": "Printer on fire"}})

	assert.Contains(t, body, "<title>Ticket #7 — Printer on fire</title>",
		"Title must run AFTER OnInit, or it sees the zero value")
}

func TestTitle_isEscaped(t *testing.T) {
	t.Parallel()
	_, body := titledBody(t, titledPage{title: `</title><script>x()</script>`})

	assert.NotContains(t, body, "<script>x()</script>", "the title must not be able to close its element")
	assert.Contains(t, body, "&lt;/title&gt;")
}

// The defect the *Ctx.Title shape had: one head shared down the request tree
// let any nested child silently rename the page.
func TestTitle_anEmbeddedUnitCannotRenameThePage(t *testing.T) {
	t.Parallel()
	_, body := titledBody(t, titledPage{title: "outer", Child: titledChild{title: "inner"}},
		via.WithHead(via.Head{Title: "router-wide"}))

	assert.Contains(t, body, "<title>outer</title>")
	assert.NotContains(t, body, "inner", "only the MOUNTED root's Title names the document")
}

// The warning is once per TYPE per process (embedHookWarned), so a -count>1 run
// would see an empty log on every pass but the first: capture it once.
func TestTitle_warnsWhenAnEmbedDeclaresOne(t *testing.T) {
	logged := warnChildOnce(t)

	assert.Contains(t, logged, "warnChild.Title is ignored")
}

// A per-page title must not be a way to widen the policy: the CSP is derived
// from the router-wide Head alone and must be byte-identical whatever a page
// declares.
var warnChildOnce = func() func(*testing.T) string {
	var once sync.Once
	var out string
	return func(t *testing.T) string {
		t.Helper()
		once.Do(func() {
			out = captureLog(t, func() { titledBody(t, warnPage{Child: warnChild{title: "inner"}}) })
		})
		return out
	}
}()

type warnPage struct{ Child warnChild }

func (p *warnPage) View() h.H { return h.Div(via.Embed(p.Child)) }

type warnChild struct{ title string }

func (c *warnChild) Title() string { return c.title }
func (c *warnChild) View() h.H     { return h.Span(h.Str("child")) }

func TestTitle_doesNotTouchTheCSP(t *testing.T) {
	t.Parallel()
	plain, _ := titledBody(t, titledPage{})
	titled, _ := titledBody(t, titledPage{title: "x"})

	assert.Equal(t, plain.Header.Get("Content-Security-Policy"), titled.Header.Get("Content-Security-Policy"))
}
