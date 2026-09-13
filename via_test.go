package via_test

import (
	"bytes"
	"encoding/json"
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
	status, _ := app.Action(0).Body("{\"f0\":\"\xff\"}").Fire()
	assert.Equal(t, http.StatusOK, status)
}

// A duplicate key in a signal body must decode with v1's last-write-wins
// semantics, not jsonv2's stricter default, or the same request a v1 client
// sent starts failing under the new decoder.
func TestAction_duplicateSignalKeyLastWins(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(boundForm{}))
	status, frag := app.Action(0).Body(`{"f0":"one","f0":"two"}`).Fire()
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
				require.Equal(t, "via.go", filepath.Base(file),
					"%s imports reflect — via wiring must be reflection-free", file)
				src, err := os.ReadFile(file)
				require.NoError(t, err)
				uses := slices.Compact(slices.Sorted(slices.Values(
					regexp.MustCompile(`reflect\.\w+`).FindAllString(string(src), -1))))
				assert.Equal(t, []string{"reflect.ValueOf"}, uses,
					"via.go may only reflect to take a func value's pointer")
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
func TestActionID_indistinguishableHandlersPanic(t *testing.T) {
	t.Parallel()
	app := via.Register(idClosurePair{})
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Contains(t, logs.String(), "share the action id")
}
