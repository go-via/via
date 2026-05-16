package via_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	neturl "net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/require"
)

// tabRawRE mirrors test.tabRE — the raw fallback tests need their own
// jar/client pair, so they can't go through viatest.NewClient.
var tabRawRE = regexp.MustCompile(`&#34;via_tab&#34;:&#34;([^"&]+)&#34;`)

type formPage struct {
	Email    via.Signal[string]
	Password via.Signal[string]
	Age      via.Signal[int]
	Result   via.State[string]
}

type loginForm struct {
	Email    string `form:"email"`
	Password string `form:"password"`
	Age      int    `form:"age"`
}

func (p *formPage) Submit(ctx *via.Ctx) error {
	var f loginForm
	via.DecodeForm(ctx, &f)
	p.Result.Set(ctx, f.Email+"|"+f.Password+"|"+strings.Repeat("*", f.Age))
	return nil
}

func (p *formPage) View(ctx *via.Ctx) h.H {
	return h.Div(p.Result.Text())
}

func TestDecodeForm_readsSignalsIntoTaggedStruct(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[formPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, tc.Action("Submit").
		WithSignal("email", "alice@example.com").
		WithSignal("password", "secret").
		WithSignal("age", 3).Fire())
	viatest.AwaitFrame(t, frames, 2*time.Second, "alice@example.com|secret|***")
}

type formNoTag struct {
	UserName via.Signal[string]
	Captured via.State[string]
}

type lazyForm struct {
	UserName string // no tag — uses lowercased field name "userName"
}

func (p *formNoTag) Submit(ctx *via.Ctx) error {
	var f lazyForm
	via.DecodeForm(ctx, &f)
	p.Captured.Set(ctx, f.UserName)
	return nil
}

func (p *formNoTag) View(ctx *via.Ctx) h.H { return h.Div(p.Captured.Text()) }

func TestDecodeForm_defaultsKeyToLowercasedFieldName(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[formNoTag](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, tc.Action("Submit").
		WithSignal("userName", "bob").Fire())
	viatest.AwaitFrame(t, frames, 2*time.Second, ">bob<")
}

// Fallback chain — query string and unparseable values.

type formFallbackPage struct {
	Captured via.State[string]
}

type fallbackForm struct {
	City string `form:"city"`
	Age  int    `form:"age"`
	On   bool   `form:"on"`
}

func (p *formFallbackPage) Submit(ctx *via.Ctx) error {
	var f fallbackForm
	via.DecodeForm(ctx, &f)
	on := "false"
	if f.On {
		on = "true"
	}
	p.Captured.Set(ctx, f.City+"|"+strings.Repeat("y", f.Age)+"|"+on)
	return nil
}

func (p *formFallbackPage) View(ctx *via.Ctx) h.H { return h.Div(p.Captured.Text()) }

// rawTabClient does a GET to acquire a tab id + session cookie on a
// shared jar, then exposes a Fire that drives /_action/{name} with
// optional URL query — used to exercise DecodeForm's fallback chain.
type rawTabClient struct {
	t      *testing.T
	server *httptest.Server
	httpc  *http.Client
	tabID  string
}

func newRawTabClient(t *testing.T, server *httptest.Server, path string) *rawTabClient {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	httpc := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	resp, err := httpc.Get(server.URL + path)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m := tabRawRE.FindStringSubmatch(string(body))
	require.NotEmpty(t, m, "tab id not found in rendered body")
	return &rawTabClient{t: t, server: server, httpc: httpc, tabID: m[1]}
}

func (c *rawTabClient) FireWithQuery(name, query string, signals map[string]any) int {
	c.t.Helper()
	body := map[string]any{"via_tab": c.tabID}
	for k, v := range signals {
		body[k] = v
	}
	buf, _ := json.Marshal(body)
	url := c.server.URL + "/_action/" + name
	if query != "" {
		url += "?" + query
	}
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpc.Do(req)
	require.NoError(c.t, err)
	resp.Body.Close()
	return resp.StatusCode
}

func (c *rawTabClient) OpenSSE() (<-chan string, func()) {
	c.t.Helper()
	out := make(chan string, 16)
	ctx, cancel := context.WithCancel(context.Background())
	body, _ := json.Marshal(map[string]any{"via_tab": c.tabID})
	sseURL := c.server.URL + "/_sse?datastar=" + neturl.QueryEscape(string(body))
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, sseURL, nil)
	httpc := &http.Client{Jar: c.httpc.Jar}
	resp, err := httpc.Do(req)
	require.NoError(c.t, err)
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
	return out, func() { cancel(); resp.Body.Close() }
}

func TestDecodeForm_fallsBackToURLQueryWhenSignalAbsent(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[formFallbackPage](app, "/")
	defer server.Close()

	c := newRawTabClient(t, server, "/")
	frames, cancel := c.OpenSSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, c.FireWithQuery("Submit",
		"city=Lisbon&age=3&on=true",
		// No signal payload — every value must come from the URL query.
		map[string]any{},
	))
	viatest.AwaitFrame(t, frames, 2*time.Second, "Lisbon|yyy|true")
}

func TestDecodeForm_signalPayloadWinsOverQuery(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[formFallbackPage](app, "/")
	defer server.Close()

	c := newRawTabClient(t, server, "/")
	frames, cancel := c.OpenSSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	// Same key in both places — the signal payload must take precedence.
	require.Equal(t, 200, c.FireWithQuery("Submit",
		"city=FromQuery", map[string]any{"city": "FromSignal"},
	))
	viatest.AwaitFrame(t, frames, 2*time.Second, "FromSignal||false")
}

func TestDecodeForm_unparseableValueLeavesFieldZero(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[formFallbackPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	// Age can't parse as int; bool can't parse from "maybe". String
	// passes through. The handler must not 500, and the int/bool fields
	// must stay at their zero values.
	require.Equal(t, 200, tc.Action("Submit").
		WithSignal("city", "Porto").
		WithSignal("age", "not-an-int").
		WithSignal("on", "maybe").
		Fire())
	viatest.AwaitFrame(t, frames, 2*time.Second, "Porto||false")
}

// Defensive shapes — nil dst, non-struct dst, unexported fields.

func TestDecodeForm_nilDstIsNoOp(t *testing.T) {
	t.Parallel()
	page := &formFallbackPage{}
	ctx := viatest.NewCtx(t, page)
	// Must not panic.
	var dst *fallbackForm
	via.DecodeForm(ctx, dst)
}

func TestDecodeForm_nonStructDstIsNoOp(t *testing.T) {
	t.Parallel()
	page := &formFallbackPage{}
	ctx := viatest.NewCtx(t, page)
	// Must not panic — non-struct pointer is silently ignored.
	var n int
	via.DecodeForm(ctx, &n)
	require.Equal(t, 0, n)
}

type formMixedFields struct {
	Visible  string `form:"visible"`
	hidden   string //nolint:unused // intentionally unexported for the test
	NoTag    string
	NotFound string `form:"missing"`
}

func TestDecodeForm_skipsUnexportedFieldsAndLeavesMissingKeysZero(t *testing.T) {
	t.Parallel()
	page := &formFallbackPage{}
	ctx := viatest.NewCtx(t, page)
	// No request and no signal payload — every field stays zero, no
	// panic on the unexported field.
	var dst formMixedFields
	via.DecodeForm(ctx, &dst)
	require.Equal(t, formMixedFields{}, dst)
}
