package via_test

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-via/via/internal/viaold"
	"github.com/go-via/via/internal/viaold/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sseTimeout = 3 * time.Second

// clientFor returns a per-server http.Client with a cookie jar so the session
// cookie set on the first page GET flows into subsequent action/SSE calls.
// Tests use unique httptest.Server instances, so jar state is isolated per test.
var testClientJars sync.Map // server URL -> *http.Client

func clientFor(serverURL string) *http.Client {
	if v, ok := testClientJars.Load(serverURL); ok {
		return v.(*http.Client)
	}
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	actual, _ := testClientJars.LoadOrStore(serverURL, c)
	return actual.(*http.Client)
}

// --- testClient ---

// testClient drives integration tests against a via app. It wraps a test
// server and provides methods for extracting IDs, opening SSE streams,
// triggering actions, and reading events.
type testClient struct {
	t       *testing.T
	server  *httptest.Server
	route   string
	body    string
	ctxID   string
	cookies []*http.Cookie
	stream  *bufio.Scanner
	cancel  context.CancelFunc
}

// newTestClient creates an app with a single page and returns a connected-ready client.
func newTestClient(t *testing.T, route string, initFn func(*via.Cmp)) *testClient {
	t.Helper()
	server := newTestApp(t, route, initFn)
	return newTestClientFromServer(t, server, route)
}

// newTestClientFromServer wraps an existing server. Use when the app needs
// custom setup (extra handlers, options, etc.).
func newTestClientFromServer(t *testing.T, server *httptest.Server, route string) *testClient {
	t.Helper()
	tc := &testClient{t: t, server: server, route: route}
	resp, err := http.Get(server.URL + route)
	require.NoError(t, err)
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	resp.Body.Close()
	tc.cookies = resp.Cookies()
	tc.body = html.UnescapeString(string(raw))
	tc.ctxID = extractCtxID(t, tc.body)
	return tc
}

// withSession re-fetches the page with session cookies, enabling
// session-aware actions and SSE connections.
func (tc *testClient) withSession() *testClient {
	tc.t.Helper()
	resp, err := http.Get(tc.server.URL + tc.route)
	require.NoError(tc.t, err)
	resp.Body.Close()
	tc.cookies = resp.Cookies()
	tc.body = getPageBodyWithCookies(tc.t, tc.server, tc.route, tc.cookies)
	tc.ctxID = extractCtxID(tc.t, tc.body)
	return tc
}

// connect opens an SSE stream and waits for it to be ready.
func (tc *testClient) connect() *testClient {
	tc.t.Helper()
	if tc.cookies != nil {
		tc.stream, tc.cancel = connectSSEWithCookies(tc.t, tc.server, tc.ctxID, tc.cookies)
	} else {
		tc.stream, tc.cancel = connectSSE(tc.t, tc.server, tc.ctxID)
	}
	tc.t.Cleanup(func() { tc.cancel() })
	time.Sleep(20 * time.Millisecond)
	return tc
}

func (tc *testClient) actionID() string    { return extractActionID(tc.t, tc.body) }
func (tc *testClient) actionIDs() []string { return extractActionIDs(tc.t, tc.body) }
func (tc *testClient) signalID() string    { return extractSignalID(tc.t, tc.body) }

// fire triggers an action by ID.
func (tc *testClient) fire(actionID string) {
	tc.t.Helper()
	if tc.cookies != nil {
		triggerActionWithCookies(tc.t, tc.server.URL, tc.ctxID, actionID, tc.cookies)
	} else {
		triggerAction(tc.t, tc.server.URL, tc.ctxID, actionID)
	}
}

// fireWithSignal triggers an action with a string signal value.
func (tc *testClient) fireWithSignal(actionID, sigID, sigValue string) {
	tc.t.Helper()
	triggerActionWithSignal(tc.t, tc.server.URL, tc.ctxID, actionID, sigID, sigValue)
}

// fireWithValue triggers an action with a typed signal value (numbers, etc.).
func (tc *testClient) fireWithValue(actionID, sigID string, value any) {
	tc.t.Helper()
	postSignal(tc.t, tc.server.URL, tc.ctxID, actionID, sigID, value)
}

// readEvent reads the next SSE event, failing on timeout.
func (tc *testClient) readEvent() sseEvent {
	tc.t.Helper()
	return readSSEEvent(tc.t, tc.stream, sseTimeout)
}

// tryReadEvent reads the next SSE event with a custom timeout.
// Returns (true, event) on success, (false, zero) on timeout.
func (tc *testClient) tryReadEvent(timeout time.Duration) (bool, sseEvent) {
	tc.t.Helper()
	return tryReadEvent(tc.t, tc.stream, timeout)
}

// --- App and page helpers ---

func newTestApp(t *testing.T, route string, initFn func(*via.Cmp)) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))
	v.Page(route, initFn)
	t.Cleanup(server.Close)
	return server
}

func getPageBody(t *testing.T, server *httptest.Server, route string) string {
	t.Helper()
	resp, err := clientFor(server.URL).Get(server.URL + route)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return html.UnescapeString(string(body))
}

func getPageBodyWithCookies(t *testing.T, server *httptest.Server, route string, cookies []*http.Cookie) string {
	t.Helper()
	req, _ := http.NewRequest("GET", server.URL+route, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return html.UnescapeString(string(body))
}

// --- ID extraction ---

func extractCtxID(t *testing.T, body string) string {
	t.Helper()
	const marker = `"via_tab":"`
	idx := strings.Index(body, marker)
	require.NotEqual(t, -1, idx, "via_tab not found in page body")
	start := idx + len(marker)
	end := strings.Index(body[start:], `"`)
	require.NotEqual(t, -1, end, "via_tab value not terminated")
	return body[start : start+end]
}

func extractActionID(t *testing.T, body string) string {
	t.Helper()
	const prefix = "/_action/"
	idx := strings.Index(body, prefix)
	require.NotEqual(t, -1, idx, "action URL not found in page body")
	start := idx + len(prefix)
	end := strings.IndexAny(body[start:], "'&#\"")
	require.NotEqual(t, -1, end)
	return body[start : start+end]
}

func extractActionIDs(t *testing.T, body string) []string {
	t.Helper()
	var ids []string
	const prefix = "/_action/"
	searchStart := 0
	for {
		idx := strings.Index(body[searchStart:], prefix)
		if idx == -1 {
			break
		}
		idx += searchStart
		start := idx + len(prefix)
		end := strings.IndexAny(body[start:], "'&#\"")
		if end == -1 {
			break
		}
		ids = append(ids, body[start:start+end])
		searchStart = start + end
	}
	require.NotEmpty(t, ids, "no action IDs found in page body")
	return ids
}

func extractSignalID(t *testing.T, body string) string {
	t.Helper()
	markers := []string{`data-bind="`, `data-text="`, `data-show="`}
	for _, marker := range markers {
		idx := strings.Index(body, marker)
		if idx != -1 {
			start := idx + len(marker)
			end := strings.Index(body[start:], `"`)
			require.NotEqual(t, -1, end, "signal ID not terminated")
			return body[start : start+end]
		}
	}
	t.Fatal("signal ID not found in page body")
	return ""
}

// --- SSE ---

type sseEvent struct {
	eventType string
	data      string
}

func connectSSE(t *testing.T, server *httptest.Server, ctxID string) (*bufio.Scanner, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	sigsJSON := `{"via_tab":"` + ctxID + `"}`
	req, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/_sse?datastar="+sigsJSON, nil)
	require.NoError(t, err)
	resp, err := clientFor(server.URL).Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return bufio.NewScanner(resp.Body), cancel
}

func connectSSEWithCookies(t *testing.T, server *httptest.Server, ctxID string, cookies []*http.Cookie) (*bufio.Scanner, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	sigsJSON := `{"via_tab":"` + ctxID + `"}`
	req, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/_sse?datastar="+sigsJSON, nil)
	require.NoError(t, err)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return bufio.NewScanner(resp.Body), cancel
}

// tryReadEvent reads the next SSE event with a timeout.
// Returns (true, event) on success, (false, zero) on timeout.
func tryReadEvent(t *testing.T, scanner *bufio.Scanner, timeout time.Duration) (bool, sseEvent) {
	t.Helper()
	resultCh := make(chan sseEvent, 1)
	go func() {
		var ev sseEvent
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "event:") {
				ev.eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			} else if strings.HasPrefix(line, "data:") {
				d := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if ev.data == "" {
					ev.data = d
				} else {
					ev.data += "\n" + d
				}
			} else if line == "" && ev.eventType != "" {
				resultCh <- ev
				return
			}
		}
	}()
	select {
	case ev := <-resultCh:
		return true, ev
	case <-time.After(timeout):
		return false, sseEvent{}
	}
}

// readSSEEvent reads the next SSE event, failing the test on timeout.
func readSSEEvent(t *testing.T, scanner *bufio.Scanner, timeout time.Duration) sseEvent {
	t.Helper()
	ok, ev := tryReadEvent(t, scanner, timeout)
	require.True(t, ok, "timed out waiting for SSE event")
	return ev
}

// --- Action triggers ---

func triggerAction(t *testing.T, serverURL, ctxID, actionID string) {
	t.Helper()
	sigsJSON := `{"via_tab":"` + ctxID + `"}`
	resp, err := clientFor(serverURL).Post(serverURL+"/_action/"+actionID, "application/json", strings.NewReader(sigsJSON))
	require.NoError(t, err)
	resp.Body.Close()
}

func triggerActionWithCookies(t *testing.T, serverURL, ctxID, actionID string, cookies []*http.Cookie) {
	t.Helper()
	sigsJSON := `{"via_tab":"` + ctxID + `"}`
	req, _ := http.NewRequest("POST", serverURL+"/_action/"+actionID, strings.NewReader(sigsJSON))
	req.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
}

func triggerActionWithSignal(t *testing.T, serverURL, ctxID, actionID, sigID, sigValue string) {
	t.Helper()
	sigsJSON := `{"via_tab":"` + ctxID + `","` + sigID + `":"` + sigValue + `"}`
	resp, err := clientFor(serverURL).Post(serverURL+"/_action/"+actionID, "application/json", strings.NewReader(sigsJSON))
	require.NoError(t, err)
	resp.Body.Close()
}

func postSignal(t *testing.T, serverURL, ctxID, actionID, sigID string, value any) {
	t.Helper()
	sigsJSON := fmt.Sprintf(`{"via_tab":%q,%q:%v}`, ctxID, sigID, value)
	resp, err := clientFor(serverURL).Post(serverURL+"/_action/"+actionID, "application/json", strings.NewReader(sigsJSON))
	require.NoError(t, err)
	resp.Body.Close()
}

// --- Assertions ---

// assertEvent asserts the event type and that the data contains all given substrings.
func assertEvent(t *testing.T, ev sseEvent, eventType string, contains ...string) {
	t.Helper()
	assert.Equal(t, eventType, ev.eventType)
	for _, s := range contains {
		assert.Contains(t, ev.data, s)
	}
}

// awaitChan reads from ch with a timeout, failing the test if the timeout expires.
func awaitChan[T any](t *testing.T, ch <-chan T, timeout time.Duration) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(timeout):
		t.Fatal("timed out waiting on channel")
		var zero T
		return zero
	}
}

// --- Cookie helpers ---

func collectCookies(t *testing.T, _ string, cookies []*http.Cookie) []*http.Cookie {
	t.Helper()
	return cookies
}

func mergeCookies(existing []*http.Cookie, fresh []*http.Cookie) []*http.Cookie {
	merged := make(map[string]*http.Cookie)
	for _, c := range existing {
		merged[c.Name] = c
	}
	for _, c := range fresh {
		merged[c.Name] = c
	}
	out := make([]*http.Cookie, 0, len(merged))
	for _, c := range merged {
		out = append(out, c)
	}
	return out
}

func addCookies(req *http.Request, cookies []*http.Cookie) {
	for _, c := range cookies {
		req.AddCookie(c)
	}
}

// --- Render and capture helpers ---

func renderH(t *testing.T, node h.H) string {
	t.Helper()
	var buf bytes.Buffer
	err := node.Render(&buf)
	require.NoError(t, err)
	return buf.String()
}

type signalT interface {
	ID() string
	Err() error
	Bind() h.H
	Text() h.H
	Show() h.H
	Ref() string
	Tag(string)
}

func captureSignal(initFn func(cmp *via.Cmp) signalT) signalT {
	v := via.New()
	var sig signalT
	v.Page("/", func(cmp *via.Cmp) {
		sig = initFn(cmp)
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	return sig
}

type actionT interface {
	OnClick(options ...via.ActionTriggerOption) h.H
	OnChange(options ...via.ActionTriggerOption) h.H
	OnKeyDown(key string, options ...via.ActionTriggerOption) h.H
}

func captureAction(initFn func(cmp *via.Cmp) actionT) actionT {
	v := via.New()
	var act actionT
	v.Page("/", func(cmp *via.Cmp) {
		act = initFn(cmp)
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	return act
}
