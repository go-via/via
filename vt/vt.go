// Package vt (via test) holds testing helpers for via compositions. It
// lets tests drive a Composition by HTTP without parsing HTML, by
// name-addressing actions and signals through the descriptor.
//
//	app := via.New()
//	via.Mount[Counter](app, "/")
//	srv := vt.Serve(t, app)
//	tc := vt.NewClient(t, srv, "/")
//	tc.Action(p.Inc).WithSignal("step", 3).Fire()
//	require.Contains(t, tc.Reload(), ">3<")
package vt

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/internal/spec"
)

// Serve starts an httptest.Server bound to app and registers its shutdown
// with t.Cleanup, so a test gets a live URL in one line:
//
//	app := via.New()
//	via.Mount[Counter](app, "/")
//	srv := vt.Serve(t, app)
//	tc := vt.NewClient(t, srv, "/")
//
// app is its own http.Handler, so the server dispatches through App.ServeHTTP
// on every request — routes mounted before or after Serve are both reachable.
func Serve(t testing.TB, app *via.App) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return srv
}

// Client drives a mounted Composition over HTTP for tests.
type Client struct {
	t        testing.TB
	server   *httptest.Server
	tabID    string
	path     string // captured at NewClient so Reload can re-fetch
	jar      http.CookieJar
	httpc    *http.Client
	mu       sync.Mutex
	lastBody string
}

// NewClient performs a GET on path, picks up the rendered tab id, and is
// ready to drive actions and signal updates against that tab.
func NewClient(t testing.TB, server *httptest.Server, path string) *Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	httpc := &http.Client{Jar: jar, Timeout: 5 * time.Second, Transport: &http.Transport{}}
	resp, err := httpc.Get(server.URL + path)
	if err != nil {
		t.Fatalf("vt.NewClient: GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	tab := tabIDFrom(string(body))
	if tab == "" {
		t.Fatalf("vt.NewClient: no tab id in body of %s", path)
	}
	return &Client{t: t, server: server, tabID: tab, path: path, jar: jar, httpc: httpc, lastBody: string(body)}
}

// TabID returns the active tab id.
func (c *Client) TabID() string { return c.tabID }

// Fork opens a second tab against path that shares this client's cookie
// jar, so both tabs land on the same session — the only way to drive
// StateSess behavior that spans tabs.
func (c *Client) Fork(path string) *Client {
	c.t.Helper()
	httpc := &http.Client{Jar: c.jar, Timeout: 5 * time.Second, Transport: &http.Transport{}}
	resp, err := httpc.Get(c.server.URL + path)
	if err != nil {
		c.t.Fatalf("vt.Client.Fork: GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	tab := tabIDFrom(string(body))
	if tab == "" {
		c.t.Fatalf("vt.Client.Fork: no tab id in body of %s", path)
	}
	return &Client{t: c.t, server: c.server, tabID: tab, path: path, jar: c.jar, httpc: httpc, lastBody: string(body)}
}

// HTML returns the most recently fetched page body.
func (c *Client) HTML() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastBody
}

// Reload re-fetches the currently mounted page, refreshes lastBody, and
// returns the new HTML. Use after firing actions to assert on the
// re-rendered body — server-side state changes only show up in HTML on
// a fresh GET (or via the SSE stream, but that's heavier to wire up).
//
//	tc.Action("Bump").Fire()
//	body := tc.Reload()
//	assert.Contains(t, body, ">3<")
//
// Note: Reload assigns a *new* tab id (each GET registers a fresh Ctx).
// If you need to assert against the original tab, capture HTML() first.
func (c *Client) Reload() string {
	c.t.Helper()
	resp, err := c.httpc.Get(c.server.URL + c.path)
	if err != nil {
		c.t.Fatalf("vt.Client.Reload: GET %s: %v", c.path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	c.mu.Lock()
	c.lastBody = string(body)
	c.tabID = tabIDFrom(c.lastBody)
	c.mu.Unlock()
	return c.lastBody
}

// Action returns a handle that fires an action. The target may be either
// the action's name as a string, or a bound method value whose method
// name is resolved via the runtime — the typed form gives the test
// compile-time protection against typos:
//
//	tc.Action("Bump").Fire()         // string form
//	tc.Action(p.Bump).Fire()         // typed form (preferred)
func (c *Client) Action(target any) *ActionCall {
	name, ok := target.(string)
	if !ok {
		name = spec.MethodName(target)
	}
	return &ActionCall{client: c, name: name}
}

// ActionCall is a builder for action invocations.
type ActionCall struct {
	client  *Client
	name    string
	signals map[string]any
	files   []actionFile
}

type actionFile struct {
	field, filename string
	body            []byte
}

// WithSignal adds a signal value to send with the action POST.
//
// A `_`-prefixed name is a Datastar client-only (local) signal, which a real
// browser never POSTs to the server — sending one here reproduces behavior
// that cannot happen in the browser, so WithSignal logs a (non-fatal) warning.
func (a *ActionCall) WithSignal(name string, value any) *ActionCall {
	if strings.HasPrefix(name, "_") {
		a.client.t.Helper()
		a.client.t.Logf("vt.WithSignal(%q): a `_`-prefixed local signal is never "+
			"sent to the server by a real browser; this test cannot reproduce "+
			"client-side behavior for it", name)
	}
	if a.signals == nil {
		a.signals = map[string]any{}
	}
	a.signals[name] = value
	return a
}

// WithFile attaches a file part to the action POST. Adding any file
// switches the request from JSON to multipart/form-data; signals added
// via WithSignal ride along as text fields. Repeat calls add multiple
// files.
//
//	tc.Action(p.Upload).
//	    WithFile("avatar", "me.png", pngBytes).
//	    WithSignal("note", "from CLI").
//	    Fire()
func (a *ActionCall) WithFile(field, filename string, body []byte) *ActionCall {
	a.files = append(a.files, actionFile{field, filename, body})
	return a
}

// Fire issues POST /_action/{name} and returns the response status code.
func (a *ActionCall) Fire() int {
	a.client.t.Helper()
	if len(a.files) > 0 {
		return a.fireMultipart()
	}
	body := map[string]any{"via_tab": a.client.tabID}
	maps.Copy(body, a.signals)
	buf, _ := json.Marshal(body)
	resp, err := a.client.httpc.Post(
		a.client.server.URL+"/_action/"+a.name,
		"application/json",
		bytes.NewReader(buf),
	)
	if err != nil {
		a.client.t.Fatalf("vt.Action(%s).Fire: %v", a.name, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func (a *ActionCall) fireMultipart() int {
	a.client.t.Helper()
	// Errors from multipart.Writer methods can't surface here because
	// the backing bytes.Buffer is infallible. Real failures only show
	// up at the http.NewRequest / Do boundary.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("via_tab", a.client.tabID)
	for k, v := range a.signals {
		_ = mw.WriteField(k, scalarToFormValue(v))
	}
	for _, f := range a.files {
		fw, _ := mw.CreateFormFile(f.field, f.filename)
		_, _ = fw.Write(f.body)
	}
	_ = mw.Close()

	req, _ := http.NewRequest("POST", a.client.server.URL+"/_action/"+a.name, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := a.client.httpc.Do(req)
	if err != nil {
		a.client.t.Fatalf("vt.Action(%s).Fire: %v", a.name, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func scalarToFormValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// AwaitFrame waits for every needle to appear on a single SSE frames
// channel, failing the test if any one is missing within timeout.
// Returns the accumulated frame content at the moment the match landed,
// so callers can assert further on what came in alongside.
//
//	frames, cancel := tc.SSE()
//	defer cancel()
//	tc.Action("Bump").Fire()
//	body := vt.AwaitFrame(t, frames, 2*time.Second, "<div>3</div>")
//	assert.NotContains(t, body, "stale")
func AwaitFrame(t testing.TB, frames <-chan string, timeout time.Duration, needles ...string) string {
	t.Helper()
	deadline := time.After(timeout)
	var got strings.Builder
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatalf("SSE closed before seeing %v; got %q", needles, got.String())
				return got.String()
			}
			got.WriteString(f)
			accum := got.String()
			matched := true
			for _, n := range needles {
				if !strings.Contains(accum, n) {
					matched = false
					break
				}
			}
			if matched {
				return accum
			}
		case <-deadline:
			t.Fatalf("did not see %v within %v; got %q", needles, timeout, got.String())
			return got.String()
		}
	}
}

// SSEReady opens an SSE stream and blocks until the server's handshake
// comment (`: ready`) arrives, signalling that the server-side SSE
// goroutine has entered its select loop and is registered to receive
// patch-queue wakeups. Use it in place of the
// `tc.SSE(); time.Sleep(20*time.Millisecond)` idiom — it replaces a
// timing guess with a deterministic wait so the suite doesn't flake on
// busy CI runners. The comment bytes are consumed off the channel so
// downstream `AwaitFrame` calls see only post-handshake traffic. Times
// out after 2s.
func (c *Client) SSEReady() (frames <-chan string, cancel func()) {
	c.t.Helper()
	frames, cancel = c.SSE()
	AwaitFrame(c.t, frames, 2*time.Second, ": ready")
	return frames, cancel
}

// SSE opens an SSE stream and returns a cancel func and a channel of frames.
// Use only when you must observe live patch frames.
//
// The stream uses a separate http.Client with no overall timeout — the
// regular client's Timeout would kill the stream mid-flight. Per-frame
// waits should be bounded with AwaitFrame; cancel with the returned func.
func (c *Client) SSE() (frames <-chan string, cancel func()) {
	c.t.Helper()
	out := make(chan string, 16)
	ctx, cancelF := context.WithCancel(context.Background())
	url := c.server.URL + "/_sse?datastar=" + sseQueryParam(c.tabID)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	sseClient := &http.Client{Jar: c.jar, Transport: &http.Transport{}} // no timeout — SSE is long-lived
	resp, err := sseClient.Do(req)
	if err != nil {
		cancelF()
		close(out)
		c.t.Fatalf("vt.SSE: %v", err)
	}
	go func() {
		defer close(out)
		defer resp.Body.Close()
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				out <- string(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	cancel = func() { cancelF(); resp.Body.Close() }
	// Register cleanup so the reader goroutine + connection don't leak if a
	// test t.Fatals before reaching its `defer cancel()`. cancel is idempotent
	// (context cancel + Body.Close both tolerate a second call).
	c.t.Cleanup(cancel)
	return out, cancel
}

// helpers

// tabRE picks the via_tab id out of the data-signals attribute on the
// rendered <meta>. The id is `<route>_<64-hex>`; the route can contain
// any URL-safe characters (including `/`), so we match the suffix and
// then re-extract the surrounding key.
var tabRE = regexp.MustCompile(`&#34;via_tab&#34;:&#34;([^"&]+)&#34;`)

func tabIDFrom(html string) string {
	m := tabRE.FindStringSubmatch(html)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// TabIDFromHTML extracts the via_tab id from a rendered page's data-signals
// meta (the id is `<route>_<64-hex>`). The HTML escapes the JSON quotes, so
// tests reaching for the tab id directly should use this rather than
// hand-rolling a regex against the escaped form. Returns "" if absent.
func TabIDFromHTML(html string) string { return tabIDFrom(html) }

func sseQueryParam(tabID string) string {
	body, _ := json.Marshal(map[string]any{"via_tab": tabID})
	return url.QueryEscape(string(body))
}
