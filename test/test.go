// Package test holds testing helpers for via compositions. It lets tests
// drive a Composition by HTTP without parsing HTML, by name-addressing
// actions and signals through the descriptor.
//
//	app := via.New()
//	via.Mount[Counter](app, "/")
//	tc := test.NewClient(t, app, "/")
//	tc.Action("Inc").Fire()
//	tc.Signal("step").Set(3)
//	require.Equal(t, "3", tc.Text())   // last rendered fragment innerText
package test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-via/via"
)

// NewCtx returns a *via.Ctx wired to the given composition, suitable
// for unit-testing action methods directly without spinning up an
// HTTP server. Signal/State handles are bound (Get/Set work), the
// session is empty, and the context's Done() channel is open.
//
//	c := &Counter{}
//	ctx := test.NewCtx(t, c)
//	require.NoError(t, c.Inc(ctx))
//	assert.Equal(t, 1, c.Hits.Get(ctx))
//
// Use this for unit-testing logic where a full HTTP round-trip would
// be wasteful. For end-to-end tests use NewClient against an
// httptest.Server.
func NewCtx[T any](t testing.TB, c *T) *via.Ctx {
	t.Helper()
	return via.NewBoundCtx(c)
}

// Client drives a mounted Composition over HTTP for tests.
type Client struct {
	t        testing.TB
	server   *httptest.Server
	tabID    string
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
	httpc := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	resp, err := httpc.Get(server.URL + path)
	if err != nil {
		t.Fatalf("test.NewClient: GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	tab := tabIDFrom(string(body))
	if tab == "" {
		t.Fatalf("test.NewClient: no tab id in body of %s", path)
	}
	return &Client{t: t, server: server, tabID: tab, jar: jar, httpc: httpc, lastBody: string(body)}
}

// TabID returns the active tab id.
func (c *Client) TabID() string { return c.tabID }

// HTML returns the most recently fetched page body.
func (c *Client) HTML() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastBody
}

// Action returns a handle that fires the named action.
func (c *Client) Action(name string) *ActionCall {
	return &ActionCall{client: c, name: name}
}

// ActionCall is a builder for action invocations.
type ActionCall struct {
	client  *Client
	name    string
	signals map[string]any
}

// WithSignal adds a signal value to send with the action POST.
func (a *ActionCall) WithSignal(name string, value any) *ActionCall {
	if a.signals == nil {
		a.signals = map[string]any{}
	}
	a.signals[name] = value
	return a
}

// Fire issues POST /_action/{name} and returns the response status code.
func (a *ActionCall) Fire() int {
	a.client.t.Helper()
	body := map[string]any{"via_tab": a.client.tabID}
	for k, v := range a.signals {
		body[k] = v
	}
	buf, _ := json.Marshal(body)
	resp, err := a.client.httpc.Post(
		a.client.server.URL+"/_action/"+a.name,
		"application/json",
		strings.NewReader(string(buf)),
	)
	if err != nil {
		a.client.t.Fatalf("test.Action(%s).Fire: %v", a.name, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// Signal returns a handle that sets the named signal via an action POST.
type SignalRef struct {
	client *Client
	name   string
}

// Signal returns a handle for the named server-side signal.
func (c *Client) Signal(name string) *SignalRef {
	return &SignalRef{client: c, name: name}
}

// Set fires an empty action that updates the signal value. Useful only when
// an action exists that consumes the signal; otherwise call Action(...).
func (s *SignalRef) Set(v any) {
	s.client.t.Helper()
	body, _ := json.Marshal(map[string]any{
		"via_tab": s.client.tabID,
		s.name:    v,
	})
	resp, err := s.client.httpc.Post(
		s.client.server.URL+"/_action/__signal_set__",
		"application/json",
		strings.NewReader(string(body)),
	)
	if err == nil {
		resp.Body.Close()
	}
}

// SSE opens an SSE stream and returns a cancel func and a channel of frames.
// Use only when you must observe live patch frames.
func (c *Client) SSE(t testing.TB) (frames <-chan string, cancel func()) {
	t.Helper()
	out := make(chan string, 16)
	ctx, cancelF := context.WithCancel(context.Background())
	url := c.server.URL + "/_sse?datastar=" + sseQueryParam(c.tabID)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := c.httpc.Do(req)
	if err != nil {
		cancelF()
		close(out)
		t.Fatalf("test.SSE: %v", err)
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
	return out, func() { cancelF(); resp.Body.Close() }
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

func sseQueryParam(tabID string) string {
	body, _ := json.Marshal(map[string]any{"via_tab": tabID})
	return strings.NewReplacer(
		"\"", "%22", "{", "%7B", "}", "%7D",
		":", "%3A", ",", "%2C", "/", "%2F",
	).Replace(string(body))
}
