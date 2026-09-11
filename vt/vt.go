// Package vt (via test) is a black-box test harness for via compositions. It
// drives a registered handler over real HTTP through via's public surface — no
// reach into unexported state — so a test can exercise the origin floor, an
// action, or a live SSE stream without hand-rolling request plumbing.
//
//	app := vt.Serve(t, via.Register(Counter{count: &store{}}))
//	status, body := app.Action(1).Fire()      // POST /_via/a/0/1, same-origin
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
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
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

// actionURLRe finds the currently-rendered action URL for a given
// {island}/{n} — the `?v=` shape digest travels with it, so a builder that
// hand-built the path would 410 the instant a test's View shape changed.
// Reading it off the page is what makes the digest an enforced contract
// rather than a second, parallel numbering scheme tests must keep in sync by
// hand.
func actionURLRe(island, n int) *regexp.Regexp {
	pat := `(?:@post\('|action=")([^'"]*_via/a/` + strconv.Itoa(island) + `/` + strconv.Itoa(n) + `(?:[?&][^'"]*)?)['"]`
	return regexp.MustCompile(pat)
}

// Serve mounts handler on an in-memory httptest server (req.TLS is nil), so
// the live-runtime suite can run under testing/synctest without touching a
// real socket.
func Serve(t testing.TB, handler http.Handler) *App {
	t.Helper()
	srv := httptest.NewTestServer(t, handler)
	// ErrorLog must be set before the first Client()/Start() call: a test that
	// deliberately drives a render panic (e.g. the off-island State guard)
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

// URL returns the server's base URL.
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

// Action builds a POST to the root's action table, /_via/a/0/{n} (island 0
// is the root). The URL (including its `?v=` shape digest) is read off the
// rendered root page at Fire time, not constructed — every action posts to
// {base}/_via/a/{island}/{n}?v={digest}, and the digest 410s a click whose
// page has gone stale, so a hand-built path would drift the moment a test's
// View shape changed. By default it carries Sec-Fetch-Site: same-origin,
// modelling a same-origin browser fetch; the builder methods override that
// to exercise the origin floor.
func (a *App) Action(n int) *Action {
	return &Action{app: a, island: 0, n: n, headers: map[string]string{}, body: "{}"}
}

// IslandAction builds a POST to an embedded island's action table —
// /_via/a/{island}/{n}. island is the URL id an island's own container
// carries: 1 for the first embedded child, 2 for the second, and so on (0 is
// the root; use Action for that).
func (a *App) IslandAction(island, n int) *Action {
	return &Action{app: a, island: island, n: n, headers: map[string]string{}, body: "{}"}
}

// page fetches and caches the root page's HTML, so repeated Action/
// IslandAction calls don't re-render it. Refresh drops the cache when a test
// needs the current (possibly reshaped) page instead.
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

// Refresh drops the cached root page, so the next Action/IslandAction call
// re-fetches it. Needed after a mutation that changes the View's rendered
// shape (a branched View whose action set or digest differs by state).
func (a *App) Refresh() {
	a.mu.Lock()
	a.fetched = false
	a.mu.Unlock()
}

// Action is a builder for an action POST.
type Action struct {
	app         *App
	island      int
	n           int
	raw         string
	host        string
	headers     map[string]string
	body        string
	originSet   bool
	secFetchSet bool
	noOrigin    bool
	conn        *Conn // when set, Fire reads the URL off conn's own pushed markup, not the stateless page
}

// Raw overrides the URL Fire posts to, bypassing the page-read lookup — for a
// test that deliberately wants a hand-built or stale URL (e.g. asserting a
// 410 on a shape-digest mismatch).
func (x *Action) Raw(path string) *Action { x.raw = path; return x }

// Host overrides the request Host header (the authority the origin floor
// compares an Origin against). The connection still dials the test server.
func (x *Action) Host(h string) *Action { x.host = h; return x }

// Origin sets the Origin header and suppresses the default Sec-Fetch-Site, so
// the floor falls through to its Origin-host comparison.
func (x *Action) Origin(o string) *Action {
	x.headers["Origin"] = o
	x.originSet = true
	return x
}

// SecFetch sets the Sec-Fetch-Site header explicitly.
func (x *Action) SecFetch(s string) *Action {
	x.headers["Sec-Fetch-Site"] = s
	x.secFetchSet = true
	return x
}

// Tab sets the X-Via-Tab header, routing a live action to a connection's island.
func (x *Action) Tab(id string) *Action { x.headers["X-Via-Tab"] = id; return x }

// Live routes this action against c: its X-Via-Tab header is set to c's tab
// id, and — unless Raw overrides it — Fire reads the action's URL off c's own
// pushed markup (see Conn.ActionURL) instead of a separate stateless GET's
// render, which can carry a different shape digest than what this connection
// actually has on screen.
func (x *Action) Live(c *Conn) *Action {
	x.conn = c
	return x.Tab(c.tabID)
}

// NoOrigin sends no origin signal at all, exercising the fail-closed branch.
func (x *Action) NoOrigin() *Action { x.noOrigin = true; return x }

// Body sets the raw JSON signal body (defaults to "{}").
func (x *Action) Body(json string) *Action { x.body = json; return x }

// Fire issues the POST and returns the status code and response body.
func (x *Action) Fire() (int, string) {
	x.app.t.Helper()
	path := x.raw
	if path == "" && x.conn != nil {
		path = x.conn.ActionURL(x.island, x.n)
	}
	if path == "" {
		m := actionURLRe(x.island, x.n).FindStringSubmatch(x.app.page())
		if m == nil {
			x.app.t.Fatalf("vt.Action.Fire: no action %d/%d found on the rendered page", x.island, x.n)
		}
		path = m[1]
	}
	req, err := http.NewRequest(http.MethodPost, x.app.srv.URL+path, strings.NewReader(x.body))
	if err != nil {
		x.app.t.Fatalf("vt.Action.Fire: build request: %v", err)
	}
	if x.host != "" {
		req.Host = x.host
	}
	// The bundled Datastar client sends this on every @post; it is how
	// dispatch tells a JSON action apart from a native form submit.
	req.Header.Set("Datastar-Request", "true")
	// A same-origin fetch is the default; only when the test pins no origin,
	// an explicit Origin, or an explicit Sec-Fetch-Site do we drop it.
	if !x.noOrigin && !x.originSet && !x.secFetchSet {
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	for k, v := range x.headers {
		req.Header.Set(k, v)
	}
	resp, err := x.app.srv.Client().Do(req)
	if err != nil {
		x.app.t.Fatalf("vt.Action.Fire: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// Conn is an open SSE stream to a live island, carrying its per-connection tab id.
type Conn struct {
	t        testing.TB
	app      *App
	frames   <-chan string
	cancel   context.CancelFunc
	tabID    string
	mu       sync.Mutex
	elements []byte // every datastar-patch-elements frame's data lines seen so far, concatenated in arrival order
}

var tabRE = regexp.MustCompile(`"_viatab":"([^"]+)"`)

// Connect opens the per-tab SSE stream and reads the connect-time signals frame
// that carries the tab id, so the returned Conn is ready to route actions.
func (a *App) Connect() *Conn {
	a.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.srv.URL+"/_via/sse", strings.NewReader("{}"))
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
	c := &Conn{t: a.t, app: a, frames: frames, cancel: cancel}
	go func() {
		defer close(frames)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		inElements := false
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event:"):
				inElements = strings.Contains(line, "datastar-patch-elements")
			case line == "":
				inElements = false
			case inElements:
				c.mu.Lock()
				c.elements = append(c.elements, line...)
				c.elements = append(c.elements, '\n')
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

// TabID returns the connection's tab id.
func (c *Conn) TabID() string { return c.tabID }

// ActionURL returns the currently-rendered action URL for {island}/{n} — read
// off the LATEST datastar-patch-elements frame this connection has actually
// received, the same markup a browser's DOM would hold at this point, not a
// separate stateless GET's render. Before any push has touched this island
// (e.g. the very first action after Connect), nothing has been pushed yet
// either, so this falls back to the page's own initial GET — exactly what a
// real browser would still be showing.
func (c *Conn) ActionURL(island, n int) string {
	c.t.Helper()
	re := actionURLRe(island, n)
	c.mu.Lock()
	buf := append([]byte(nil), c.elements...)
	c.mu.Unlock()
	if m := re.FindAllSubmatch(buf, -1); m != nil {
		return string(m[len(m)-1][1])
	}
	m := re.FindStringSubmatch(c.app.page())
	if m == nil {
		c.t.Fatalf("vt.Conn.ActionURL: no action %d/%d found on the page or any pushed frame", island, n)
	}
	return m[1]
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

// Close cancels the stream. Idempotent.
func (c *Conn) Close() { c.cancel() }

func (c *Conn) awaitTab() string {
	c.t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			c.t.Fatal("vt.Connect: no _viatab frame arrived")
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
