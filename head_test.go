package via_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestDocumentHead_rendersTheDeclaredShell(t *testing.T) {
	t.Parallel()
	_, body := headResp(t, via.Head{
		Lang: "pt-PT",
		Raw:  `<meta name="viewport" content="width=device-width, initial-scale=1"><link rel="icon" href="/favicon.png">`,
	})
	for _, want := range []string{
		`<html lang="pt-PT">`,
		`<meta name="viewport" content="width=device-width, initial-scale=1">`,
		`<link rel="icon" href="/favicon.png">`,
	} {
		assert.Contains(t, body, want)
	}
}

func TestDocumentHead_zeroValueChangesNothing(t *testing.T) {
	t.Parallel()
	_, with := headResp(t, via.Head{})
	_, without := do(t, headSrv(t), http.MethodGet, "/", "")
	assert.Equal(t, without, with)
	assert.Contains(t, with, "<html><head>")
	assert.NotContains(t, with, "<title>")
}

func TestDocumentHead_derivesTheCSPFromDeclaredAssets(t *testing.T) {
	t.Parallel()
	resp, body := headResp(t, via.Head{Assets: via.Assets{
		Styles:      []via.Style{{Href: "https://fonts.googleapis.com/css2?family=Inter"}},
		Scripts:     []via.Script{{Src: "https://plausible.io/script.js", Defer: true}},
		FontOrigins: []string{"https://fonts.gstatic.com"},
	}})
	csp := resp.Header.Get("Content-Security-Policy")
	assert.Contains(t, csp, "style-src 'self' https://fonts.googleapis.com;")
	assert.Contains(t, csp, "https://plausible.io;")
	assert.Contains(t, csp, "font-src 'self' https://fonts.gstatic.com;")
	assert.NotContains(t, csp, "style-src 'self' https://plausible.io")
	assert.Contains(t, body, `<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Inter">`)
	assert.Contains(t, body, `<script defer src="https://plausible.io/script.js"></script>`)
}

func TestDocumentHead_noAssetsDoesNotWidenTheCSP(t *testing.T) {
	t.Parallel()
	resp, _ := headResp(t, via.Head{Raw: `<link rel="icon" href="/favicon.png">`})
	assert.Contains(t, resp.Header.Get("Content-Security-Policy"), "style-src 'self';")
}

func TestDocumentHead_inlineStyleIsAdmittedByItsHash(t *testing.T) {
	t.Parallel()
	const css = "body{margin:0}"
	resp, body := headResp(t, via.Head{Assets: via.Assets{Styles: []via.Style{{Inline: css}}}})
	assert.Contains(t, body, "<style>"+css+"</style>")
	assert.Contains(t, resp.Header.Get("Content-Security-Policy"), hashSource(css))
}

func TestDocumentHead_preloadWidensTheDirectiveItsAsNames(t *testing.T) {
	t.Parallel()
	resp, body := headResp(t, via.Head{Assets: via.Assets{
		Preload: []via.Preload{{Href: "https://cdn.example.com/hero.avif", As: "image"}},
	}})
	csp := resp.Header.Get("Content-Security-Policy")
	assert.Contains(t, csp, "img-src 'self' https://cdn.example.com;")
	assert.NotContains(t, csp, "script-src 'self' 'unsafe-eval' https://cdn.example.com")
	assert.Contains(t, body, `<link rel="preload" href="https://cdn.example.com/hero.avif" as="image">`)
}

func TestDocumentHead_rejectsMalformedHeadsAtStartup(t *testing.T) {
	t.Parallel()
	for name, head := range map[string]via.Head{
		"bad lang":             {Lang: `en" onload="x`},
		"relative font origin": {Assets: via.Assets{FontOrigins: []string{"/fonts"}}},
		"unsafe scheme as src": {Assets: via.Assets{Scripts: []via.Script{{Src: "javascript:alert(1)"}}}},
		"data url as href":     {Assets: via.Assets{Styles: []via.Style{{Href: "data:text/css,body{}"}}}},
		"script with src and inline": {Assets: via.Assets{
			Scripts: []via.Script{{Src: "/a.js", Inline: "x()"}}}},
		"script with neither": {Assets: via.Assets{Scripts: []via.Script{{}}}},
		"style with neither":  {Assets: via.Assets{Styles: []via.Style{{}}}},
		"script breakout": {Assets: via.Assets{
			Scripts: []via.Script{{Inline: "x()</script><script>y()"}}}},
		"style breakout": {Assets: via.Assets{
			Styles: []via.Style{{Inline: "a{}</style><script>x</script>"}}}},
		"unknown preload as": {Assets: via.Assets{Preload: []via.Preload{{Href: "/a.wasm", As: "wasm"}}}},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Panics(t, func() { via.Handler(headPage{}, via.WithHead(head)) })
		})
	}
}

func TestDocumentHead_rejectsScriptsAndStylesSmuggledThroughRaw(t *testing.T) {
	t.Parallel()
	for name, raw := range map[string]string{
		"inline script":   `<script>window.x = 1</script>`,
		"external script": `<SCRIPT src="/a.js"></SCRIPT>`,
		"inline style":    `<style>body{margin:0}</style>`,
	} {
		t.Run(name, func(t *testing.T) {
			assert.PanicsWithValue(t, panicOf(t, raw), func() {
				via.Handler(headPage{}, via.WithHead(via.Head{Raw: raw}))
			})
		})
	}
}

func panicOf(t *testing.T, raw string) string {
	t.Helper()
	bad := "<script"
	if !strings.Contains(strings.ToLower(raw), bad) {
		bad = "<style"
	}
	return `via: WithHead: Raw contains "` + bad + `" — via never parses Raw, so the CSP ` +
		`cannot admit it and the browser would block it silently; declare it in Head.Assets instead`
}

func TestDocumentHead_cspStaysAPureFunctionOfTheConfig(t *testing.T) {
	t.Parallel()
	head := via.Head{Assets: via.Assets{
		Scripts:     []via.Script{{Src: "https://cdn.example.com/a.js"}},
		FontOrigins: []string{"https://fonts.gstatic.com"},
	}}
	r1, _ := headResp(t, head)
	r2, _ := headResp(t, head)
	require.NotEmpty(t, r1.Header.Get("Content-Security-Policy"))
	assert.Equal(t, r1.Header.Get("Content-Security-Policy"), r2.Header.Get("Content-Security-Policy"))
}

type metaPage struct {
	title string
	Child metaChild
}

var _ via.PageMetaer = (*metaPage)(nil)

func (p *metaPage) PageMeta() via.Meta { return via.Meta{Title: p.title} }
func (p *metaPage) View() h.H          { return h.Div(h.Str("hi"), via.Child(p.Child)) }

type metaChild struct{ title string }

func (c *metaChild) PageMeta() via.Meta { return via.Meta{Title: c.title} }
func (c *metaChild) View() h.H          { return h.Span(h.Str("child")) }

type loadedMetaPage struct {
	store   map[string]string
	subject string
}

var _ via.Initer = (*loadedMetaPage)(nil)
var _ via.PageMetaer = (*loadedMetaPage)(nil)

func (p *loadedMetaPage) OnInit(*via.Ctx) error { p.subject = p.store["7"]; return nil }
func (p *loadedMetaPage) PageMeta() via.Meta {
	return via.Meta{Title: "Ticket #7 — " + p.subject, Description: p.subject}
}
func (p *loadedMetaPage) View() h.H { return h.Div(h.Str(p.subject)) }

func metaBody[T any, PT interface {
	*T
	View() h.H
}](t *testing.T, p T, opts ...via.Option) (*http.Response, string) {
	t.Helper()
	srv := httptest.NewServer(via.Handler[T, PT](p, opts...))
	t.Cleanup(srv.Close)
	return do(t, srv, http.MethodGet, "/", "")
}

func TestPageMeta_writesEveryInertSlot(t *testing.T) {
	t.Parallel()
	_, body := metaBody(t, inertPage{})
	for _, want := range []string{
		"<title>Ticket #7</title>",
		`<meta name="description" content="A printer is on fire">`,
		`<meta name="robots" content="noindex">`,
		`<link rel="canonical" href="https://example.com/t/7">`,
		`<meta property="og:title" content="Ticket #7">`,
		`<meta property="og:type" content="article">`,
		`<meta name="twitter:card" content="summary">`,
	} {
		assert.Contains(t, body, want)
	}
}

type inertPage struct{}

func (inertPage) PageMeta() via.Meta {
	return via.Meta{
		Title: "Ticket #7", Description: "A printer is on fire",
		Canonical: "https://example.com/t/7", Robots: "noindex",
		OG:      map[string]string{"title": "Ticket #7", "type": "article"},
		Twitter: map[string]string{"card": "summary"},
	}
}
func (inertPage) View() h.H { return h.Div(h.Str("hi")) }

func TestPageMeta_readsDataLoadedByOnInit(t *testing.T) {
	t.Parallel()
	_, body := metaBody(t, loadedMetaPage{store: map[string]string{"7": "Printer on fire"}})

	assert.Contains(t, body, "<title>Ticket #7 — Printer on fire</title>",
		"PageMeta must run AFTER OnInit, or it sees the zero value")
	assert.Contains(t, body, `<meta name="description" content="Printer on fire">`)
}

func TestPageMeta_escapesEveryInertSlot(t *testing.T) {
	t.Parallel()
	_, body := metaBody(t, escapingPage{})

	assert.NotContains(t, body, "<script>x()</script>", "the title must not be able to close its element")
	assert.Contains(t, body, "&lt;/title&gt;")
	assert.NotContains(t, body, `content="" onload="x"`, "an attribute value must not break out")
	assert.Contains(t, body, "&#34; onload=&#34;x")
}

type escapingPage struct{}

func (escapingPage) PageMeta() via.Meta {
	return via.Meta{
		Title:       `</title><script>x()</script>`,
		Description: `" onload="x`,
		OG:          map[string]string{"title": `" onload="x`},
	}
}
func (escapingPage) View() h.H { return h.Div(h.Str("hi")) }

func TestPageMeta_anEmbeddedUnitCannotRenameThePage(t *testing.T) {
	t.Parallel()
	_, body := metaBody(t, metaPage{title: "outer", Child: metaChild{title: "inner"}})

	assert.Contains(t, body, "<title>outer</title>")
	assert.NotContains(t, body, "inner", "only the MOUNTED root's PageMeta names the document")
}

func TestPageMeta_warnsWhenAChildDeclaresOne(t *testing.T) {
	logged := warnChildOnce(t)

	assert.Contains(t, logged, "warnChild.PageMeta is ignored")
}

var warnChildOnce = func() func(*testing.T) string {
	var once sync.Once
	var out string
	return func(t *testing.T) string {
		t.Helper()
		once.Do(func() {
			out = captureLog(t, func() { metaBody(t, warnPage{Child: warnChild{title: "inner"}}) })
		})
		return out
	}
}()

type warnPage struct{ Child warnChild }

func (p *warnPage) View() h.H { return h.Div(via.Child(p.Child)) }

type warnChild struct{ title string }

func (c *warnChild) PageMeta() via.Meta { return via.Meta{Title: c.title} }
func (c *warnChild) View() h.H          { return h.Span(h.Str("child")) }

func TestPageMeta_inertFieldsDoNotTouchTheCSP(t *testing.T) {
	t.Parallel()
	plain, _ := metaBody(t, metaPage{})
	titled, _ := metaBody(t, metaPage{title: "x"})

	assert.Equal(t, plain.Header.Get("Content-Security-Policy"), titled.Header.Get("Content-Security-Policy"))
}

type assetPage struct{}

func (assetPage) PageMeta() via.Meta {
	return via.Meta{Title: "charts", Assets: via.Assets{
		Scripts: []via.Script{{Src: "https://cdn.charts.example/c.js", Module: true}},
		Styles:  []via.Style{{Inline: "svg{display:block}"}},
	}}
}
func (assetPage) View() h.H { return h.Div(h.Str("chart")) }

type plainPage struct{}

func (plainPage) PageMeta() via.Meta { return via.Meta{Title: "plain"} }
func (plainPage) View() h.H          { return h.Div(h.Str("plain")) }

func TestPageMeta_assetsWidenOnlyTheirOwnMount(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	via.Mount(r, "/charts", assetPage{})
	via.Mount(r, "/plain", plainPage{})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	charts, body := do(t, srv, http.MethodGet, "/charts", "")
	plain, _ := do(t, srv, http.MethodGet, "/plain", "")

	assert.Contains(t, charts.Header.Get("Content-Security-Policy"), "https://cdn.charts.example")
	assert.Contains(t, charts.Header.Get("Content-Security-Policy"), hashSource("svg{display:block}"))
	assert.NotContains(t, plain.Header.Get("Content-Security-Policy"), "https://cdn.charts.example")
	assert.Contains(t, plain.Header.Get("Content-Security-Policy"), "style-src 'self';")
	assert.Contains(t, body, `<script type="module" src="https://cdn.charts.example/c.js"></script>`)
}

func TestPageMeta_patchResponsesCarryTheGlobalCSP(t *testing.T) {
	t.Parallel()
	srv := newCounter(t)
	_, page := do(t, srv, http.MethodGet, "/", "")
	resp, _ := do(t, srv, http.MethodPost, actionURL(t, page, "r", 1), "{}")

	csp := resp.Header.Get("Content-Security-Policy")
	assert.Contains(t, csp, "style-src 'self';")
	assert.Contains(t, csp, "frame-ancestors 'self'")
}

type varyingAssetPage struct{ n int }

func (p *varyingAssetPage) OnInit(*via.Ctx) error { p.n = 1; return nil }
func (p *varyingAssetPage) PageMeta() via.Meta {
	if p.n == 0 {
		return via.Meta{}
	}
	return via.Meta{Assets: via.Assets{Scripts: []via.Script{{Src: "/late.js"}}}}
}
func (p *varyingAssetPage) View() h.H { return h.Div(h.Str("x")) }

func TestPageMeta_panicsAtMountWhenAssetsDependOnData(t *testing.T) {
	t.Parallel()
	assert.PanicsWithValue(t,
		"via: PageMeta().Assets of *via_test.varyingAssetPage depends on the page's data; "+
			"the CSP is built once at Mount, so assets must be a constant of the type. "+
			"via read the assets twice — once off the mounted literal and once off a copy "+
			"with its zero fields filled in with synthetic values, standing in for what "+
			"OnInit would load — and got two different answers. Move the asset into the "+
			"mounted literal, declare it router-wide with WithHead, or hold it in a "+
			"package-level var.",
		func() { via.Mount(via.NewRouter(), "/", varyingAssetPage{}) })
}

type litAssetPage struct{ cdn string }

func (p *litAssetPage) PageMeta() via.Meta {
	return via.Meta{Assets: via.Assets{Scripts: []via.Script{{Src: p.cdn + "/app.js"}}}}
}
func (p *litAssetPage) View() h.H { return h.Div(h.Str("x")) }

func TestPageMeta_assetsFromTheMountedLiteralAreConstant(t *testing.T) {
	t.Parallel()
	assert.NotPanics(t, func() {
		via.Mount(via.NewRouter(), "/", litAssetPage{cdn: "https://cdn.example"})
	})
	_, body := metaBody(t, litAssetPage{cdn: "https://cdn.example"})
	assert.Contains(t, body, `src="https://cdn.example/app.js"`)
}

type lateAssetPage struct{ extra []string }

func (p *lateAssetPage) OnInit(*via.Ctx) error { p.extra = []string{"/late.js"}; return nil }
func (p *lateAssetPage) PageMeta() via.Meta {
	var sc []via.Script
	for _, src := range p.extra {
		sc = append(sc, via.Script{Src: src})
	}
	return via.Meta{Assets: via.Assets{Scripts: sc}}
}
func (p *lateAssetPage) View() h.H { return h.Div(h.Str("x")) }

func TestPageMeta_panicsOnRenderWhenAssetsTheProbeCannotSeeMove(t *testing.T) {
	logged := captureLog(t, func() {
		resp, _ := metaBody(t, lateAssetPage{})
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	})
	assert.Contains(t, logged, "PageMeta().Assets of *via_test.lateAssetPage depends on request data; "+
		"assets must be a constant of the type")
}

func TestPageMeta_validatesItsAssetsAtMount(t *testing.T) {
	t.Parallel()
	assert.PanicsWithValue(t,
		`via: via_test.badAssetPage.PageMeta().Assets: Script.Src "javascript:alert(1)" is not a relative URL or an absolute http(s) one`,
		func() { via.Mount(via.NewRouter(), "/", badAssetPage{}) })
}

type badAssetPage struct{}

func (badAssetPage) PageMeta() via.Meta {
	return via.Meta{Assets: via.Assets{Scripts: []via.Script{{Src: "javascript:alert(1)"}}}}
}
func (badAssetPage) View() h.H { return h.Div(h.Str("x")) }

// The boot probe writes into a copy of the mounted literal, and a sync.Mutex's
// zero state is an invariant: perturbing it makes the next Lock a runtime throw
// that no recover can catch, so the process dies at Mount with no via wording.
// A page holding a mutex (directly, or through a store) is the ordinary shape.
type mutexAssetPage struct {
	mu    sync.Mutex
	once  sync.Once
	since time.Time
	store *mutexStore
	cdn   string
}

type mutexStore struct {
	mu sync.Mutex
	n  int
}

func (s *mutexStore) bump() int { s.mu.Lock(); defer s.mu.Unlock(); s.n++; return s.n }

func (p *mutexAssetPage) PageMeta() via.Meta {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.once.Do(func() {})
	_ = p.since.IsZero()
	if p.store != nil {
		p.store.bump()
	}
	return via.Meta{Title: "mutex", Assets: via.Assets{Scripts: []via.Script{{Src: p.cdn + "/app.js"}}}}
}
func (p *mutexAssetPage) View() h.H { return h.Div(h.Str("x")) }

func TestMount_probeLeavesForeignStructsAlone(t *testing.T) {
	t.Parallel()
	require.NotPanics(t, func() {
		r := via.NewRouter()
		via.Mount(r, "/", mutexAssetPage{cdn: "https://cdn.example", store: &mutexStore{}})
		t.Cleanup(r.Close)
		srv := httptest.NewServer(r)
		t.Cleanup(srv.Close)
		resp, body := do(t, srv, http.MethodGet, "/", "")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, body, "https://cdn.example/app.js")
	})
}

type mutexVaryingAssetPage struct {
	mu sync.Mutex
	n  int
}

func (p *mutexVaryingAssetPage) OnInit(*via.Ctx) error { p.n = 1; return nil }
func (p *mutexVaryingAssetPage) PageMeta() via.Meta {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.n == 0 {
		return via.Meta{}
	}
	return via.Meta{Assets: via.Assets{Scripts: []via.Script{{Src: "/late.js"}}}}
}
func (p *mutexVaryingAssetPage) View() h.H { return h.Div(h.Str("x")) }

func TestMount_probeStillCatchesDataDependentAssetsPastAMutex(t *testing.T) {
	t.Parallel()
	require.PanicsWithValue(t,
		"via: PageMeta().Assets of *via_test.mutexVaryingAssetPage depends on the page's data; "+
			"the CSP is built once at Mount, so assets must be a constant of the type. "+
			"via read the assets twice — once off the mounted literal and once off a copy "+
			"with its zero fields filled in with synthetic values, standing in for what "+
			"OnInit would load — and got two different answers. Move the asset into the "+
			"mounted literal, declare it router-wide with WithHead, or hold it in a "+
			"package-level var.",
		func() { via.Mount(via.NewRouter(), "/", mutexVaryingAssetPage{}) })
}

type probeRefusingPage struct{ n int }

func (p *probeRefusingPage) PageMeta() via.Meta {
	if p.n != 0 {
		panic("no")
	}
	return via.Meta{Title: "ok"}
}
func (p *probeRefusingPage) View() h.H { return h.Div(h.Str("x")) }

func TestMount_logsWhenTheAssetProbeIsRefused(t *testing.T) {
	logged := captureLog(t, func() {
		require.NotPanics(t, func() { via.Mount(via.NewRouter(), "/", probeRefusingPage{}) })
	})
	assert.Contains(t, logged, "*via_test.probeRefusingPage")
	assert.Contains(t, logged, "SKIPPED")
}
