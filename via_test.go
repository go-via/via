package via_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/go-via/via/v2"
	"github.com/go-via/via/v2/h"
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
// API. It mirrors example/counter: server-authoritative state in an injected
// dependency, element-patched on each action.
type counter struct{ count *store }

func (c *counter) Inc(ctx *via.Ctx) { c.count.Add(1) }
func (c *counter) Dec(ctx *via.Ctx) { c.count.Add(-1) }

func (c *counter) View() h.H {
	return h.Div(
		h.H1(h.Str(c.count.Value())),
		h.Button(via.OnClick(c.Dec), h.Str("-")),
		h.Button(via.OnClick(c.Inc), h.Str("+")),
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
	// Simulate a same-origin browser fetch so the action endpoint's origin floor
	// admits the request; tests that exercise the floor itself build their own
	// requests (see post() in security_test.go).
	if method == http.MethodPost {
		req.Header.Set("Sec-Fetch-Site", "same-origin")
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
func serve(t *testing.T, handler http.Handler) *httptest.Server {
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

// The GET page must ship the server-rendered skeleton — the current value baked
// into HTML, both wired buttons, the morph-target #root, and the client script.
func TestPage_shipsServerRenderedSkeleton(t *testing.T) {
	t.Parallel()
	resp, body := do(t, newCounter(t), http.MethodGet, "/", "")

	ct := resp.Header.Get("Content-Type")
	assert.True(t, strings.HasPrefix(ct, "text/html"), "page Content-Type = %q, want text/html", ct)
	for _, want := range []string{
		`<div id="root"`,
		`<h1>0</h1>`,                         // value rendered server-side, not a signal
		`data-on:click="@post('/_via/a/0')"`, // Dec, declared first
		`data-on:click="@post('/_via/a/1')"`, // Inc, declared second
		`src="/_via/datastar.js">`,           // module script tag (now nonce'd between attrs)
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

	resp, body := do(t, srv, http.MethodPost, "/_via/a/1", "{}") // Inc: 0 -> 1
	ct := resp.Header.Get("Content-Type")
	assert.True(t, strings.HasPrefix(ct, "text/html"), "Content-Type = %q, want text/html (element-patch)", ct)
	for _, want := range []string{`<div id="root"`, `<h1>1</h1>`} {
		assert.Contains(t, body, want, "patch missing fragment")
	}

	_, body = do(t, srv, http.MethodPost, "/_via/a/1", "{}") // 1 -> 2
	assert.Contains(t, body, `<h1>2</h1>`, "second Inc did not persist to 2")
	_, body = do(t, srv, http.MethodPost, "/_via/a/0", "{}") // 2 -> 1
	assert.Contains(t, body, `<h1>1</h1>`, "Dec did not bring state back to 1")
}

// An action index with no registered handler must be rejected with 410 Gone, so
// a stale client learns the action is gone rather than silently no-op.
func TestOutOfRangeAction_isGone(t *testing.T) {
	t.Parallel()
	resp, _ := do(t, newCounter(t), http.MethodPost, "/_via/a/99", "{}")
	assert.Equal(t, http.StatusGone, resp.StatusCode, "want 410 Gone")
}

// Positional dispatch is only sound against the render shape the client was
// served. The server-state counter renders no signals, so a request carrying a
// signal the View never declares is a mismatch and must be rejected with 410
// rather than dispatched against a slot table that does not line up.
func TestRenderShapeMismatch_isRejected(t *testing.T) {
	t.Parallel()
	resp, body := do(t, newCounter(t), http.MethodPost, "/_via/a/1", `{"s0":1}`)
	assert.Equal(t, http.StatusGone, resp.StatusCode, "want 410 Gone (render-shape mismatch)\nbody: %s", body)
}

// A request whose signal-key set is the same SIZE as the rendered shape but a
// different key is still a render-shape mismatch and must 410, not be dispatched
// against a slot table that does not line up.
func TestRenderShapeMismatch_sameLengthDifferentKeyIsRejected(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(boundForm{}))
	resp, _ := post(t, srv, "/_via/a/0", `{"sX":"1"}`, sameOrigin())
	assert.Equal(t, http.StatusGone, resp.StatusCode, "one declared signal vs one foreign key must 410")
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
	resp, body := do(t, newCounter(t), http.MethodPost, "/_via/a/1", "{}")

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
	return h.Div(h.Button(via.OnClick(n.Ping), h.Str("ping")))
}

// An action that leaves the rendered View identical must return 204 No Content,
// not re-send an identical #root the browser would morph onto itself. The
// runtime infers this by comparing the pre- and post-action renders — no author
// annotation (no NoContent call) required.
func TestAction_returns204WhenViewIsUnchanged(t *testing.T) {
	t.Parallel()
	resp, body := post(t, serve(t, via.Register(noopComp{})), "/_via/a/0", "{}", sameOrigin())
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	assert.Empty(t, body)
}

// formComp uses OnSubmit on a form.
type formComp struct{ q via.Signal[string] }

func (c *formComp) Go(ctx *via.Ctx) {}
func (c *formComp) View() h.H {
	return h.Form(via.OnSubmit(c.Go), h.Input(c.q.Bind()))
}

// OnSubmit wires a form submit to a POST action with Datastar's colon event
// syntax. Datastar auto-prevents a form's default submit, so no modifier is
// needed.
func TestOnSubmit_wiresSubmitToAPostAction(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(formComp{})), http.MethodGet, "/", "")
	assert.Contains(t, body, `data-on:submit="@post('/_via/a/0')"`)
	assert.NotContains(t, body, "data-on-submit", "must use the colon form, not the dead dash form")
}

// reqEchoer is a stateless component whose action copies a header off the
// triggering request into a rendered field.
type reqEchoer struct{ echo string }

func (r *reqEchoer) Grab(ctx *via.Ctx) { r.echo = ctx.Request().Header.Get("X-Echo") }
func (r *reqEchoer) View() h.H {
	return h.Div(h.Button(via.OnClick(r.Grab), h.Str("x")), h.P(h.Str(r.echo)))
}

// An action must be able to read the HTTP request that triggered it — auth
// headers, cookies, client info — through ctx.Request(); without it there is no
// way to do request-native wiring from a handler. The value the action pulls out
// of the request must reach the re-rendered response.
func TestAction_canReadTheTriggeringRequest(t *testing.T) {
	t.Parallel()
	_, body := post(t, serve(t, via.Register(reqEchoer{})), "/_via/a/0", "{}", map[string]string{
		"Sec-Fetch-Site": "same-origin",
		"X-Echo":         "hello-from-header",
	})
	assert.Contains(t, body, "hello-from-header", "the action must see the triggering request via ctx.Request()")
}

// viaCallNames are the via entry points whose arguments must be named method
// values or by-value compositions — never an address-of or a closure.
var viaCallNames = map[string]bool{
	"Register": true, "Embed": true, "Subscribe": true, "When": true, "Each": true,
	"OnClick": true, "OnSubmit": true, "OnInput": true, "OnChange": true,
}

// The framework's headline promise is that user code never writes '&' and never
// passes a closure at a via call site (Register/Embed/On*). A violation that
// compiles silently erodes the design, so this asserts it structurally over the
// example sources — the canonical user-facing call sites. It is an interim
// guard; the type-level closure ban is tracked as follow-up (see DESIGN.md).
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

// via's headline guarantee is reflection-free wiring: the composition is bound
// by generics + interface assertions + positional/handle identity, never by
// reflecting over its fields, method names, or struct tags (which is what the
// old reflect-based framework did). This locks that — no via source file may
// import "reflect". (Signal values decode through encoding/json, which reflects
// internally; that is data decoding, not wiring, and is out of this guard.)
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
				assert.NotEqualf(t, `"reflect"`, imp.Path.Value,
					"%s imports reflect — via wiring must be reflection-free", file)
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
