// Package vt (via test) is a black-box test harness for via compositions. It
// drives a registered handler over real HTTP — no reach into a running app's
// state, only the naming rules via itself uses for a handler or a child — so a
// test can exercise the origin floor, an action, or a live SSE stream without
// hand-rolling request plumbing.
//
//	app := vt.Serve(t, via.Handler(Counter{count: &store{}}))
//	inc := func(c *Counter) func(*via.Ctx) { return c.Inc }
//	status, body := vt.Act(app, inc).Fire()   // POST Counter.Inc, same-origin
//	require.Equal(t, 200, status)
//
//	conn := app.Connect()                     // open the per-tab SSE stream
//	vt.Act(app, inc).Over(conn).Fire()        // route to this connection
//	conn.Await("count: 1")                    // the push lands on the stream
//
// vt exists so security- and render-level behavior that the plain httptest
// idiom can't reach cleanly (a controlled request Host, a TLS request, the
// per-connection tab handshake) is still testable as a black box.
package vt

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/go-via/via"
	"github.com/go-via/via/internal/actionid"
)

// App wraps a via handler under an httptest server, registered for cleanup.
type App struct {
	t     testing.TB
	srv   *httptest.Server
	mu    sync.Mutex
	pages map[string]string // path → the HTML Act last read its URLs off
}

// actionURLRe matches every action URL in one chunk of rendered markup. The
// action id is content-addressed from the handler's func name and receiver
// path, so vt reads the URL the page actually shipped, which is also what
// makes the ?a= row datum travel with it. A binding with a query ships it in
// the data-via-q-<event> attribute just before its data-on, which the
// expression appends to the path (group 1); the path is group 2, its unit
// address group 3 and its id group 4.
var actionURLRe = regexp.MustCompile(`(?:data-via-q-[^=\s]+="([^"]*)" data-on:[^=\s]+="@post\('|@post\('|action=")` +
	`([^'"]*_via/a/([^/'"]+)/([A-Za-z0-9_-]+)(?:[?&][^'"]*)?)['"]`)

// findActionURL returns the URL in markup that posts to one of targets, tried
// in order, with exactly query ("" for an action with no arg).
func findActionURL(markup []byte, targets []actionid.Target, query string) (string, bool) {
	m := actionURLRe.FindAllSubmatch(markup, -1)
	for _, tg := range targets {
		for _, u := range m {
			if (tg.Unit != "" && string(u[3]) != tg.Unit) || string(u[4]) != tg.ID {
				continue
			}
			// The URL sits in attributes, so it is entity-escaped there: the
			// browser posts the decoded text.
			full := html.UnescapeString(string(u[2]) + string(u[1]))
			if _, q, _ := strings.Cut(full, "?"); q == query {
				return full, true
			}
		}
	}
	return "", false
}

// Serve mounts handler on an in-memory httptest server (req.TLS is nil), so
// the live-runtime suite can run under testing/synctest without touching a
// real socket. The App keeps cookies as one browser does: a session an action
// mints or logs in carries to every later request. A test that needs a
// cookieless request copies App.Client and clears its Jar.
func Serve(t testing.TB, handler http.Handler) *App {
	t.Helper()
	srv := httptest.NewTestServer(t, handler)
	// ErrorLog must be set before the first Client()/Start() call: a test that
	// deliberately drives a render panic (e.g. the off-child State guard)
	// would otherwise spam a recovered-panic stack trace to stderr.
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	keepCookies(srv.Client())
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
	keepCookies(srv.Client()) // cookies as Serve keeps them
	return &App{t: t, srv: srv}
}

// keepCookies gives c a cookie jar; see Serve.
func keepCookies(c *http.Client) {
	jar, _ := cookiejar.New(nil)
	c.Jar = jar
}

// Logger returns a logger that writes each record to t.Log, so via's
// diagnostics land in the test's own output. Pass it to via.WithLogger:
//
//	app := vt.Serve(t, via.Handler(Page{}, via.WithLogger(vt.Logger(t))))
//
// go test then prints them under the test that caused them, only when it
// fails or runs with -v. A record logged after the test has finished (a
// stream winding down) is dropped, since t.Log would panic.
func Logger(t testing.TB) *slog.Logger {
	w := &testLog{t: t}
	t.Cleanup(w.end)
	// The time is dropped: under synctest it is the fake clock's, and go test
	// already orders the lines under their test.
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{ReplaceAttr: dropTime}))
}

func dropTime(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 && a.Key == slog.TimeKey {
		return slog.Attr{}
	}
	return a
}

type testLog struct {
	mu    sync.Mutex
	t     testing.TB
	ended bool
}

func (w *testLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.ended {
		w.t.Log(strings.TrimSuffix(string(p), "\n"))
	}
	return len(p), nil
}

func (w *testLog) end() {
	w.mu.Lock()
	w.ended = true
	w.mu.Unlock()
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
		a.t.Fatalf("vt.App.Get: build request: %v", err)
	}
	resp, err := a.srv.Client().Do(req)
	if err != nil {
		return 0, ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// Act builds a POST to the action fn returns, taken off the mounted
// composition: vt.Act(app, func(p *Page) func(*via.Ctx) { return p.Chat.Send }).
// The handler is named, not counted, so reordering the View moves no test. A
// handler on a field rendered with via.Child is found under that child. The
// URL is read off the page at "/" (another path with At), cached and fetched
// again when the handler is not on the cached copy, or, with Over, off the
// Conn's pushed frames. By default it carries Sec-Fetch-Site: same-origin,
// modelling a same-origin browser fetch; the builder methods override that to
// exercise the origin floor.
func Act[P any](a *App, fn func(*P) func(*via.Ctx)) *Action {
	root := new(P)
	return a.act(root, fn(root), "")
}

// ActArg is Act for a handler bound with on.Bind(fn, arg): it posts to the
// binding of fn that carries arg, as clicking that row's button would.
func ActArg[P, T any](a *App, fn func(*P) func(*via.Ctx, T), arg T) *Action {
	data, err := json.Marshal(arg)
	if err != nil {
		a.t.Fatalf("vt.ActArg: arg does not encode: %v", err)
	}
	root := new(P)
	return a.act(root, fn(root), "a="+url.QueryEscape(string(data)))
}

// act builds an Action for h, a handler taken off root.
func (a *App) act(root, h any, query string) *Action {
	return &Action{app: a, targets: actionid.Targets(root, h), name: actionid.FuncOf(reflect.ValueOf(h).Pointer()).Name,
		query: query, headers: map[string]string{}, body: "{}"}
}

// Post builds a POST to path as is, for a URL the test read itself: one off a
// fresh Get, or a forged or stale one.
func (a *App) Post(path string) *Action {
	return &Action{app: a, raw: path, headers: map[string]string{}, body: "{}"}
}

// A SigValue is a signal value an action posts; see Sig and Action.Set.
type SigValue struct {
	slot string
	val  json.RawMessage
}

// Sig is the value v for the Signal fn points at, under the slot via gives it:
// vt.Sig(func(p *Page) *via.Signal[string] { return &p.Chat.Draft }, "hi")
// posts {"chat__draft":"hi"}. Pass it to Action.Set.
func Sig[P, T any](fn func(*P) *via.Signal[T], v T) SigValue {
	val, err := json.Marshal(v)
	if err != nil {
		panic("vt.Sig: the value does not encode: " + err.Error())
	}
	return SigValue{slot: Slot(fn), val: val}
}

// Slot is the wire slot via gives the Signal fn points at: "chat__draft" for
// &p.Chat.Draft under a Chat child, "form_name" for a plain nested struct.
func Slot[P, T any](fn func(*P) *via.Signal[T]) string {
	root := new(P)
	slot, ok := actionid.Slot(root, fn(root))
	if !ok {
		panic("vt.Slot: fn must return the address of a via.Signal field of the *P it is given, outside any array below the last child")
	}
	return slot
}

// Set posts vals with the action, over any Body: the browser sends every
// signal the page binds, and these are the ones the test names.
func (a *Action) Set(vals ...SigValue) *Action {
	a.sigs = append(a.sigs, vals...)
	return a
}

// Key is the key via gives the child fn points at, its field path from the
// root: vt.Key(func(p *Page) *Chat { return &p.Chat }) is "Chat". The child's
// container is #via-i{key}.
func Key[P, C any](fn func(*P) *C) string {
	root := new(P)
	key, ok := actionid.Key(root, fn(root))
	if !ok {
		panic("vt.Key: fn must return the address of a field (or array element) of the *P it is given")
	}
	return key
}

// page returns the HTML at path, cached so repeated Act calls don't re-render
// it; fresh fetches it again.
func (a *App) page(path string, fresh bool) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	body, ok := a.pages[path]
	if !ok || fresh {
		_, body = a.Get(path)
		if a.pages == nil {
			a.pages = map[string]string{}
		}
		a.pages[path] = body
	}
	return body
}

// At reads the action's URL off the page at path instead of "/", for a
// handler on a composition mounted there.
func (a *Action) At(path string) *Action {
	a.path = path
	return a
}

// Action is a builder for an action POST.
type Action struct {
	app         *App
	targets     []actionid.Target
	name        string // the handler's Go name, for a lookup that fails
	query       string // "a=…" for an on.Bind arg
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
	expect      []int // the codes Fire accepts; empty means any 2xx
	sigs        []SigValue
	path        string // the page At names; "" is "/"
	form        url.Values
	redirect    string // where the last Fire's answer navigates
}

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

// Header sets any other request header, such as the X-Forwarded-Proto a
// TLS-terminating proxy adds.
func (a *Action) Header(k, v string) *Action { a.headers[k] = v; return a }

// Tab sets the viatab signal in the POST body, routing a live action to a
// connection's child — the same channel the real client uses, since Datastar
// ships the whole (underscore-filtered) signal store with every @post.
func (a *Action) Tab(id string) *Action { a.tab, a.tabSet = id, true; return a }

// Over routes this action over c's stream: its viatab signal is set to c's
// tab id, and — unless Post pinned it — Fire reads the action's URL off c's
// own pushed markup (see Conn.find) instead of a separate plain GET's render.
// That GET renders a fresh tab, so once c's actions have changed what is on
// screen (a row added, a branch toggled), it may not bind the handler at all.
func (a *Action) Over(c *Conn) *Action {
	a.conn = c
	return a.Tab(c.tabID)
}

// NoOrigin sends no origin signal at all, as a non-browser client does: the
// origin check admits it by default and refuses it under WithTrustedOrigin.
func (a *Action) NoOrigin() *Action { a.noOrigin = true; return a }

// Body sets the raw JSON signal body (defaults to "{}").
func (a *Action) Body(json string) *Action { a.body = json; return a }

// signalBody splices the Set values and the tab id signal into the JSON
// signal body the way the browser would. A body with neither, or one that is
// not a JSON object (a test feeding deliberate garbage), is posted verbatim.
func (a *Action) signalBody() string {
	if !a.tabSet && len(a.sigs) == 0 {
		return a.body
	}
	sig := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(a.body), &sig); err != nil || sig == nil {
		if len(a.sigs) > 0 {
			a.app.t.Fatalf("vt.Action.Set: the Body %q is no JSON object to set signals in", a.body)
		}
		return a.body
	}
	for _, s := range a.sigs {
		sig[s.slot] = s.val
	}
	if a.tabSet {
		sig[tabSignal], _ = json.Marshal(a.tab)
	}
	out, err := json.Marshal(sig)
	if err != nil {
		return a.body
	}
	return string(out)
}

// Expect makes Fire accept exactly codes instead of any 2xx, for a test that
// asserts a refusal.
func (a *Action) Expect(codes ...int) *Action {
	a.expect = codes
	return a
}

// Fire issues the POST and returns the status code and the response body. It
// fails the test unless the action answers 2xx or 303, or a code given to
// Expect. It follows no redirect; Redirected reports the target. A
// plain page answers 200 with the Datastar patch: HTML, or SSE frames when the
// action wrote or opened signals. A live unit, and a plain child of a live
// root, answers 204 and sends the patch to the open Conn, so assert on the
// Conn there; a plain page answers 204 too
// when nothing changed. A refused dispatch answers with its reason as a
// one-line text body: 410 when the target is gone (no such action id in the
// current render, no such child, the stream closed), 403 for the origin or
// tab-id check, and 400, 413 or 503 for a malformed body, an oversized one,
// or a busy or closing server.
func (a *Action) Fire() (int, string) {
	a.app.t.Helper()
	client := *a.app.srv.Client()
	client.CheckRedirect = stayPut
	resp, err := client.Do(a.request())
	if err != nil {
		a.app.t.Fatalf("vt.Action.Fire: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	a.redirect = redirectTarget(resp)
	a.check(resp.StatusCode, string(b))
	return resp.StatusCode, string(b)
}

// Redirected is where the last Fire's answer navigates: the Location of a
// form's 303, or the target of an action's navigation script. It is "" when
// the action queued no Redirect.
func (a *Action) Redirected() string { return a.redirect }

// stayPut stops a client at a redirect, so Fire answers with the redirect
// itself and Redirected can report where it goes.
func stayPut(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func redirectTarget(resp *http.Response) string {
	if resp.StatusCode/100 == 3 {
		return resp.Header.Get("Location")
	}
	var attrs map[string]string
	_ = json.Unmarshal([]byte(resp.Header.Get("Datastar-Script-Attributes")), &attrs)
	return attrs["data-via-to"]
}

// Form submits fields as a browser submits a via.PostForm: multipart, with no
// Datastar header, so the handler reads them with ctx.Request().FormValue. A
// handler's Redirect answers 303; see Redirected.
func (a *Action) Form(fields url.Values) *Action {
	a.form = fields
	return a
}

// request builds the POST Fire sends.
func (a *Action) request() *http.Request {
	a.app.t.Helper()
	body, ctype := a.payload()
	req, err := http.NewRequest(http.MethodPost, a.app.srv.URL+a.URL(), body)
	if err != nil {
		a.app.t.Fatalf("vt.Action.Fire: build request: %v", err)
	}
	if a.host != "" {
		req.Host = a.host
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	} else {
		// The bundled Datastar client sends this on every @post; it is how
		// dispatch tells a JSON action apart from a native form submit.
		req.Header.Set("Datastar-Request", "true")
	}
	// A same-origin fetch is the default; only when the test pins no origin,
	// an explicit Origin, or an explicit Sec-Fetch-Site do we drop it.
	if !a.noOrigin && !a.originSet && !a.secFetchSet {
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	for k, v := range a.headers {
		req.Header.Set(k, v)
	}
	return req
}

// payload is the body Fire posts and its content type: the JSON signal body
// (type left to the Datastar header), or the multipart form Form names.
func (a *Action) payload() (io.Reader, string) {
	if a.form == nil {
		return strings.NewReader(a.signalBody()), ""
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, vs := range a.form {
		for _, v := range vs {
			w.WriteField(k, v)
		}
	}
	if a.tabSet {
		w.WriteField(tabFormField, a.tab)
	}
	w.Close()
	return &buf, w.FormDataContentType()
}

// check fails the test on a status Fire was not told to accept.
func (a *Action) check(code int, body string) {
	a.app.t.Helper()
	if slices.Contains(a.expect, code) || len(a.expect) == 0 && (code/100 == 2 || code == http.StatusSeeOther) {
		return
	}
	if len(body) > 300 {
		body = body[:300] + "…"
	}
	if len(a.expect) > 0 {
		a.app.t.Fatalf("vt.Action.Fire: %s answered %d, want one of %v: %s", a.label(), code, a.expect, body)
	}
	a.app.t.Fatalf("vt.Action.Fire: %s answered %d: %s; chain .Expect(%d) to assert a refusal", a.label(), code, body, code)
}

// label names the action in a failure: its handler as T.M, or the URL Post
// pinned.
func (a *Action) label() string {
	if a.name == "" {
		return a.raw
	}
	name := strings.TrimSuffix(a.name[strings.LastIndex(a.name, "/")+1:], "-fm")
	_, name, _ = strings.Cut(name, ".")
	return strings.NewReplacer("(*", "", ")", "").Replace(name)
}

// URL is the action URL Fire posts to: the one Post pinned, else the open
// Conn's, else the one the rendered page carries.
func (a *Action) URL() string {
	a.app.t.Helper()
	if a.raw != "" {
		return a.raw
	}
	if a.conn != nil {
		if u, ok := a.conn.find(a.targets, a.query); ok {
			return u
		}
	}
	path := cmp.Or(a.path, "/")
	u, ok := findActionURL([]byte(a.app.page(path, false)), a.targets, a.query)
	if !ok {
		u, ok = findActionURL([]byte(a.app.page(path, true)), a.targets, a.query)
	}
	if !ok {
		hint := ""
		if strings.Contains(a.name, "[...]") {
			// Inside a generic type's View, c.M compiles to a closure named
			// after the View, so the binding carries that name, not M's.
			hint = "; a method on a generic type is bound under its View's closure name, so post its URL with Post"
		}
		name := a.label()
		if a.query != "" {
			name += " with ?" + a.query
		}
		a.app.t.Fatalf("vt.Action.URL: %s is bound nowhere on the page at %s%s", name, path, hint)
	}
	return u
}

// Conn is an open SSE stream to a live page, carrying its tab id.
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
	events   int      // SSE events read so far
	recent   []string // the last few of them, one line each, for failure messages
	pending  []string // the event being read: its name, then its data lines
}

// recentKept is how many events a failed Await prints. Enough to show the
// push before the one that was expected, few enough to read.
const recentKept = 3

// maxFrameLine caps one line of the stream.
const maxFrameLine = 16 << 20

// awaitTimeout bounds every wait on the stream.
const awaitTimeout = 2 * time.Second

// frameHead and frameTail cap each printed event. A patch carries the unit's
// whole HTML, so a long list would push everything else off the screen; the
// head names the element and the tail is where a list's last row lands.
const (
	frameHead = 240
	frameTail = 120
)

// endEvent files the event read so far; called under c.mu at the blank line
// that ends it.
func (c *Conn) endEvent() {
	if len(c.pending) == 0 {
		return
	}
	c.events++
	line := c.pending[0]
	if len(c.pending) > 1 {
		line += ": " + strings.Join(c.pending[1:], " | ")
	}
	c.recent = append(c.recent, clip(line))
	if len(c.recent) > recentKept {
		c.recent = c.recent[1:]
	}
	c.pending = c.pending[:0]
}

func clip(s string) string {
	if len(s) <= frameHead+frameTail+40 {
		return s
	}
	head, tail := frameHead, len(s)-frameTail
	for head > 0 && !utf8.RuneStart(s[head]) {
		head--
	}
	for tail < len(s) && !utf8.RuneStart(s[tail]) {
		tail++
	}
	return s[:head] + fmt.Sprintf(" …(%d bytes cut)… ", tail-head) + s[tail:]
}

// arrived describes what the stream carried, for a failure message. read is
// how many frames the failing wait itself consumed: 0 means the frame it wanted,
// if it came at all, was taken by an earlier Await.
func (c *Conn) arrived(read int) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.events == 0 {
		return "no frames arrived on this stream"
	}
	var b strings.Builder
	noun := "frames"
	if c.events == 1 {
		noun = "frame"
	}
	fmt.Fprintf(&b, "%d %s on this stream, %d read by this wait; the last %d:", c.events, noun, read, len(c.recent))
	for i, f := range c.recent {
		fmt.Fprintf(&b, "\n  #%d %s", c.events-len(c.recent)+i+1, f)
	}
	return b.String()
}

// tabSignal and tabFormField mirror via's own wire names for the tab id: the
// signal a @post carries, and the hidden field a PostForm renders.
const (
	tabSignal    = "viatab"
	tabFormField = "_viatab"
)

var tabRE = regexp.MustCompile(`"` + tabSignal + `":"([^"]+)"`)

// Connect opens the per-tab SSE stream and reads the connect-time signals frame
// that carries the tab id, so the returned Conn is ready to route actions.
func (a *App) Connect() *Conn { return a.connect("/", "{}") }

// ConnectWith is Connect with a hand-written connect body — the page signals a
// real client ships with its @post('/_via/sse'). Tests that need to prove what
// a forged body can and cannot do reach for this; everything else wants
// Connect.
func (a *App) ConnectWith(body string) *Conn { return a.connect("/", body) }

// ConnectAt is Connect on the page mounted at path, for a Router with several
// pages: app.ConnectAt("/room/lobby").
func (a *App) ConnectAt(path string) *Conn { return a.connect(path, "{}") }

func (a *App) connect(path, body string) *Conn {
	a.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	sse := strings.TrimSuffix(path, "/") + "/_via/sse"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.srv.URL+sse, strings.NewReader(body))
	if err != nil {
		cancel()
		a.t.Fatalf("vt.App.Connect: build request: %v", err)
	}
	// The stream connect is a POST carrying the page signals as a body. A real
	// same-origin browser fetch sends Sec-Fetch-Site; the origin floor requires
	// it (or a trusted Origin), so mimic the browser here.
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := a.srv.Client().Do(req)
	if err != nil {
		cancel()
		a.t.Fatalf("vt.App.Connect: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		cancel()
		resp.Body.Close()
		a.t.Fatalf("vt.App.Connect: status %d", resp.StatusCode)
	}

	frames := make(chan string, 256)
	c := &Conn{t: a.t, app: a, frames: frames, cancel: cancel, closed: make(chan struct{})}
	go c.read(ctx, resp, frames)

	a.t.Cleanup(c.Close)
	c.tabID = c.awaitTab()
	return c
}

// read scans the stream into c's events and element patches, and forwards
// each line on frames until ctx ends.
func (c *Conn) read(ctx context.Context, resp *http.Response, frames chan<- string) {
	defer close(frames)
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	// A patch carries the unit's whole HTML on one line, past the default 64 KB.
	sc.Buffer(nil, maxFrameLine)
	defer c.endRead(sc)
	inElements := false
	for sc.Scan() {
		line := sc.Text()
		c.mu.Lock()
		if line == "" {
			c.endEvent()
		} else {
			c.pending = append(c.pending, strings.TrimPrefix(strings.TrimPrefix(line, "event: "), "data: "))
		}
		c.mu.Unlock()
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
}

// endRead records the scan error before close(closed), so AwaitClose never
// observes the channel shut with the error not yet stored.
func (c *Conn) endRead(sc *bufio.Scanner) {
	c.mu.Lock()
	c.readErr = sc.Err()
	c.endEvent()
	c.mu.Unlock()
	close(c.closed)
}

// TabID is the credential a live action must carry to route to this
// connection; Action.Over splices it in for you.
func (c *Conn) TabID() string { return c.tabID }

// find returns the currently-rendered URL of one of targets — read off the
// latest datastar-patch-elements frame that carries one, the same markup a
// browser's DOM would hold at this point, not a separate plain GET's render.
// Until a push carries it (the first action after Connect, say), the caller
// falls back to the page's initial GET, which is what a browser would still
// show.
func (c *Conn) find(targets []actionid.Target, query string) (string, bool) {
	c.mu.Lock()
	frames := append([][]byte(nil), c.elements...)
	c.mu.Unlock()
	for i := len(frames) - 1; i >= 0; i-- {
		if u, ok := findActionURL(frames[i], targets, query); ok {
			return u, true
		}
	}
	return "", false
}

// Peek takes the next buffered frame without blocking. Like Await it consumes
// the frame: a later Await never sees a line Peek returned. It lets a test
// assert something has not happened yet without letting time advance further
// to find out — under synctest, a blocking Await would wait for whatever
// eventually arrives, defeating a "not yet" claim at a specific instant.
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
//
// Each line is read once: a line an earlier Await skipped past or matched is
// gone. A failure prints how many frames the stream carried, how many this
// Await read, and the last three, each cut to a few hundred bytes.
func (c *Conn) Await(needle string) string {
	c.t.Helper()
	deadline := time.After(awaitTimeout)
	read := 0
	for {
		select {
		case <-deadline:
			c.t.Fatalf("vt.Conn.Await: timed out after %v waiting for %q. %s", awaitTimeout, needle, c.arrived(read))
		case line, ok := <-c.frames:
			if !ok {
				c.t.Fatalf("vt.Conn.Await: stream closed before %q arrived. %s", needle, c.arrived(read))
			}
			if strings.HasPrefix(line, "event:") {
				read++
			}
			if strings.Contains(line, needle) {
				return line
			}
		}
	}
}

// AwaitClose blocks until the server ends the stream and returns the error that
// ended it — nil when the response terminated cleanly, which is what a graceful
// shutdown owes the client; a non-nil error means the client saw a truncated
// stream. Fails the test if the stream is still open after 2s.
func (c *Conn) AwaitClose() error {
	c.t.Helper()
	select {
	case <-c.closed:
	case <-time.After(awaitTimeout):
		c.t.Fatalf("vt.Conn.AwaitClose: the stream was still open after %v", awaitTimeout)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readErr
}

// Close cancels the stream. Idempotent.
func (c *Conn) Close() { c.cancel() }

func (c *Conn) awaitTab() string {
	c.t.Helper()
	deadline := time.After(awaitTimeout)
	read := 0
	for {
		select {
		case <-deadline:
			c.t.Fatal("vt.App.Connect: no tab-id frame arrived. " + c.arrived(read))
		case line, ok := <-c.frames:
			if !ok {
				c.t.Fatal("vt.App.Connect: stream closed before the tab-id frame. " + c.arrived(read))
			}
			if strings.HasPrefix(line, "event:") {
				read++
			}
			if m := tabRE.FindStringSubmatch(line); m != nil {
				return m[1]
			}
		}
	}
}
