package via_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The binder plumbing (signal slots, action ids, the renderer itself) is
// deliberately off the h package's public surface: h is elements + attrs +
// Str only, the via core reaches the plumbing through internal/hcore. If this
// fails, plumbing leaked back onto the package every user of h sees.
func TestCtx_doesNotExposeBinderPlumbing(t *testing.T) {
	t.Parallel()
	banned := map[string]bool{
		"Dyn": true, "DynAttr": true, "NewRenderer": true, "Renderer": true, "Binder": true,
	}
	fset := token.NewFileSet()
	entries, err := os.ReadDir("h")
	require.NoError(t, err)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join("h", e.Name()), nil, 0)
		require.NoError(t, err)
		for _, decl := range f.Decls {
			var name string
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					name = d.Name.Name
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok {
						name = ts.Name.Name
					}
				}
			}
			if name != "" && banned[name] {
				t.Fatalf("h package must not export %q — it belongs to internal/hcore", name)
			}
		}
	}
}

// store is the in-server state the counter tracks — a plain app dependency.
type store struct {
	mu sync.Mutex
	n  int
}

func (s *store) Value() int { s.mu.Lock(); defer s.mu.Unlock(); return s.n }
func (s *store) Add(d int)  { s.mu.Lock(); s.n += d; s.mu.Unlock() }

// counter is the slice-1 component under test, exercised through the public via
// API. It mirrors example/counter: server-authoritative state in an injected
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
	srv := httptest.NewServer(via.Register(counter{count: &store{}}))
	t.Cleanup(srv.Close)
	return srv
}

// do issues method+path (with optional body) against srv and returns the
// response and body string.
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

// serve mounts a handler behind one httptest server, registered for cleanup.
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

// actionURL extracts the n-th action URL embed rendered, in document order,
// out of html. The wire id is a hash of the handler's func name (and any ?a=
// row datum rides with it), so a test posts the URL the page actually
// shipped rather than a hand-built path.
func actionURL(t *testing.T, html string, embed string, n int) string {
	t.Helper()
	pat := `(?:@post\('|action=")([^'"]*_via/a/` + embed + `/[A-Za-z0-9_-]+(?:[?&][^'"]*)?)['"]`
	m := regexp.MustCompile(pat).FindAllStringSubmatch(html, -1)
	require.Greaterf(t, len(m), n, "action %s/%d not found on rendered page:\n%s", embed, n, html)
	return m[n][1]
}

// The GET page must ship the server-rendered skeleton — the current value baked
// into HTML, both wired buttons, the morph-target #root, and the client script.
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
		`src="/_via/datastar.js">`,                                  // module script tag (external, admitted by 'self')
	} {
		assert.Contains(t, body, want, "page missing skeleton fragment")
	}
}

// Event bindings must use Datastar v1's colon key syntax (data-on:click). The
// old dash form (data-on-click) is parsed by v1.0.2 as a nonexistent plugin
// "on-click" and silently dropped — the button renders, no listener attaches,
// and the click is dead in the browser while every server-side test still
// passes. Assert the dash form never ships so that regression can't reappear.
func TestEventBinding_usesDatastarColonSyntaxNotDeadDashForm(t *testing.T) {
	t.Parallel()
	_, body := do(t, newCounter(t), http.MethodGet, "/", "")

	assert.Contains(t, body, `data-on:click="@post(`, "page is missing the v1 colon event binding data-on:click")
	assert.NotContains(t, body, "data-on-click", "page ships the dead v0.x dash form data-on-click (silently ignored by Datastar v1.0.2)")
}

// An action must mutate the server-side dependency and return the re-rendered
// fragment as a text/html element-patch reflecting the NEW value — and the state
// must persist across requests (it lives in the store, not the request).
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

// An action id with no registered handler must be rejected with 410 Gone, so
// a stale client learns the action is gone rather than silently no-op.
func TestUnknownAction_isGone(t *testing.T) {
	t.Parallel()
	srv := newCounter(t)
	_, page := do(t, srv, http.MethodGet, "/", "")
	url := swapActionID(t, actionURL(t, page, "r", 0), "zzzzzzzz")

	resp, _ := do(t, srv, http.MethodPost, url, "{}")
	assert.Equal(t, http.StatusGone, resp.StatusCode, "want 410 Gone")
}

// Go 1.27's default jsonv2 backing must still decode a signal body the way v1
// did (replace invalid UTF-8 with U+FFFD) rather than reject it, or the module
// silently starts 400ing bodies real browsers happily sent under v1.
func TestAction_invalidUTF8InSignalBodyIsNotA400(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(boundForm{}))
	status, _ := app.Action(0).Body("{\"name\":\"\xff\"}").Fire()
	assert.Equal(t, http.StatusOK, status)
}

// A duplicate key in a signal body must decode with v1's last-write-wins
// semantics, not jsonv2's stricter default, or the same request a v1 client
// sent starts failing under the new decoder.
func TestAction_duplicateSignalKeyLastWins(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(boundForm{}))
	status, frag := app.Action(0).Body(`{"name":"one","name":"two"}`).Fire()
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, frag, "two")
	assert.NotContains(t, frag, "one")
}

// The vendored Datastar client must be served from the embedded asset with a JS
// content-type, or the module script tag on the page 404s and nothing hydrates.
func TestEmbeddedDatastarClient_isServedAsJS(t *testing.T) {
	t.Parallel()
	resp, body := do(t, newCounter(t), http.MethodGet, "/_via/datastar.js", "")

	require.Equal(t, http.StatusOK, resp.StatusCode, "want 200")
	ct := resp.Header.Get("Content-Type")
	assert.True(t, strings.HasPrefix(ct, "text/javascript"), "Content-Type = %q, want text/javascript", ct)
	assert.NotEmpty(t, body, "served datastar.js was empty")
}

// Slice 1's action response is a full element-patch (text/html morphed into
// #root); it must never emit an application/json signal-patch. The dead
// dirty/markDirty signal-patch leg was deleted, and this guard stops a second,
// untested wire contract from silently returning.
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

// An action that leaves the rendered View identical must return 204 No Content,
// not re-send an identical #root the browser would morph onto itself. The
// runtime infers this by comparing the pre- and post-action renders — no author
// annotation (no NoContent call) required.
func TestAction_returns204WhenViewIsUnchanged(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(noopComp{}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	resp, body := post(t, srv, actionURL(t, page, "r", 0), "{}", sameOrigin())
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	assert.Empty(t, body)
}

// formComp uses On("submit", ...) on a form.
type formComp struct{ q via.Signal[string] }

func (c *formComp) Go(ctx *via.Ctx) {}
func (c *formComp) View() h.H {
	return h.Form(via.On("submit", c.Go), h.Input(c.q.Bind()))
}

// On("submit", ...) wires a form submit to a POST action with Datastar's colon event
// syntax. Datastar auto-prevents a form's default submit, so no modifier is
// needed.
func TestOn_submitWiresAPostAction(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(formComp{})), http.MethodGet, "/", "")
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

// An action must be able to read the HTTP request that triggered it — auth
// headers, cookies, client info — through ctx.Request(); without it there is no
// way to do request-native wiring from a handler. The value the action pulls out
// of the request must reach the re-rendered response.
func TestAction_canReadTheTriggeringRequest(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(reqEchoer{}))
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

// TestAction_digestPlaceholderCannotBeForgedByUserText posts hostile text
// containing the literal NUL-delimited digest placeholder via.digestPlaceholder
// mints internally (NUL, "vD0", NUL — token 0, this component's only
// action). If writeEscaped ever stopped neutralising NUL, the final
// bytes.ReplaceAll pass that splices the real shape digest into the response
// would match this text too and corrupt it with digest bytes.
func TestAction_digestPlaceholderCannotBeForgedByUserText(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(digestEchoer{}))
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

// viaCallNames are the via entry points whose arguments must be named method
// values or by-value compositions — never an address-of or a closure. Mount
// (a *Router method, called as r.Mount) and Param (a *Ctx method, called as
// ctx.Param) cannot appear here: isViaCall only matches a package-qualified
// call (via.X), and neither is ever spelled that way.
var viaCallNames = map[string]bool{
	"Register": true, "Embed": true, "When": true, "Each": true,
	"On": true, "OnArg": true, "PostForm": true,
}

// The framework's headline promise is that user code never writes '&' and never
// passes a closure at a via call site (Register/Embed/On*). A violation that
// compiles silently erodes the design, so this asserts it structurally over the
// example sources — the canonical user-facing call sites. It is an interim
// guard; the type-level closure ban is tracked as follow-up.
func TestExamples_takeNoAddressOfOrClosureAtViaCallSites(t *testing.T) {
	t.Parallel()
	files := exampleGoFiles(t)
	require.NotEmpty(t, files, "expected example sources to lint")

	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			t.Parallel()
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, file, nil, 0)
			require.NoError(t, err)

			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !isViaCall(call) {
					return true
				}
				for _, arg := range call.Args {
					switch a := arg.(type) {
					case *ast.FuncLit:
						assert.Failf(t, "closure at via call site",
							"%s: pass a named method value, not a func literal", fset.Position(a.Pos()))
					case *ast.UnaryExpr:
						if a.Op == token.AND {
							assert.Failf(t, "address-of at via call site",
								"%s: via takes compositions by value — drop the '&'", fset.Position(a.Pos()))
						}
					case *ast.CompositeLit:
						// via.Embed(p.Chat) embeds the parent's field; a literal
						// would re-seed a fresh child on every render.
						if isViaCallNamed(call, "Embed") {
							assert.Failf(t, "composite literal at via.Embed call site",
								"%s: pass the parent's field (p.Chat), not a literal", fset.Position(a.Pos()))
						}
					}
				}
				return true
			})
		})
	}
}

func isViaCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "via" && viaCallNames[sel.Sel.Name]
}

// isViaCallNamed reports whether call is via.<name>(...).
func isViaCallNamed(call *ast.CallExpr, name string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "via" && sel.Sel.Name == name
}

// via's headline guarantee is reflection-free wiring: the composition is bound
// by generics + interface assertions + handle identity, never by reflecting
// over its fields, method names, or struct tags (which is what the old
// reflect-based framework did). This locks that — only via.go may import
// reflect, and only to read a handler func value's own code pointer for
// actionID (a func's identity, not a struct's shape). (Signal values decode
// through encoding/json, which reflects internally; that is data decoding, not
// wiring, and is out of this guard.)
func TestCore_importsNoReflectPackage(t *testing.T) {
	t.Parallel()
	// reflect is admitted in exactly three files and only on TYPE-setup paths
	// that run once per composition type (Mount/Embed) and are memoized: the
	// action-id func name, the field-name signal table, and the embed's parent
	// field lookup. Nothing here may run per render — that is the invariant
	// this whitelist exists to keep honest.
	allowed := map[string][]string{
		"via.go":    {"reflect.PointerTo", "reflect.Struct", "reflect.Type", "reflect.TypeOf", "reflect.ValueOf"},
		"embed.go":  {"reflect.TypeOf"},
		"router.go": {"reflect.TypeOf"},
	}
	files := coreGoFiles(t)
	require.NotEmpty(t, files, "expected core sources to scan")
	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			t.Parallel()
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, file, nil, parser.ImportsOnly)
			require.NoError(t, err)
			for _, imp := range f.Imports {
				if imp.Path.Value != `"reflect"` {
					continue
				}
				want, ok := allowed[filepath.Base(file)]
				require.True(t, ok, "%s imports reflect — via wiring must be reflection-free", file)
				src, err := os.ReadFile(file)
				require.NoError(t, err)
				uses := slices.Compact(slices.Sorted(slices.Values(
					regexp.MustCompile(`reflect\.\w+`).FindAllString(string(src), -1))))
				assert.Equal(t, want, uses, "%s may only reflect on the memoized type-setup path", file)
			}
		})
	}
}

// coreGoFiles lists the non-test Go sources of the core packages (the root via
// package and the h DSL), excluding examples.
func coreGoFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	for _, dir := range []string{".", "h", "topic"} {
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		for _, e := range entries {
			n := e.Name()
			if !e.IsDir() && strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
				files = append(files, filepath.Join(dir, n))
			}
		}
	}
	return files
}

func exampleGoFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir("example", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			files = append(files, p)
		}
		return nil
	})
	require.NoError(t, err)
	return files
}

// --- value-carrying actions (OnArg) ---

type todoItem struct {
	ID   int
	Text string
}

type todoBox struct{ items []todoItem }

func (b *todoBox) remove(id int) {
	out := b.items[:0]
	for _, it := range b.items {
		if it.ID != id {
			out = append(out, it)
		}
	}
	b.items = out
}

// todoList is a plain list whose rows each carry a delete action bound to the
// row's id — the per-row-action case. Del receives the id as a typed parameter.
type todoList struct{ box *todoBox }

func (l *todoList) Del(ctx *via.Ctx, id int) { l.box.remove(id) }
func (l *todoList) row(t todoItem) h.H {
	return h.Li(h.Str(t.Text), h.Button(via.OnArg("click", l.Del, t.ID), h.Str("x")))
}
func (l *todoList) View() h.H { return h.Ul(via.Each(l.box.items, l.row)) }

func newTodoList() *todoBox {
	return &todoBox{items: []todoItem{{1, "alpha"}, {2, "bravo"}, {3, "gamma"}}}
}

// A row's action binding must carry the row's own value, so the click self-
// describes which datum it acts on — no closure, no stable-slot scheme.
func TestActionArg_buttonCarriesTheRowValue(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(todoList{box: newTodoList()})), http.MethodGet, "/", "")
	assert.Regexp(t, `@post\('/_via/a/r/[A-Za-z0-9_-]+\?a=2'`, body, "the bravo row's button must carry its id (2) as the action arg")
}

// The handler must receive the carried value as a typed parameter and act on it:
// deleting the row whose value rode with the click.
func TestActionArg_handlerReceivesTheTypedValue(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(todoList{box: newTodoList()}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	resp, body := do(t, srv, http.MethodPost, actionURL(t, page, "r", 1), "{}")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotContains(t, body, "bravo", "the row whose value was sent must be deleted")
	assert.Contains(t, body, "alpha")
	assert.Contains(t, body, "gamma")
}

// The VALUE, not the action slot, identifies the row: posting to a different
// row's slot but with bravo's value still deletes bravo — so a renumbered list
// can't misroute, because identity rides with the click.
func TestActionArg_valueNotSlotIdentifiesTheRow(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(todoList{box: newTodoList()}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	// slot 0 is alpha's own action (its rendered arg is ?a=1); swap in bravo's
	// value (2) while keeping alpha's slot and shape digest.
	url := strings.Replace(actionURL(t, page, "r", 0), "a=1", "a=2", 1)
	resp, body := do(t, srv, http.MethodPost, url, "{}") // slot 0 (alpha), but arg=2 (bravo)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotContains(t, body, "bravo", "the carried value (2) must win over the slot (0)")
	assert.Contains(t, body, "alpha")
}

// A malformed ?a= (here, a string where the handler wants an int) must not
// silently hand the handler a zero value it might act on (e.g. deleting row
// 0) — the arg is client-controlled input, so an honest answer is 400.
func TestActionArg_malformedArgAnswers400(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(todoList{box: newTodoList()}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	url := strings.Replace(actionURL(t, page, "r", 0), "a=1", "a=%22abc%22", 1)
	resp, body := do(t, srv, http.MethodPost, url, "{}")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.NotContains(t, body, "alpha", "the row must not be rendered as deleted by a malformed arg")
}

// A missing ?a= value (empty, or the literal "null") must answer 400, not
// silently hand the handler the zero value — the "delete row 0" case this
// guards against reads identically to a malformed arg to a client.
func TestActionArg_missingArgAnswers400(t *testing.T) {
	tests := []struct {
		name string
		a    string
	}{
		{"empty", "a="},
		{"null", "a=null"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := serve(t, via.Register(todoList{box: newTodoList()}))
			_, page := do(t, srv, http.MethodGet, "/", "")
			url := strings.Replace(actionURL(t, page, "r", 0), "a=1", tt.a, 1)
			resp, body := do(t, srv, http.MethodPost, url, "{}")
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
			assert.NotContains(t, body, "alpha", "the row must not be rendered as deleted by a missing arg")
		})
	}
}

// todoBoard embeds the todo list as a PLAIN embed — per-row value-actions
// must work there too, not only at the root.
type todoBoard struct{ List todoList }

func (b *todoBoard) View() h.H { return h.Div(via.Embed(b.List)) }

// A value-carrying action inside a plain embed must still deliver
// its value: the embed action path has to expose the request to the slot.
func TestActionArg_worksInsideAPlainEmbed(t *testing.T) {
	t.Parallel()
	board := todoBoard{List: todoList{box: newTodoList()}}
	srv := serve(t, via.Register(board))
	_, page := do(t, srv, http.MethodGet, "/", "")

	resp, body := do(t, srv, http.MethodPost, actionURL(t, page, "0", 1), "{}") // embed 1, bravo's slot, arg=2
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotContains(t, body, "bravo", "the embed row's value-action did not fire")
	assert.Contains(t, body, "alpha")
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

// On("change", ...) must render its event binding and route the commit back into the
// handler. Fails if the change event stops firing or stops reaching Pick.
func TestOn_changeFiresHandlerOnCommit(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(changePicker{}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	assert.Contains(t, page, `data-on:change`, "OnChange must bind the change event")

	_, body := do(t, srv, http.MethodPost, actionURL(t, page, "r", 0), "{}")
	assert.Contains(t, body, "picked", "OnChange's handler did not run")
}

// tickSessionWriter starts a Tick in OnInit that writes to the session on
// every beat — the pattern I2 covers: a Tick/Listen handler's Ctx is the SAME
// Ctx OnInit held, so its sessW was set (for OnInit's own use) and, if
// never cleared, silently survives long past the point the response it
// pointed at was flushed.
type tickSessionWriter struct{ n via.State[int] }

func (t *tickSessionWriter) OnInit(ctx *via.Ctx) error {
	ctx.Tick(10*time.Millisecond, t.beat)
	return nil
}
func (t *tickSessionWriter) beat(ctx *via.Ctx) {
	t.n.Set(t.n.Get() + 1)
	ctx.Session().Put(member{Name: "orphan"})
}
func (t *tickSessionWriter) View() h.H { return h.Div(t.n.Display()) }

// I2: a session minted from a Tick handler has no open response to carry a
// cookie — this must produce the documented warning (loud, not silent), and
// the connect response (long since flushed by the time any tick fires) must
// never carry a stray Set-Cookie for it.
//
// Sequential: it captures the global log output.
func TestConnectUnit_tickSessionWriteWarnsInsteadOfWritingADeadResponse(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	synctest.Test(t, func(t *testing.T) {
		srv := liveServer(t, via.Register(tickSessionWriter{}, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
		lines, cancel := openStream(t, srv)
		defer cancel()
		_ = awaitTabID(t, lines)
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()
	})

	assert.Contains(t, buf.String(), "no cookie can be set",
		"a Tick-minted session must warn instead of silently orphaning")
}

// TestActionID_listMutationByAnotherTabDoesNotBreakOpenTabs is the regression
// guard for the bug content-addressed action ids replace: two tabs share one
// store; tab A deletes a row, changing the action COUNT for everyone. Under
// the old positional/shape-digest wire, every URL tab B was still holding
// (its untouched rows included) 410'd as "stale page" — silently, permanently,
// until a reload. Addressed by handler + ?a=, tab B's shipped URLs keep working.
func TestActionID_listMutationByAnotherTabDoesNotBreakOpenTabs(t *testing.T) {
	t.Parallel()
	box := newTodoList()
	srv := serve(t, via.Register(todoList{box: box}))

	_, tabB := do(t, srv, http.MethodGet, "/", "") // tab B paints, then sits idle
	bravoFromB := rowActionURL(t, tabB, 2)

	_, tabA := do(t, srv, http.MethodGet, "/", "")
	resp, _ := do(t, srv, http.MethodPost, rowActionURL(t, tabA, 1), "{}") // tab A deletes alpha
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp, body := do(t, srv, http.MethodPost, bravoFromB, "{}")
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"tab B's already-rendered row action must survive another tab resizing the list")
	assert.NotContains(t, body, "bravo", "and it must have deleted bravo — the row it named")
	assert.Contains(t, body, "gamma", "not some other row")
}

// rowActionURL picks the action URL carrying ?a={id} out of a rendered list.
func rowActionURL(t *testing.T, html string, id int) string {
	t.Helper()
	m := regexp.MustCompile(`@post\('([^']*_via/a/r/[A-Za-z0-9_-]+\?a=` + strconv.Itoa(id) + `(?:&[^']*)?)'`).FindStringSubmatch(html)
	require.NotEmptyf(t, m, "no row action for id %d in:\n%s", id, html)
	return m[1]
}

// An action id is derived from the handler's own Go func name, so it is the
// same across renders AND across instances — a deploy or a second server does
// not invalidate the URLs open tabs are holding.
func TestActionID_isStableAcrossRendersAndInstances(t *testing.T) {
	t.Parallel()
	_, first := do(t, serve(t, via.Register(counter{count: &store{}})), http.MethodGet, "/", "")
	_, second := do(t, serve(t, via.Register(counter{count: &store{}})), http.MethodGet, "/", "")
	assert.Equal(t, actionURL(t, first, "r", 1), actionURL(t, second, "r", 1),
		"two independent instances must address the same handler identically")
}

// twinButtons binds the SAME handler twice. Both buttons mean the same thing,
// so they collapse onto one action entry and one URL — identity is the
// handler, never the render position.
type twinButtons struct{ count *store }

func (c *twinButtons) Inc(ctx *via.Ctx) { c.count.Add(1) }
func (c *twinButtons) View() h.H {
	return h.Div(
		h.H1(h.Str(strconv.Itoa(c.count.Value()))),
		h.Button(via.On("click", c.Inc)),
		h.Button(via.On("click", c.Inc)),
	)
}

func TestActionID_sameHandlerTwiceCollapsesToOneEntry(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(twinButtons{count: &store{}}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	assert.Equal(t, actionURL(t, page, "r", 0), actionURL(t, page, "r", 1),
		"two bindings of one handler must share one id")

	resp, body := do(t, srv, http.MethodPost, actionURL(t, page, "r", 1), "{}")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, "<h1>1</h1>", "and it must dispatch to that handler")
}

// The 410 for an id this render does not bind keeps the diagnosis (which
// handlers ARE bound, and the Go method behind each) server-side: the body
// names only the id the client asked for, so the render's Go type and method
// names never reach it.
//
// Sequential: it captures the global log output.
func TestUnknownAction_410NamesOnlyTheAskedForIDAndLogsTheBoundHandlers(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	srv := newCounter(t)
	_, page := do(t, srv, http.MethodGet, "/", "")
	resp, body := do(t, srv, http.MethodPost, swapActionID(t, actionURL(t, page, "r", 0), "zzzzzzzz"), "{}")
	require.Equal(t, http.StatusGone, resp.StatusCode)
	assert.Contains(t, body, "zzzzzzzz", "the 410 must name the id that was asked for")
	assert.NotContains(t, body, ").Inc", "but never the Go method names of the render")
	assert.Contains(t, buf.String(), ").Inc", "the bound handlers go to the server log instead")
}

// --- action id identity (two instances of one type) ---

type idCounter struct {
	N via.Signal[int]
}

func (c *idCounter) Inc(ctx *via.Ctx) { c.N.Set(c.N.Get() + 1) }

func (c *idCounter) View() h.H {
	return h.Div(h.Button(via.On("click", c.Inc), h.Str("+")), c.N.Display())
}

type idPair struct{ A, B idCounter }

func (p *idPair) View() h.H { return h.Div(via.Embed(p.A), via.Embed(p.B)) }

// idTwins holds two instances of one type as PLAIN fields (no Embed), so both
// bind into the same action table. runtime.FuncForPC drops the receiver, so
// without the offset in the id both buttons would render the SAME action URL
// and A's click would run B's handler.
type idTwins struct{ A, B idCounter }

func (p *idTwins) View() h.H { return h.Div(p.A.View(), p.B.View()) }

var actionURLRe = regexp.MustCompile(`@post\('([^']+)'`)

func actionURLs(t *testing.T, h http.Handler) []string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var out []string
	for _, m := range actionURLRe.FindAllStringSubmatch(rec.Body.String(), -1) {
		out = append(out, m[1])
	}
	return out
}

func TestActionID_twoInstancesOfOneTypeGetDistinctIDs(t *testing.T) {
	t.Parallel()
	urls := actionURLs(t, via.Register(idTwins{}))
	require.Len(t, urls, 2)
	require.NotEqual(t, urls[0], urls[1],
		"two instances of one type must not share an action id — A's click would run B")
}

func TestActionID_embeddedSiblingsGetDistinctIDs(t *testing.T) {
	t.Parallel()
	urls := actionURLs(t, via.Register(idPair{}))
	require.Len(t, urls, 2)
	require.NotEqual(t, urls[0], urls[1])
}

// The id must address the receiver, not the render position: clicking the
// SECOND twin's button must increment the second counter, not the first.
func TestActionID_postRoutesToItsOwnReceiver(t *testing.T) {
	t.Parallel()
	app := via.Register(idTwins{})
	urls := actionURLs(t, app)
	require.Len(t, urls, 2)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, urls[1], strings.NewReader(`{}`))
	req.Header.Set("Datastar-Request", "true")
	app.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	// Two signals on the page; the second twin's is the one that moved.
	slots := regexp.MustCompile(`data-text="\$([a-z0-9_]+)"`).FindAllStringSubmatch(rec.Body.String(), -1)
	require.Len(t, slots, 2)
	require.Contains(t, rec.Body.String(), `"`+slots[1][1]+`":1`, "body: %s", rec.Body.String())
}

type idSameMethodTwice struct{ N via.Signal[int] }

func (c *idSameMethodTwice) Inc(ctx *via.Ctx) { c.N.Set(c.N.Get() + 1) }

func (c *idSameMethodTwice) View() h.H {
	return h.Div(
		h.Button(via.On("click", c.Inc), h.Str("+")),
		h.Button(via.On("click", c.Inc), h.Str("also +")),
	)
}

// The flip side of the guard: the SAME method on the SAME receiver, bound
// twice, is one action and must still collapse onto one id.
func TestActionID_sameHandlerTwiceCollapses(t *testing.T) {
	t.Parallel()
	urls := actionURLs(t, via.Register(idSameMethodTwice{}))
	require.Len(t, urls, 2)
	require.Equal(t, urls[0], urls[1])
}

type idClosurePair struct{ hits [2]int }

func (p *idClosurePair) View() h.H {
	var kids []h.H
	for i := range p.hits {
		kids = append(kids, h.Button(via.On("click", func(ctx *via.Ctx) { p.hits[i]++ })))
	}
	return h.Div(kids...)
}

// Two distinct closures share a Go name ("…View.func1"), so via cannot tell
// them apart by identity. That must be loud, never a silent last-wins.
// Sequential: it captures the global log output.
func TestActionID_indistinguishableHandlersPanic(t *testing.T) {
	app := via.Register(idClosurePair{})
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Contains(t, logs.String(), "share the action id")
}

// --- action id memoization (change 1) ---

// gridBench binds ONE handler a thousand times, which is the shape actionID's
// cost shows up in: the id is a pure function of (code pointer, receiver
// offset), so resolving the Go name and hashing it per binding per render was
// pure waste.
type gridBench struct{ rows []int }

func (g *gridBench) Hit(ctx *via.Ctx) {}

func (g *gridBench) row(int) h.H { return h.Button(via.On("click", g.Hit)) }

func (g *gridBench) View() h.H { return h.Div(via.Each(g.rows, g.row)) }

func BenchmarkRender_thousandActionBindings(b *testing.B) {
	handler := via.Register(gridBench{rows: make([]int, 1000)})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for b.Loop() {
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}
}

// The memoized id must be stable across renders AND across separately mounted
// instances of the same type — the cache is keyed on the code pointer plus the
// receiver's offset, so a key that dropped either half would either churn URLs
// between renders or collapse two receivers onto one id.
func TestActionID_memoIsStableAcrossRenders(t *testing.T) {
	t.Parallel()
	handler := via.Register(idTwins{})
	first := actionURLs(t, handler)
	second := actionURLs(t, handler)
	require.Len(t, first, 2)
	assert.Equal(t, first, second, "an action URL must survive a re-render, or every open tab 410s")
	assert.Equal(t, first, actionURLs(t, via.Register(idTwins{})),
		"the id is content-addressed, not per-instance")
}

// --- regression: a slot name that is not one field's unique identity ---

// collidePage mints "a_b" twice: once for the nested A.B (nested struct names
// join with "_") and once for the sibling field A_b. Two spans bind one slot,
// one slot is declared, and the hydrator map keeps whichever came last — so a
// POST writes the WRONG field, silently.
type collidePage struct {
	A   struct{ B via.Signal[int] }
	A_b via.Signal[int]
}

func (p *collidePage) View() h.H { return h.Div(p.A.B.Bind(), p.A_b.Bind()) }

// embedCollidePage's field A__b mints "a__b" in the PARENT, which is exactly
// the slot the EMBED of field A gives its own signal B ("a__" + "b").
type embedKidB struct{ B via.Signal[int] }

func (k *embedKidB) View() h.H { return k.B.Bind() }

type embedCollidePage struct {
	A    embedKidB
	A__b via.Signal[int]
}

func (p *embedCollidePage) View() h.H { return h.Div(via.Embed(p.A), p.A__b.Bind()) }

func assertSlotPanic(t *testing.T, app http.Handler, want string) {
	t.Helper()
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, logs.String(), want)
}

func TestSignal_duplicateSlotNamePanics(t *testing.T) {
	assertSlotPanic(t, via.Register(collidePage{}), "signal slot a_b is minted twice")
}

// boxedSignal reaches its signal through a pointer field, so the handle lives
// outside the composition struct and has no field offset to name itself by.
type boxedSignal struct{ S *via.Signal[string] }

func (b *boxedSignal) View() h.H { return h.Div(h.Input(b.S.Bind())) }

// valueReceiverView binds a STACK COPY: every signal offsets from the wrong
// base, so its writes land on a struct the render throws away.
type valueReceiverView struct{ S via.Signal[int] }

func (v valueReceiverView) View() h.H { return h.Div(v.S.Display()) }

func TestSignal_behindAPointerFieldPanics(t *testing.T) {
	assertSlotPanic(t, via.Register(boxedSignal{S: &via.Signal[string]{}}), "not a plain field of its composition")
}

func TestSignal_valueReceiverViewPanics(t *testing.T) {
	assertSlotPanic(t, via.Register(valueReceiverView{}), "not a plain field of its composition")
}

func TestSignal_slotCollidingWithAnEmbedPrefixPanics(t *testing.T) {
	assertSlotPanic(t, via.Register(embedCollidePage{}), "collides with the embed prefix of field a")
}

// ownedRows is the arg-authorization fixture: a list filtered by owner, plus
// an admin-only row reachable only through a via.When. Two users see two
// disjoint sets of ?a= values from ONE handler and one action id — which is
// the whole point: the action id is content-addressed on the handler, so the
// arg is the only thing separating alice's row from bob's.
type ownedRows struct {
	me    string
	admin bool
	rows  map[int]string // id -> owner
	gone  []int
}

func newOwnedRows(me string, admin bool) ownedRows {
	return ownedRows{me: me, admin: admin, rows: map[int]string{1: "alice", 2: "bob", 9: "system"}}
}

func (o *ownedRows) Delete(ctx *via.Ctx, id int) { o.gone = append(o.gone, id) }

func (o *ownedRows) mine() []int {
	var out []int
	for id, owner := range o.rows {
		if owner == o.me {
			out = append(out, id)
		}
	}
	sort.Ints(out)
	return out
}

func (o *ownedRows) systemRow() h.H {
	return h.Li(h.ID("system"), h.Button(via.OnArg("click", o.Delete, 9), h.Str("drop system")))
}

func (o *ownedRows) row(id int) h.H {
	return h.Li(h.ID("r"+strconv.Itoa(id)), h.Str(strconv.Itoa(id)),
		h.Button(via.OnArg("click", o.Delete, id), h.Str("delete")))
}

func (o *ownedRows) View() h.H {
	return h.Div(
		h.P(h.Str("deleted: "+fmt.Sprint(o.gone))),
		h.Ul(via.Each(o.mine(), o.row)),
		via.When(o.admin, o.systemRow),
	)
}

// The blocker this file's arg checks exist for: bob's render binds Delete for
// his own row (?a=2), so the handler id is dispatchable by him — but swapping
// the arg to alice's row (?a=1) over his own valid session must 410, not
// delete her row. Before the (handler, arg) pair became the dispatch identity
// this answered 200 and ran Delete(1).
func TestActionArg_swappingInAnotherUsersArgIs410(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(newOwnedRows("bob", false)))
	_, page := do(t, srv, http.MethodGet, "/", "")
	require.Contains(t, page, "a=2", "bob's own row must be bound")
	require.NotContains(t, page, "a=1", "alice's row must not be rendered for bob")

	url := strings.Replace(actionURL(t, page, "r", 0), "a=2", "a=1", 1)
	resp, body := do(t, srv, http.MethodPost, url, "{}")
	assert.Equal(t, http.StatusGone, resp.StatusCode, "an arg bob's render never bound must not dispatch")
	assert.NotContains(t, body, "deleted: [1]", "alice's row was deleted by an arg swap")
}

// The same swap over a LIVE connection — bob's own tab, his own session, the
// handler bound by his own render. The live path resolves the action against
// the last push's table rather than a fresh render, so it needs its own test.
func TestLiveActionArg_swappingInAnotherUsersArgIs410(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Register(liveOwnedRows{}))
		conn := app.Connect()

		url := strings.Replace(conn.ActionURL("r", 0), "a=2", "a=1", 1)
		status, _ := app.Action(0).Raw(url).Over(conn).Fire()
		assert.Equal(t, http.StatusGone, status, "an unrendered arg must not dispatch over a live stream")

		status, _ = app.Action(0).Over(conn).Fire() // the rendered arg, same slot
		assert.Equal(t, http.StatusNoContent, status, "the stream goroutine must still be alive")
		conn.Await("deleted 2")
	})
}

// liveOwnedRows is ownedRows' live twin: one rendered arg (2), a State to make
// the unit live, and a handler that reports what it was asked to delete.
type liveOwnedRows struct{ last via.State[string] }

func (l *liveOwnedRows) Delete(ctx *via.Ctx, id int) { l.last.Set("deleted " + strconv.Itoa(id)) }
func (l *liveOwnedRows) View() h.H {
	return h.Div(l.last.Display(), h.Button(via.OnArg("click", l.Delete, 2)))
}

// An arg rendered only inside a via.When branch that is CLOSED for this
// request must 410 even though the handler itself is bound elsewhere on the
// page. This is the check doing its job: the branch, not the handler, is what
// authorizes the row, and a closed branch is an authorization answer.
func TestActionArg_argFromAClosedWhenBranchIs410(t *testing.T) {
	t.Parallel()
	admin := serve(t, via.Register(newOwnedRows("alice", true)))
	_, adminPage := do(t, admin, http.MethodGet, "/", "")
	require.Contains(t, adminPage, `id="system"`)
	systemURL := actionURL(t, adminPage, "r", 1) // the When branch's own binding
	require.Contains(t, systemURL, "a=9")

	// Same handler, same action id, same URL — but a render with the branch shut.
	plain := serve(t, via.Register(newOwnedRows("alice", false)))
	_, plainPage := do(t, plain, http.MethodGet, "/", "")
	require.NotContains(t, plainPage, `id="system"`)
	require.Contains(t, plainPage, "a=1", "the handler must still be bound, or this proves nothing")

	resp, body := do(t, plain, http.MethodPost, systemURL, "{}")
	assert.Equal(t, http.StatusGone, resp.StatusCode)
	assert.NotContains(t, body, "deleted: [9]")
}

// The other half of the check: an arg the render DID bind still dispatches
// normally. A fix that 410s everything would pass the tests above.
func TestActionArg_renderedArgStillDispatches(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(newOwnedRows("bob", false)))
	_, page := do(t, srv, http.MethodGet, "/", "")
	resp, body := do(t, srv, http.MethodPost, actionURL(t, page, "r", 0), "{}")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, "deleted: [2]", "bob's own row must still be deletable")
}

// bigList is via.Each over a large list: ONE handler, one action id, one
// thousand bound args. It pins that the arg set stays bounded by the render
// (a set of the very strings the HTML already carries) and that every row in
// it still dispatches.
type bigList struct{ hit int }

func (b *bigList) Pick(ctx *via.Ctx, id int) { b.hit = id }
func (b *bigList) row(id int) h.H            { return h.Li(h.Button(via.OnArg("click", b.Pick, id))) }
func (b *bigList) View() h.H {
	ids := make([]int, 1000)
	for i := range ids {
		ids[i] = i + 1
	}
	return h.Div(h.P(h.Str(b.hit)), h.Ul(via.Each(ids, b.row)))
}

// Each over 1000 rows: the first, last, and a middle row all dispatch, and an
// id past the end does not. The set is one entry per rendered binding — the
// same string the binding already wrote into the HTML — so it cannot outgrow
// the response it was built from.
func TestActionArg_eachOverALargeListDispatchesEveryRow(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(bigList{}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	base := actionURL(t, page, "r", 0)
	for _, id := range []string{"1", "500", "1000"} {
		url := strings.Replace(base, "a=1", "a="+id, 1)
		resp, body := do(t, srv, http.MethodPost, url, "{}")
		require.Equalf(t, http.StatusOK, resp.StatusCode, "row %s must dispatch", id)
		assert.Containsf(t, body, "<p>"+id+"</p>", "row %s did not reach the handler", id)
	}
	resp, _ := do(t, srv, http.MethodPost, strings.Replace(base, "a=1", "a=1001", 1), "{}")
	assert.Equal(t, http.StatusGone, resp.StatusCode, "an id past the end of the list must not dispatch")
}

// The arg set's cost, measured rather than asserted by eye: rendering 1000
// value-carrying bindings must stay within a small constant per binding. The
// ceiling is deliberately loose (it is a regression tripwire, not a budget) —
// what it catches is the set turning into something super-linear, or the args
// being retained per ROW rather than merged per SLOT.
func BenchmarkEachArgSet(b *testing.B) {
	h := via.Register(bigList{})
	srv := httptest.NewServer(h)
	defer srv.Close()
	b.ReportAllocs()
	for b.Loop() {
		resp, err := srv.Client().Get(srv.URL)
		if err != nil {
			b.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}
