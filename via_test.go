package via_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
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
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, string(b)
}

// The GET page must ship the server-rendered skeleton — the current value baked
// into HTML, both wired buttons, the morph-target #root, and the client script.
func TestPageShipsServerRenderedSkeleton(t *testing.T) {
	resp, body := do(t, newCounter(t), http.MethodGet, "/", "")

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("page Content-Type = %q, want text/html", ct)
	}
	for _, want := range []string{
		`<div id="root"`,
		`<h1>0</h1>`,                         // value rendered server-side, not a signal
		`data-on:click="@post('/_via/a/0')"`, // Dec, declared first
		`data-on:click="@post('/_via/a/1')"`, // Inc, declared second
		`<script type="module" src="/_via/datastar.js">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q\n---page---\n%s", want, body)
		}
	}
}

// Event bindings must use Datastar v1's colon key syntax (data-on:click). The
// old dash form (data-on-click) is parsed by v1.0.2 as a nonexistent plugin
// "on-click" and silently dropped — the button renders, no listener attaches,
// and the click is dead in the browser while every server-side test still
// passes. Assert the dash form never ships so that regression can't reappear.
func TestEventBindingUsesDatastarColonSyntaxNotDeadDashForm(t *testing.T) {
	_, body := do(t, newCounter(t), http.MethodGet, "/", "")

	if !strings.Contains(body, `data-on:click="@post(`) {
		t.Errorf("page is missing the v1 colon event binding data-on:click\n---page---\n%s", body)
	}
	if strings.Contains(body, "data-on-click") {
		t.Errorf("page ships the dead v0.x dash form data-on-click (silently ignored by Datastar v1.0.2)\n---page---\n%s", body)
	}
}

// An action must mutate the server-side dependency and return the re-rendered
// fragment as a text/html element-patch reflecting the NEW value — and the state
// must persist across requests (it lives in the store, not the request).
func TestActionElementPatchesAndPersists(t *testing.T) {
	srv := newCounter(t)

	resp, body := do(t, srv, http.MethodPost, "/_via/a/1", "{}") // Inc: 0 -> 1
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html (element-patch)", ct)
	}
	for _, want := range []string{`<div id="root"`, `<h1>1</h1>`} {
		if !strings.Contains(body, want) {
			t.Errorf("patch missing %q\n---patch---\n%s", want, body)
		}
	}

	if _, body := do(t, srv, http.MethodPost, "/_via/a/1", "{}"); !strings.Contains(body, `<h1>2</h1>`) {
		t.Errorf("second Inc did not persist to 2\n%s", body) // 1 -> 2
	}
	if _, body := do(t, srv, http.MethodPost, "/_via/a/0", "{}"); !strings.Contains(body, `<h1>1</h1>`) {
		t.Errorf("Dec did not bring state back to 1\n%s", body) // 2 -> 1
	}
}

// An action index with no registered handler must be rejected with 410 Gone, so
// a stale client learns the action is gone rather than silently no-op.
func TestOutOfRangeActionIsGone(t *testing.T) {
	resp, _ := do(t, newCounter(t), http.MethodPost, "/_via/a/99", "{}")
	if resp.StatusCode != http.StatusGone {
		t.Errorf("status = %d, want 410 Gone", resp.StatusCode)
	}
}

// Positional dispatch is only sound against the render shape the client was
// served. The server-state counter renders no signals, so a request carrying a
// signal the View never declares is a mismatch and must be rejected with 410
// rather than dispatched against a slot table that does not line up.
func TestRenderShapeMismatchIsRejected(t *testing.T) {
	resp, body := do(t, newCounter(t), http.MethodPost, "/_via/a/1", `{"s0":1}`)
	if resp.StatusCode != http.StatusGone {
		t.Errorf("status = %d, want 410 Gone (render-shape mismatch)\nbody: %s", resp.StatusCode, body)
	}
}

// The vendored Datastar client must be served from the embedded asset with a JS
// content-type, or the module script tag on the page 404s and nothing hydrates.
func TestEmbeddedDatastarClientIsServedAsJS(t *testing.T) {
	resp, body := do(t, newCounter(t), http.MethodGet, "/_via/datastar.js", "")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("Content-Type = %q, want text/javascript", ct)
	}
	if len(body) == 0 {
		t.Error("served datastar.js was empty")
	}
}
