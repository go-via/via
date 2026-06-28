// Package vt (via test) is a black-box test harness for via/v2 compositions. It
// drives a registered handler over real HTTP through via's public surface — no
// reach into unexported state — so a test can exercise the origin floor, an
// action, or a live SSE stream without hand-rolling request plumbing.
//
//	app := vt.Serve(t, via.Register(Counter{count: &store{}}))
//	status, body := app.Action(1).Fire()      // POST /_via/a/1, same-origin
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
	"testing"
	"time"
)

// quietServer builds an httptest server whose net/http ErrorLog is discarded:
// a test that deliberately drives a render panic (e.g. the off-island State
// guard) would otherwise spam a recovered-panic stack trace to stderr. Real
// failures surface through the test's own assertions, not this log.
func quietServer(handler http.Handler, tls bool) *httptest.Server {
	srv := httptest.NewUnstartedServer(handler)
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	if tls {
		srv.StartTLS()
	} else {
		srv.Start()
	}
	return srv
}

// App wraps a via handler under an httptest server, registered for cleanup.
type App struct {
	t   testing.TB
	srv *httptest.Server
}

// Serve mounts handler on a plain-HTTP httptest server (req.TLS is nil).
func Serve(t testing.TB, handler http.Handler) *App {
	t.Helper()
	srv := quietServer(handler, false)
	t.Cleanup(srv.Close)
	return &App{t: t, srv: srv}
}

// ServeTLS mounts handler on a TLS httptest server, so the action endpoint sees
// req.TLS != nil and the origin floor enforces the https scheme. The returned
// App's client trusts the server's self-signed certificate.
func ServeTLS(t testing.TB, handler http.Handler) *App {
	t.Helper()
	srv := quietServer(handler, true)
	t.Cleanup(srv.Close)
	return &App{t: t, srv: srv}
}

// URL returns the server's base URL.
func (a *App) URL() string { return a.srv.URL }

// Get fetches path and returns the status code and body. A transport error
// (e.g. the server aborted the connection because the render panicked) returns
// status 0 rather than failing the test, so a caller can assert on a render
// that is expected to fail.
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

// Action builds a POST to /_via/a/{n}. By default it carries
// Sec-Fetch-Site: same-origin, modelling a same-origin browser fetch; the
// builder methods override that to exercise the origin floor.
func (a *App) Action(n int) *Action {
	return &Action{app: a, n: n, headers: map[string]string{}, body: "{}"}
}

// Action is a builder for an action POST.
type Action struct {
	app         *App
	n           int
	host        string
	headers     map[string]string
	body        string
	originSet   bool
	secFetchSet bool
	noOrigin    bool
}

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

// NoOrigin sends no origin signal at all, exercising the fail-closed branch.
func (x *Action) NoOrigin() *Action { x.noOrigin = true; return x }

// Body sets the raw JSON signal body (defaults to "{}").
func (x *Action) Body(json string) *Action { x.body = json; return x }

// Fire issues the POST and returns the status code and response body.
func (x *Action) Fire() (int, string) {
	x.app.t.Helper()
	req, err := http.NewRequest(http.MethodPost, x.app.srv.URL+"/_via/a/"+strconv.Itoa(x.n), strings.NewReader(x.body))
	if err != nil {
		x.app.t.Fatalf("vt.Action.Fire: build request: %v", err)
	}
	if x.host != "" {
		req.Host = x.host
	}
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
	t      testing.TB
	frames <-chan string
	cancel context.CancelFunc
	tabID  string
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
	go func() {
		defer close(frames)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			select {
			case frames <- sc.Text():
			case <-ctx.Done():
				return
			}
		}
	}()

	c := &Conn{t: a.t, frames: frames, cancel: cancel}
	a.t.Cleanup(c.Close)
	c.tabID = c.awaitTab()
	return c
}

// TabID returns the connection's tab id.
func (c *Conn) TabID() string { return c.tabID }

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
