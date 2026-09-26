package via_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// store is the in-server state the counter tracks — a plain app dependency.
type store struct {
	mu sync.Mutex
	n  int
}

func (s *store) Value() int { s.mu.Lock(); defer s.mu.Unlock(); return s.n }
func (s *store) Add(d int)  { s.mu.Lock(); s.n += d; s.mu.Unlock() }

// counter is the slice-1 component under test, exercised through the public via
// API. It mirrors internal/example/counter: server-authoritative state in an injected
// dependency, element-patched on each action.
type counter struct{ count *store }

func (c *counter) Inc(ctx *via.Ctx) { c.count.Add(1) }
func (c *counter) Dec(ctx *via.Ctx) { c.count.Add(-1) }

func (c *counter) View() h.H {
	return h.Div(
		h.H1(h.Str(c.count.Value())),
		h.Button(via.On("click", c.Dec), h.Str("-")),
		h.Button(via.On("click", c.Inc), h.Str("+")),
	)
}

// newCounter starts one httptest server whose counter shares a single store, so
// sequential requests observe persisted server state.
func newCounter(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(via.Handler(counter{count: &store{}}))
	t.Cleanup(srv.Close)
	return srv
}

func do(t *testing.T, srv *httptest.Server, method, path, body string) (*http.Response, string) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, srv.URL+path, rdr)
	require.NoError(t, err, "build request")
	// Simulate a same-origin browser fetch so the origin floor (on the action
	// POST and the SSE GET) admits the request; tests that exercise the floor
	// itself build their own requests (see post()/sseStatus in security_test.go).
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if method == http.MethodPost {
		// The bundled Datastar client sends this on every @post; dispatch reads
		// it to tell a JSON action apart from a native form submit.
		req.Header.Set("Datastar-Request", "true")
	}
	resp, err := srv.Client().Do(req)
	require.NoError(t, err, "request failed")
	t.Cleanup(func() { resp.Body.Close() })
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "read body")
	return resp, string(b)
}

// The raw-httptest helpers below back the tests that assert on response headers
// or SSE frame structure (csp, theme, live, and via's own Content-Type checks),
// which the vt harness deliberately does not expose; the behavior-only tests
// (signals, compose, security, state) drive vt instead.
func serve(t testing.TB, handler http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// post issues a POST to the action endpoint with exactly the given headers and
// no defaults, so a test pins precisely the origin signal it intends to send
// (do() injects same-origin, which would mask the origin floor).
func post(t *testing.T, srv *httptest.Server, path, body string, headers map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
	require.NoError(t, err)
	// A JSON action POST — every caller of post() carries a Datastar signals
	// body, never a native form submit.
	req.Header.Set("Datastar-Request", "true")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp, readAll(t, resp)
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(b)
}

func sameOrigin() map[string]string { return map[string]string{"Sec-Fetch-Site": "same-origin"} }

// actionURL extracts the n-th action URL child rendered, in document order,
// out of html. The wire id is a hash of the handler's func name (and any ?a=
// row datum rides with it), so a test posts the URL the page actually
// shipped rather than a hand-built path.
func actionURL(t *testing.T, html string, child string, n int) string {
	t.Helper()
	urls := actionURLsOf(html, child)
	require.Greaterf(t, len(urls), n, "action %s/%d not found on rendered page:\n%s", child, n, html)
	return urls[n]
}

// actionURLsOf reads child's action URLs off a page in document order. A
// binding with a query carries it in the data-via-q-<event> attribute right
// before its data-on, and the expression appends it to the path.
func actionURLsOf(page, child string) []string {
	re := regexp.MustCompile(`(?:data-via-q-[^=\s]+="([^"]*)" data-on:[^=\s]+="@post\('|@post\('|action=")` +
		`([^'"]*_via/a/` + child + `/[A-Za-z0-9_-]+(?:[?&][^'"]*)?)['"]`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(page, -1) {
		out = append(out, m[2]+m[1])
	}
	return out
}

func TestPage_shipsServerRenderedSkeleton(t *testing.T) {
	t.Parallel()
	resp, body := do(t, newCounter(t), http.MethodGet, "/", "")

	ct := resp.Header.Get("Content-Type")
	assert.True(t, strings.HasPrefix(ct, "text/html"), "page Content-Type = %q, want text/html", ct)
	for _, want := range []string{
		`<div id="root"`,
		`<h1>0</h1>`, // value rendered server-side, not a signal
		`data-on:click="@post('` + actionURL(t, body, "r", 0) + `'`, // Dec, declared first
		`data-on:click="@post('` + actionURL(t, body, "r", 1) + `'`, // Inc, declared second
		`src="/_via/datastar.js?v=`,                                 // module script tag (external, admitted by 'self')
	} {
		assert.Contains(t, body, want, "page missing skeleton fragment")
	}
}

func TestEventBinding_usesDatastarColonSyntaxNotDeadDashForm(t *testing.T) {
	t.Parallel()
	_, body := do(t, newCounter(t), http.MethodGet, "/", "")

	assert.Contains(t, body, `data-on:click="@post(`, "page is missing the v1 colon event binding data-on:click")
	assert.NotContains(t, body, "data-on-click", "page ships the dead v0.x dash form data-on-click (silently ignored by Datastar v1.0.2)")
}

func TestAction_elementPatchesAndPersists(t *testing.T) {
	t.Parallel()
	srv := newCounter(t)
	_, page := do(t, srv, http.MethodGet, "/", "")
	dec, inc := actionURL(t, page, "r", 0), actionURL(t, page, "r", 1)

	resp, body := do(t, srv, http.MethodPost, inc, "{}") // Inc: 0 -> 1
	ct := resp.Header.Get("Content-Type")
	assert.True(t, strings.HasPrefix(ct, "text/html"), "Content-Type = %q, want text/html (element-patch)", ct)
	for _, want := range []string{`<div id="root"`, `<h1>1</h1>`} {
		assert.Contains(t, body, want, "patch missing fragment")
	}

	_, body = do(t, srv, http.MethodPost, inc, "{}") // 1 -> 2
	assert.Contains(t, body, `<h1>2</h1>`, "second Inc did not persist to 2")
	_, body = do(t, srv, http.MethodPost, dec, "{}") // 2 -> 1
	assert.Contains(t, body, `<h1>1</h1>`, "Dec did not bring state back to 1")
}

func TestUnknownAction_isGone(t *testing.T) {
	t.Parallel()
	srv := newCounter(t)
	_, page := do(t, srv, http.MethodGet, "/", "")
	url := swapActionID(t, actionURL(t, page, "r", 0), "zzzzzzzz")

	resp, _ := do(t, srv, http.MethodPost, url, "{}")
	assert.Equal(t, http.StatusGone, resp.StatusCode, "want 410 Gone")
}

func TestAction_invalidUTF8InSignalBodyIsNotA400(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(boundForm{}))
	status, _ := app.Action(0).Body("{\"name\":\"\xff\"}").Fire()
	assert.Equal(t, http.StatusOK, status)
}

func TestAction_duplicateSignalKeyLastWins(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(boundForm{}))
	status, frag := app.Action(0).Body(`{"name":"one","name":"two"}`).Fire()
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, frag, "two")
	assert.NotContains(t, frag, "one")
}

func TestEmbeddedDatastarClient_isServedAsJS(t *testing.T) {
	t.Parallel()
	resp, body := do(t, newCounter(t), http.MethodGet, "/_via/datastar.js", "")

	require.Equal(t, http.StatusOK, resp.StatusCode, "want 200")
	ct := resp.Header.Get("Content-Type")
	assert.True(t, strings.HasPrefix(ct, "text/javascript"), "Content-Type = %q, want text/javascript", ct)
	assert.NotEmpty(t, body, "served datastar.js was empty")
}

var datastarSrc = regexp.MustCompile(`<script type="module" src="(/_via/datastar\.js\?v=[0-9a-f]+)">`)

func getWithHeader(t *testing.T, srv *httptest.Server, path, key, val string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	require.NoError(t, err)
	if key != "" {
		req.Header.Set(key, val)
	}
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, string(b)
}

func TestEmbeddedDatastarClient_cachesByURL(t *testing.T) {
	t.Parallel()
	srv := newCounter(t)
	_, page := do(t, srv, http.MethodGet, "/", "")
	m := datastarSrc.FindStringSubmatch(page)
	require.NotNil(t, m, "page must reference datastar.js by a versioned URL")

	tests := []struct {
		name  string
		path  string
		cache string
	}{
		{"versioned URL is immutable", m[1], "public, max-age=31536000, immutable"},
		{"bare URL revalidates", "/_via/datastar.js", "public, max-age=3600, must-revalidate"},
		{"stale version revalidates", "/_via/datastar.js?v=0000", "public, max-age=3600, must-revalidate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp, body := getWithHeader(t, srv, tt.path, "", "")
			require.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, tt.cache, resp.Header.Get("Cache-Control"))
			etag := resp.Header.Get("ETag")
			assert.Regexp(t, `^"[0-9a-f]+"$`, etag, "a strong ETag, quoted, no W/ prefix")
			assert.NotEmpty(t, body)
		})
	}
}

func TestEmbeddedDatastarClient_answersAMatchingIfNoneMatchWith304(t *testing.T) {
	t.Parallel()
	srv := newCounter(t)
	resp, _ := getWithHeader(t, srv, "/_via/datastar.js", "", "")
	etag := resp.Header.Get("ETag")
	require.NotEmpty(t, etag)

	resp, body := getWithHeader(t, srv, "/_via/datastar.js", "If-None-Match", etag)
	assert.Equal(t, http.StatusNotModified, resp.StatusCode)
	assert.Empty(t, body)
	assert.Equal(t, etag, resp.Header.Get("ETag"))

	resp, body = getWithHeader(t, srv, "/_via/datastar.js", "If-None-Match", `"stale"`)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, body)
}

func TestAction_respondsWithElementPatchNotSignalPatch(t *testing.T) {
	t.Parallel()
	srv := newCounter(t)
	_, page := do(t, srv, http.MethodGet, "/", "")
	resp, body := do(t, srv, http.MethodPost, actionURL(t, page, "r", 1), "{}")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	ct := resp.Header.Get("Content-Type")
	assert.Truef(t, strings.HasPrefix(ct, "text/html"), "action must respond text/html, got %q", ct)
	assert.NotContains(t, ct, "application/json")
	assert.Contains(t, body, `<div id="root"`, "element-patch must carry the #root morph target")
}

// noopComp's action is a pure side effect that changes nothing the View reads —
// the kind of action that logs, enqueues a job, or marks something the UI has
// already reflected.
type noopComp struct{}

func (n *noopComp) Ping(*via.Ctx) {}
func (n *noopComp) View() h.H {
	return h.Div(h.Button(via.On("click", n.Ping), h.Str("ping")))
}

func TestAction_returns204WhenViewIsUnchanged(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(noopComp{}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	resp, body := post(t, srv, actionURL(t, page, "r", 0), "{}", sameOrigin())
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	assert.Empty(t, body)
}

type formComp struct{ q via.Signal[string] }

func (c *formComp) Go(ctx *via.Ctx) {}
func (c *formComp) View() h.H {
	return h.Form(via.On("submit", c.Go), h.Input(c.q.Bind()))
}

func TestOn_submitWiresAPostAction(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Handler(formComp{})), http.MethodGet, "/", "")
	assert.Contains(t, body, `data-on:submit="@post('`+actionURL(t, body, "r", 0)+`'`)
	assert.NotContains(t, body, "data-on-submit", "must use the colon form, not the dead dash form")
}

// reqEchoer is a plain component whose action copies a header off the
// triggering request into a rendered field.
type reqEchoer struct{ echo string }

func (r *reqEchoer) Grab(ctx *via.Ctx) { r.echo = ctx.Request().Header.Get("X-Echo") }
func (r *reqEchoer) View() h.H {
	return h.Div(h.Button(via.On("click", r.Grab), h.Str("x")), h.P(h.Str(r.echo)))
}

func TestAction_canReadTheTriggeringRequest(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(reqEchoer{}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	_, body := post(t, srv, actionURL(t, page, "r", 0), "{}", map[string]string{
		"Sec-Fetch-Site": "same-origin",
		"X-Echo":         "hello-from-header",
	})
	assert.Contains(t, body, "hello-from-header", "the action must see the triggering request via ctx.Request()")
}

// digestEchoer renders a bound signal's raw value straight into the page, so a
// test can post hostile text through the signal channel (a header can't carry
// a literal NUL byte; a JSON body can).
type digestEchoer struct {
	q    via.Signal[string]
	echo string
}

func (c *digestEchoer) Grab(ctx *via.Ctx) { c.echo = c.q.Get() }
func (c *digestEchoer) View() h.H {
	return h.Div(h.Input(c.q.Bind()), h.Button(via.On("click", c.Grab), h.Str("x")), h.P(h.Str(c.echo)))
}

func TestAction_digestPlaceholderCannotBeForgedByUserText(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(digestEchoer{}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	slot := attrValue(t, page, "data-bind")
	nul := string(byte(0))
	hostile := nul + "vD0" + nul
	reqBody, err := json.Marshal(map[string]string{slot: hostile})
	require.NoError(t, err)
	url := actionURL(t, page, "r", 0)
	digest := url[strings.Index(url, "v=")+2:]
	_, body := post(t, srv, url, string(reqBody), sameOrigin())
	p := regexp.MustCompile(`<p>(.*?)</p>`).FindStringSubmatch(body)
	require.Len(t, p, 2, "rendered paragraph not found:\n%s", body)
	assert.NotContains(t, p[1], digest, "hostile text carrying the raw digest-placeholder token got the real shape digest spliced into it")
}

// changePicker exercises On("change", ...): a select whose commit posts an action,
// with no value carried (that's OnArg's job, tested elsewhere).
type changePicker struct{ got string }

func (a *changePicker) Pick(ctx *via.Ctx) { a.got = "picked" }
func (a *changePicker) View() h.H {
	return h.Div(
		h.El("select", via.On("change", a.Pick)),
		h.P(h.Str(a.got)),
	)
}

func TestOn_changeFiresHandlerOnCommit(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(changePicker{}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	assert.Contains(t, page, `data-on:change`, "OnChange must bind the change event")

	_, body := do(t, srv, http.MethodPost, actionURL(t, page, "r", 0), "{}")
	assert.Contains(t, body, "picked", "OnChange's handler did not run")
}

// collidePage mints "a_b" twice: once for the nested A.B (nested struct names
// join with "_") and once for the sibling field A_b. Two spans bind one slot,
// one slot is declared, and the hydrator map keeps whichever came last — so a
// POST writes the wrong field, silently.
type collidePage struct {
	A   struct{ B via.Signal[int] }
	A_b via.Signal[int]
}

func (p *collidePage) View() h.H { return h.Div(p.A.B.Bind(), p.A_b.Bind()) }

// childCollidePage's field A__b mints "a__b" in the parent, which is exactly
// the slot the child of field A gives its own signal B ("a__" + "b").
type childKidB struct{ B via.Signal[int] }

func (k *childKidB) View() h.H { return k.B.Bind() }

type childCollidePage struct {
	A    childKidB
	A__b via.Signal[int]
}

func (p *childCollidePage) View() h.H { return h.Div(via.Child(p.A), p.A__b.Bind()) }

// panicMsg runs fn and returns what it panicked with, or "" if it returned.
func panicMsg(fn func()) (msg string) {
	defer func() {
		if rec := recover(); rec != nil {
			msg = fmt.Sprint(rec)
		}
	}()
	fn()
	return ""
}

func assertSlotPanic(t *testing.T, want string, mount func()) {
	t.Helper()
	msg := panicMsg(mount)
	assert.Contains(t, msg, "found at Mount", "the boot render must catch it, not a request")
	assert.Contains(t, msg, want)
}

// assertMountPanic drives the fail-at-boot claim: a composition whose signals
// cannot be named must be rejected where the app is wired, not once per request
// for the life of the process.
func assertMountPanic(t *testing.T, want string, mount func()) {
	t.Helper()
	msg := panicMsg(mount)
	require.NotEmpty(t, msg, "expected a panic at Mount containing %q", want)
	assert.Contains(t, msg, want)
}

func TestSignal_duplicateSlotNamePanicsAtMount(t *testing.T) {
	t.Parallel()
	assertMountPanic(t, "signal slot a_b is minted twice", func() { via.Handler(collidePage{}) })
}

type tagBareKey struct {
	Open via.SignalCS[bool] `via:"true"`
}

func (p *tagBareKey) View() h.H { return h.Div() }

type tagUnknownKey struct {
	Count via.Signal[int] `via:"seed=1"`
}

func (p *tagUnknownKey) View() h.H { return h.Div(p.Count.Display()) }

type tagKeyNoValue struct {
	Count via.Signal[int] `via:"init"`
}

func (p *tagKeyNoValue) View() h.H { return h.Div(p.Count.Display()) }

func TestMount_panicsOnAnUnknownTagKey(t *testing.T) {
	t.Parallel()
	t.Run("no key at all", func(t *testing.T) {
		t.Parallel()
		assertMountPanic(t, `tagBareKey.Open has via:"true": the only key is init=<json>`,
			func() { via.Handler(tagBareKey{}) })
	})
	t.Run("a key via does not know", func(t *testing.T) {
		t.Parallel()
		assertMountPanic(t, `tagUnknownKey.Count has via:"seed=1": the only key is init=<json>`,
			func() { via.Handler(tagUnknownKey{}) })
	})
	t.Run("the key with no value", func(t *testing.T) {
		t.Parallel()
		assertMountPanic(t, `tagKeyNoValue.Count has via:"init": the only key is init=<json>`,
			func() { via.Handler(tagKeyNoValue{}) })
	})
}

type tagOnPlainField struct {
	Name string `via:"init=\"x\""`
}

func (p *tagOnPlainField) View() h.H { return h.Div() }

type tagOnNestedField struct {
	Chat struct{ X int } `via:"init=1"`
}

func (p *tagOnNestedField) View() h.H { return h.Div() }

func TestMount_panicsOnASeedTagOnAPlainField(t *testing.T) {
	t.Parallel()
	want := ` has a via:"…" tag, but only a Signal, SignalCS, State or List field reads it`
	t.Run("plain field", func(t *testing.T) {
		t.Parallel()
		assertMountPanic(t, "tagOnPlainField.Name"+want, func() { via.Handler(tagOnPlainField{}) })
	})
	t.Run("nested struct field", func(t *testing.T) {
		t.Parallel()
		assertMountPanic(t, "tagOnNestedField.Chat"+want, func() { via.Handler(tagOnNestedField{}) })
	})
}

// boxedSignal reaches its signal through a pointer field, so the handle lives
// outside the composition struct and has no field offset to name itself by.
type boxedSignal struct{ S *via.Signal[string] }

func (b *boxedSignal) View() h.H { return h.Div(h.Input(b.S.Bind())) }

// valueReceiverView binds a stack copy: every signal offsets from the wrong
// base, so its writes land on a struct the render throws away.
type valueReceiverView struct{ S via.Signal[int] }

type slicedSignal struct{ S []via.Signal[string] }

func (b *slicedSignal) View() h.H { return h.Div() }

type mappedSignal struct{ S map[string]via.Signal[string] }

func (b *mappedSignal) View() h.H { return h.Div() }

type valueReceiverChildParent struct{ C valueReceiverView }

func (p *valueReceiverChildParent) View() h.H { return h.Div(via.Child(p.C)) }

func (v valueReceiverView) View() h.H { return h.Div(v.S.Display()) }

func TestSignal_behindAPointerFieldPanicsAtMount(t *testing.T) {
	t.Parallel()
	assertMountPanic(t, "holds a via.Signal behind a ptr", func() {
		via.Handler(boxedSignal{S: &via.Signal[string]{}})
	})
}

func TestSignal_behindASliceFieldPanicsAtMount(t *testing.T) {
	t.Parallel()
	assertMountPanic(t, "holds a via.Signal behind a slice", func() { via.Handler(slicedSignal{}) })
}

func TestSignal_behindAMapFieldPanicsAtMount(t *testing.T) {
	t.Parallel()
	assertMountPanic(t, "holds a via.Signal behind a map", func() { via.Handler(mappedSignal{}) })
}

func TestSignal_valueReceiverViewPanicsAtMount(t *testing.T) {
	t.Parallel()
	assertMountPanic(t, "View has a VALUE receiver", func() { via.Handler(valueReceiverView{}) })
}

func TestSignal_valueReceiverChildPanicsAtMount(t *testing.T) {
	t.Parallel()
	assertSlotPanic(t, "View has a VALUE receiver", func() { via.Handler(valueReceiverChildParent{}) })
}

func TestSignal_slotCollidingWithAChildPrefixPanics(t *testing.T) {
	t.Parallel()
	assertSlotPanic(t, "collides with the child prefix of field a", func() { via.Handler(childCollidePage{}) })
}

type phantomChild struct {
	Load via.Signal[[]int]  `via:"init=[]"`
	Open via.SignalCS[bool] `via:"init=true"`
}

func (c *phantomChild) View() h.H { return h.Div() }

type phantomRoot struct{ Sub phantomChild }

func (p *phantomRoot) View() h.H { return h.Div(via.Child(p.Sub)) }

func TestMount_mintsNoSlotForAChildCompositionsSignals(t *testing.T) {
	t.Parallel()
	_, body := vt.Serve(t, via.Handler(phantomRoot{})).Get("/")

	assert.NotContains(t, body, `"sub_load"`)
	assert.NotContains(t, body, `"_sub_open"`)
	assert.Contains(t, body, `"sub__load":[]`)
	assert.Contains(t, body, `"_sub__open":true`)
}

type tagChildSub struct{ N via.Signal[int] }

func (s *tagChildSub) View() h.H { return h.Div() }

type tagOnChildComposition struct {
	Sub tagChildSub `via:"init=1"`
}

func (p *tagOnChildComposition) View() h.H { return h.Div(via.Child(p.Sub)) }

func TestMount_panicsOnASeedTagOnAChildComposition(t *testing.T) {
	t.Parallel()
	assertMountPanic(t, `tagOnChildComposition.Sub has a via:"…" tag, but only a Signal, SignalCS, State or List field reads it`,
		func() { via.Handler(tagOnChildComposition{}) })
}

type bareChildSub struct{ Name via.Signal[string] }

func (s *bareChildSub) View() h.H { return h.Div(h.Input(s.Name.Bind())) }

type bareChildParent struct{ Sub bareChildSub }

func (p *bareChildParent) View() h.H { return h.Div(p.Sub.View()) }

func TestChild_renderedWithoutChildPanicsNamingViaChild(t *testing.T) {
	t.Parallel()
	assertSlotPanic(t, "a child composition must be rendered through via.Child, not by calling its View",
		func() { via.Handler(bareChildParent{}) })
}
