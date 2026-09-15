// Package vt (via test) is a black-box test harness for via compositions. It
// drives a registered handler over real HTTP through via's public surface — no
// reach into unexported state — so a test can exercise the origin floor, an
// action, or a live SSE stream without hand-rolling request plumbing.
//
//	app := vt.Serve(t, via.Handler(Counter{count: &store{}}))
//	status, body := app.Action(1).Fire()      // POST the root's 2nd action, same-origin
//	require.Equal(t, 200, status)
//
//	conn := app.Connect()                      // open the per-tab SSE stream
//	app.Action(0).Tab(conn.TabID()).Fire()     // route to this connection
//	conn.Await("count: 1")                     // the push lands on the stream
//
// vt exists so security- and render-level behavior that the plain httptest
// idiom can't reach cleanly (a controlled request Host, a TLS request, the
// per-connection tab handshake) is still testable as a black box.
package vt

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// App wraps a via handler under an httptest server, registered for cleanup.
type App struct {
	t        testing.TB
	srv      *httptest.Server
	mu       sync.Mutex
	pageBody string
	fetched  bool
}

// actionURLRe matches every action URL bound to a child in one chunk of
// rendered markup. The action id is content-addressed from the handler's own
// func name, so a test cannot construct the URL — it reads the one the page
// actually shipped, which is also what makes the ?a= row datum travel with it.
func actionURLRe(child string) *regexp.Regexp {
	return regexp.MustCompile(`(?:@post\('|action=")([^'"]*_via/a/` + child + `/[A-Za-z0-9_-]+(?:[?&][^'"]*)?)['"]`)
}

// nthActionURL returns the n-th action URL for child in document order
// within markup — vt addresses actions by render position, which is how a
// test reads its own View, while the wire addresses them by handler.
func nthActionURL(markup []byte, child string, n int) (string, bool) {
	m := actionURLRe(child).FindAllSubmatch(markup, -1)
	if n < 0 || n >= len(m) {
		return "", false
	}
	return string(m[n][1]), true
}

// Serve mounts handler on an in-memory httptest server (req.TLS is nil), so
// the live-runtime suite can run under testing/synctest without touching a
// real socket.
func Serve(t testing.TB, handler http.Handler) *App {
	t.Helper()
	srv := httptest.NewTestServer(t, handler)
	// ErrorLog must be set before the first Client()/Start() call: a test that
	// deliberately drives a render panic (e.g. the off-child State guard)
	// would otherwise spam a recovered-panic stack trace to stderr.
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.Client() // forces srv.URL to populate; harmless before any real request
	return &App{t: t, srv: srv}
}

// ServeTLS mounts handler on a TLS httptest server, so the action endpoint sees
// req.TLS != nil and the origin floor enforces the https scheme. The returned
// App's client trusts the server's self-signed certificate. This stays on a
// real loopback listener — no TLS test needs fake time, and synctest requires
// an in-memory network.
func ServeTLS(t testing.TB, handler http.Handler) *App {
	t.Helper()
	srv := httptest.NewUnstartedServer(handler)
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return &App{t: t, srv: srv}
}

// URL is the server's base URL — the origin a test hand-rolls a request
// against when the builder methods above don't fit.
func (a *App) URL() string { return a.srv.URL }

// Client returns the server's http.Client, wired to reach it (over the
// in-memory network for Serve, or trusting the self-signed cert for
// ServeTLS). A test that hand-rolls a request past the builder methods above
// must send it through this, not http.DefaultClient — Serve's server has no
// real address for DefaultClient to dial.
func (a *App) Client() *http.Client { return a.srv.Client() }

// Get fetches path and returns the status code and body. A render panic is
// recovered by via itself and answered 500, not a transport-level abort, so a
// caller asserting on a render that is expected to fail sees a real status
// code rather than 0.
func (a *App) Get(path string) (int, string) {
	a.t.Helper()
	req, err := http.NewRequest(http.MethodGet, a.srv.URL+path, nil)
	if err != nil {
		a.t.Fatalf("vt.Get: build request: %v", err)
	}
	resp, err := a.srv.Client().Do(req)
	if err != nil {
		return 0, ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// Action builds a POST to the n-th action the root renders, in document
// order (the root is child "r"). The URL is read off the rendered root page at
// Fire time, not constructed: the wire id is a hash of the handler's func
// name and any ?a= row datum rides along with it, so a hand-built path would
// be wrong. By default it carries Sec-Fetch-Site: same-origin,
// modelling a same-origin browser fetch; the builder methods override that
// to exercise the origin floor.
func (a *App) Action(n int) *Action { return a.ChildAction("r", n) }

// ChildAction builds a POST to the n-th action a child renders,
// in document order. child is the key a child's own container carries:
// "0" for the root's first Child, "1" for its second, "0-1" for the second
// Child inside the first, and so on (the root is "r"; use Action for that).
func (a *App) ChildAction(child string, n int) *Action {
	return &Action{app: a, child: child, n: n, headers: map[string]string{}, body: "{}"}
}

// page fetches and caches the root page's HTML, so repeated Action/
// ChildAction calls don't re-render it.
func (a *App) page() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.fetched {
		_, body := a.Get("/")
		a.pageBody = body
		a.fetched = true
	}
	return a.pageBody
}

// Action is a builder for an action POST.
type Action struct {
	app         *App
	child       string
	n           int
	raw         string
	host        string
	headers     map[string]string
	body        string
	originSet   bool
	secFetchSet bool
	noOrigin    bool
	tab         string
	tabSet      bool
	conn        *Conn // when set, Fire reads the URL off conn's own pushed markup, not the plain page
}

// Raw overrides the URL Fire posts to, bypassing the page-read lookup — for a
// test that deliberately wants a hand-built or stale URL (e.g. asserting a
// 410 on an action id this render no longer binds).
func (a *Action) Raw(path string) *Action { a.raw = path; return a }

// Host overrides the request Host header (the authority the origin floor
// compares an Origin against). The connection still dials the test server.
func (a *Action) Host(h string) *Action { a.host = h; return a }

// Origin sets the Origin header and suppresses the default Sec-Fetch-Site, so
// the floor falls through to its Origin-host comparison.
func (a *Action) Origin(o string) *Action {
	a.headers["Origin"] = o
	a.originSet = true
	return a
}

// SecFetch sets the Sec-Fetch-Site header explicitly.
func (a *Action) SecFetch(s string) *Action {
	a.headers["Sec-Fetch-Site"] = s
	a.secFetchSet = true
	return a
}

// Tab sets the viatab signal in the POST body, routing a live action to a
// connection's child — the same channel the real client uses, since Datastar
// ships the whole (underscore-filtered) signal store with every @post.
func (a *Action) Tab(id string) *Action { a.tab, a.tabSet = id, true; return a }

// Over routes this action over c's stream: its viatab signal is set to c's
// tab id, and — unless Raw overrides it — Fire reads the action's URL off c's own
// pushed markup (see Conn.ActionURL) instead of a separate plain GET's
// render, which can carry a different shape digest than what this connection
// actually has on screen.
func (a *Action) Over(c *Conn) *Action {
	a.conn = c
	return a.Tab(c.tabID)
}

// NoOrigin sends no origin signal at all, exercising the fail-closed branch.
func (a *Action) NoOrigin() *Action { a.noOrigin = true; return a }

// Body sets the raw JSON signal body (defaults to "{}").
func (a *Action) Body(json string) *Action { a.body = json; return a }

// signalBody splices the tab id into the JSON signal body the way the browser
// would — the tab id is a signal now, not a header. A body Tab was never set
// on, or one that is not a JSON object (a test feeding deliberate garbage), is
// posted verbatim.
func (a *Action) signalBody() string {
	if !a.tabSet {
		return a.body
	}
	sig := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(a.body), &sig); err != nil {
		return a.body
	}
	sig[tabSignal], _ = json.Marshal(a.tab)
	out, err := json.Marshal(sig)
	if err != nil {
		return a.body
	}
	return string(out)
}

// Fire issues the POST and returns the status code and the response body. On a
// plain page the body is the Datastar patch; on a live one the patch goes to
// the open Conn instead and the body is empty, so assert on the Conn there. A
// refused dispatch answers 410 (nothing binds that action id in the current
// render) or 403 (origin or tab-id check), with a bodyless message.
func (a *Action) Fire() (int, string) {
	a.app.t.Helper()
	path := a.raw
	if path == "" && a.conn != nil {
		path = a.conn.ActionURL(a.child, a.n)
	}
	if path == "" {
		u, ok := nthActionURL([]byte(a.app.page()), a.child, a.n)
		if !ok {
			a.app.t.Fatalf("vt.Action.Fire: no action %s/%d found on the rendered page", a.child, a.n)
		}
		path = u
	}
	req, err := http.NewRequest(http.MethodPost, a.app.srv.URL+path, strings.NewReader(a.signalBody()))
	if err != nil {
		a.app.t.Fatalf("vt.Action.Fire: build request: %v", err)
	}
	if a.host != "" {
		req.Host = a.host
	}
	// The bundled Datastar client sends this on every @post; it is how
	// dispatch tells a JSON action apart from a native form submit.
	req.Header.Set("Datastar-Request", "true")
	// A same-origin fetch is the default; only when the test pins no origin,
	// an explicit Origin, or an explicit Sec-Fetch-Site do we drop it.
	if !a.noOrigin && !a.originSet && !a.secFetchSet {
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	for k, v := range a.headers {
		req.Header.Set(k, v)
	}
	resp, err := a.app.srv.Client().Do(req)
	if err != nil {
		a.app.t.Fatalf("vt.Action.Fire: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// Conn is an open SSE stream to a live child, carrying its per-connection tab id.
type Conn struct {
	t        testing.TB
	app      *App
	frames   <-chan string
	cancel   context.CancelFunc
	tabID    string
	closed   chan struct{} // closed when the server ended the stream
	readErr  error         // what ended it; nil for a clean end-of-response
	mu       sync.Mutex
	elements [][]byte // one entry per datastar-patch-elements frame, in arrival order
}

// tabSignal mirrors via's own wire name for the per-connection tab id.
const tabSignal = "viatab"

var tabRE = regexp.MustCompile(`"` + tabSignal + `":"([^"]+)"`)

// Connect opens the per-tab SSE stream and reads the connect-time signals frame
// that carries the tab id, so the returned Conn is ready to route actions.
func (a *App) Connect() *Conn { return a.ConnectAt("/", "{}") }

// ConnectWith is Connect with a hand-written connect body — the page signals a
// real client ships with its @post('/_via/sse'). Tests that need to prove what
// a forged body can and cannot do reach for this; everything else wants
// Connect.
func (a *App) ConnectWith(body string) *Conn { return a.ConnectAt("/", body) }

// ConnectAt is ConnectWith on a mount other than the root, for a Router with
// several pages.
func (a *App) ConnectAt(path, body string) *Conn {
	a.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	sse := strings.TrimSuffix(path, "/") + "/_via/sse"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.srv.URL+sse, strings.NewReader(body))
	if err != nil {
		cancel()
		a.t.Fatalf("vt.Connect: build request: %v", err)
	}
	// The stream connect is a POST carrying the page signals as a body. A real
	// same-origin browser fetch sends Sec-Fetch-Site; the origin floor requires
	// it (or a trusted Origin), so mimic the browser here.
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := a.srv.Client().Do(req)
	if err != nil {
		cancel()
		a.t.Fatalf("vt.Connect: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		cancel()
		resp.Body.Close()
		a.t.Fatalf("vt.Connect: status %d", resp.StatusCode)
	}

	frames := make(chan string, 256)
	c := &Conn{t: a.t, app: a, frames: frames, cancel: cancel, closed: make(chan struct{})}
	go func() {
		defer close(frames)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		// Recorded before close(closed), so AwaitClose never observes the
		// channel shut with the error not yet stored.
		defer func() {
			c.mu.Lock()
			c.readErr = sc.Err()
			c.mu.Unlock()
			close(c.closed)
		}()
		inElements := false
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event:"):
				inElements = strings.Contains(line, "datastar-patch-elements")
				if inElements {
					c.mu.Lock()
					c.elements = append(c.elements, nil)
					c.mu.Unlock()
				}
			case line == "":
				inElements = false
			case inElements:
				c.mu.Lock()
				i := len(c.elements) - 1
				c.elements[i] = append(append(c.elements[i], line...), '\n')
				c.mu.Unlock()
			}
			select {
			case frames <- line:
			case <-ctx.Done():
				return
			}
		}
	}()

	a.t.Cleanup(c.Close)
	c.tabID = c.awaitTab()
	return c
}

// TabID is the credential a live action must carry to route to this
// connection; Action.Over splices it in for you.
func (c *Conn) TabID() string { return c.tabID }

// ActionURL returns the currently-rendered URL of child's n-th action, in
// document order — read off the LATEST datastar-patch-elements frame that
// carries one, the same markup a browser's DOM would hold at this point, not a
// separate plain GET's render. Before any push has touched this child
// (e.g. the very first action after Connect), nothing has been pushed yet
// either, so this falls back to the page's own initial GET — exactly what a
// real browser would still be showing.
func (c *Conn) ActionURL(child string, n int) string {
	c.t.Helper()
	c.mu.Lock()
	frames := append([][]byte(nil), c.elements...)
	c.mu.Unlock()
	for i := len(frames) - 1; i >= 0; i-- {
		if u, ok := nthActionURL(frames[i], child, n); ok {
			return u
		}
	}
	if u, ok := nthActionURL([]byte(c.app.page()), child, n); ok {
		return u
	}
	c.t.Fatalf("vt.Conn.ActionURL: no action %s/%d found on the page or any pushed frame", child, n)
	return ""
}

// Peek returns the next buffered frame without blocking, so a test can assert
// something has NOT happened yet without letting time advance further to find
// out — under synctest, a blocking Await would just wait for whatever
// eventually arrives, which defeats a "not yet" claim at a specific instant.
func (c *Conn) Peek() (string, bool) {
	select {
	case line, ok := <-c.frames:
		return line, ok
	default:
		return "", false
	}
}

// Await blocks until a frame line containing needle arrives and returns that
// line, failing the test after 2s otherwise. The returned line lets a caller
// assert further on what rode in alongside the needle (e.g. that a raw,
// unescaped form is absent).
func (c *Conn) Await(needle string) string {
	c.t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			c.t.Fatalf("vt.Await: timed out waiting for %q", needle)
		case line, ok := <-c.frames:
			if !ok {
				c.t.Fatalf("vt.Await: stream closed before %q arrived", needle)
			}
			if strings.Contains(line, needle) {
				return line
			}
		}
	}
}

// AwaitClose blocks until the SERVER ends the stream and returns the error that
// ended it — nil when the response terminated cleanly, which is what a graceful
// shutdown owes the client; a non-nil error means the client saw a truncated
// stream. Fails the test if the stream is still open after 2s.
func (c *Conn) AwaitClose() error {
	c.t.Helper()
	select {
	case <-c.closed:
	case <-time.After(2 * time.Second):
		c.t.Fatal("vt.AwaitClose: the stream was still open after 2s")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readErr
}

// Close cancels the stream. Idempotent.
func (c *Conn) Close() { c.cancel() }

func (c *Conn) awaitTab() string {
	c.t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			c.t.Fatal("vt.Connect: no viatab frame arrived")
		case line, ok := <-c.frames:
			if !ok {
				c.t.Fatal("vt.Connect: stream closed before the tab-id frame")
			}
			if m := tabRE.FindStringSubmatch(line); m != nil {
				return m[1]
			}
		}
	}
}
