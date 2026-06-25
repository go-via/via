package via_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-via/via/v2"
	"github.com/go-via/via/v2/h"
	"github.com/go-via/via/v2/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(b)
}

// post issues a POST to the action endpoint with exactly the given headers and
// no defaults, so each test pins precisely the origin signal it intends to send
// (the shared do() helper injects same-origin, which would mask these checks).
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

func sameOrigin() map[string]string { return map[string]string{"Sec-Fetch-Site": "same-origin"} }

// panicComp's action panics, to prove a buggy handler returns 500 and does not
// crash the server.
type panicComp struct{}

func (p *panicComp) Boom(*via.Ctx) { panic("boom") }
func (p *panicComp) View() h.H {
	return h.Div(h.Button(via.OnClick(p.Boom), h.Str("x")))
}

// POST /_via/a/{n} is a state-changing endpoint; a cross-site request must be
// rejected and must not mutate server state, or any page on the web can drive
// the counter (CSRF).
func TestAction_rejectsCrossSiteOriginAndDoesNotMutate(t *testing.T) {
	t.Parallel()
	srv := newCounter(t)

	resp, _ := post(t, srv, "/_via/a/1", "{}", map[string]string{"Sec-Fetch-Site": "cross-site"})
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	_, body := do(t, srv, http.MethodGet, "/", "")
	assert.Contains(t, body, "<h1>0</h1>", "cross-site POST must not have mutated the store")
}

// same-site (a sibling subdomain) is NOT same-origin; treating it as trusted is
// the classic CSRF confusion, so a same-site action request must be rejected.
func TestAction_rejectsSameSiteOrigin(t *testing.T) {
	t.Parallel()
	resp, _ := post(t, newCounter(t), "/_via/a/1", "{}", map[string]string{"Sec-Fetch-Site": "same-site"})
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// A request that carries no origin signal at all (no Sec-Fetch-Site, no Origin)
// proves nothing about where it came from; the floor must fail closed rather
// than silently trust it.
func TestAction_failsClosedWithoutAnyOriginSignal(t *testing.T) {
	t.Parallel()
	resp, _ := post(t, newCounter(t), "/_via/a/1", "{}", nil)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// A legitimate same-origin browser fetch (proven via a matching Origin header
// when Sec-Fetch-Site is absent) must be allowed and must mutate state.
func TestAction_allowsSameOriginViaMatchingOriginHeader(t *testing.T) {
	t.Parallel()
	srv := newCounter(t)
	resp, body := post(t, srv, "/_via/a/1", "{}", map[string]string{"Origin": srv.URL})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, "<h1>1</h1>")
}

// An unbounded request body is a memory-exhaustion DoS; the handler must cap it
// and reject an oversize body with 413 rather than buffering it whole.
func TestAction_rejectsOversizeBody(t *testing.T) {
	t.Parallel()
	big := `{"s0":"` + strings.Repeat("a", 2<<20) + `"}`
	resp, _ := post(t, newCounter(t), "/_via/a/1", big, sameOrigin())
	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
}

// A malformed (non-empty, invalid-JSON) body must fail loudly with 400, not
// silently bind an empty signal set and misroute the action.
func TestAction_rejectsMalformedBody(t *testing.T) {
	t.Parallel()
	resp, _ := post(t, newCounter(t), "/_via/a/1", "{not valid json", sameOrigin())
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// An empty body is the common stateless-action case and must be treated as "no
// signals", not as a malformed-body 400.
func TestAction_treatsEmptyBodyAsNoSignals(t *testing.T) {
	t.Parallel()
	resp, body := post(t, newCounter(t), "/_via/a/1", "", sameOrigin())
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, "<h1>1</h1>")
}

// A panicking action must be contained: the request returns 500 and the server
// keeps serving subsequent requests (no crash, no wedged connection).
func TestAction_panicIsRecoveredAs500AndServerStaysUp(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(panicComp{}))

	resp, _ := post(t, srv, "/_via/a/0", "{}", sameOrigin())
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	resp2, _ := do(t, srv, http.MethodGet, "/", "")
	assert.Equal(t, http.StatusOK, resp2.StatusCode, "server must keep serving after a panicking action")
}

// WithInsecureOrigin is the documented escape hatch for non-browser/dev clients
// that cannot send origin headers; it must actually bypass the floor.
func TestWithInsecureOrigin_allowsCrossSiteForNonBrowserClients(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(counter{count: &store{}}, via.WithInsecureOrigin()))
	resp, body := post(t, srv, "/_via/a/1", "{}", map[string]string{"Sec-Fetch-Site": "cross-site"})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, "<h1>1</h1>")
}

// WithTrustedOrigin allowlists a specific cross-origin embedder; that origin
// must be allowed even when the browser labels the request cross-site.
func TestWithTrustedOrigin_allowsNamedCrossOrigin(t *testing.T) {
	t.Parallel()
	const trusted = "https://trusted.example"
	srv := serve(t, via.Register(counter{count: &store{}}, via.WithTrustedOrigin(trusted)))
	resp, body := post(t, srv, "/_via/a/1", "{}",
		map[string]string{"Origin": trusted, "Sec-Fetch-Site": "cross-site"})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, "<h1>1</h1>")
}

// When Sec-Fetch-Site is absent the floor falls back to matching the Origin
// host against the request Host. That match is case-insensitive and treats an
// explicit default port (:80 for http, :443 for https) as equivalent to none —
// browsers vary in whether they include it, and a mismatch there would reject
// legitimate same-origin requests. These normalization branches need a
// controlled request Host, which vt supplies.
func TestOriginFloor_matchesHostCaseAndDefaultPortInsensitively(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, host, origin string }{
		{"case-insensitive host", "app.example", "http://APP.Example"},
		{"explicit http default port", "app.example", "http://app.example:80"},
		{"explicit https default port", "app.example", "https://app.example:443"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			app := vt.Serve(t, via.Register(counter{count: &store{}}))
			status, body := app.Action(1).Host(c.host).Origin(c.origin).Fire()
			assert.Equal(t, http.StatusOK, status, "a normalized same-origin request must be allowed")
			assert.Contains(t, body, "<h1>1</h1>", "and must mutate server state")
		})
	}
}

// A different host, or the same host on a different (non-default) port, is a
// distinct origin and must be rejected — the floor is the CSRF boundary.
func TestOriginFloor_rejectsDifferentHostOrPort(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, host, origin string }{
		{"different host", "app.example", "http://evil.example"},
		{"different port", "app.example:8080", "http://app.example:9090"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			app := vt.Serve(t, via.Register(counter{count: &store{}}))
			status, _ := app.Action(1).Host(c.host).Origin(c.origin).Fire()
			assert.Equal(t, http.StatusForbidden, status)
		})
	}
}

// On a request that arrived over TLS, an http Origin is a scheme downgrade and
// must be rejected even though the host matches — scheme is part of a web
// origin. A matching https Origin on the same host is allowed. (This only bites
// the Sec-Fetch-Site-absent fallback; modern browsers never send an http Origin
// to their own https document.)
func TestOriginFloor_enforcesSchemeOnTLSRequests(t *testing.T) {
	t.Parallel()
	app := vt.ServeTLS(t, via.Register(counter{count: &store{}}))

	down, _ := app.Action(1).Host("app.example").Origin("http://app.example").Fire()
	assert.Equal(t, http.StatusForbidden, down, "http Origin on a TLS request is a scheme downgrade")

	ok, body := app.Action(1).Host("app.example").Origin("https://app.example").Fire()
	assert.Equal(t, http.StatusOK, ok, "https Origin on a TLS request to the same host matches")
	assert.Contains(t, body, "<h1>1</h1>")
}
