package via_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sseStatus opens the SSE stream (POST /_via/sse) with exactly the given headers
// and returns the status, cancelling immediately so a 200 stream's island
// goroutine tears down.
func sseStatus(t *testing.T, srv *httptest.Server, headers map[string]string) int {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/_via/sse", nil)
	require.NoError(t, err)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	return resp.StatusCode
}

// With WithTrustedOrigin set, origin enforcement is on: the SSE connect opens a
// long-lived server resource (an island goroutine + timers), so like the action
// POST it must fail closed to any request that can't prove a same-origin (or
// allowlisted) source.
func TestSSE_enforcementRejectsCrossSiteOrigin(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(quietIsland{}, via.WithTrustedOrigin("https://embedder.example")))
	assert.Equal(t, http.StatusForbidden,
		sseStatus(t, srv, map[string]string{"Sec-Fetch-Site": "cross-site"}))
}

// Under enforcement, a connect that carries no origin signal at all (no
// Sec-Fetch-Site, no Origin) proves nothing about its source and must fail
// closed, exactly as the action endpoint does.
func TestSSE_enforcementFailsClosedWithoutAnyOriginSignal(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(quietIsland{}, via.WithTrustedOrigin("https://embedder.example")))
	assert.Equal(t, http.StatusForbidden, sseStatus(t, srv, nil))
}

// Under enforcement, a real same-origin browser SSE fetch (Sec-Fetch-Site:
// same-origin) must still connect.
func TestSSE_enforcementAllowsSameOrigin(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(quietIsland{}, via.WithTrustedOrigin("https://embedder.example")))
	assert.Equal(t, http.StatusOK,
		sseStatus(t, srv, map[string]string{"Sec-Fetch-Site": "same-origin"}))
}

// Without WithTrustedOrigin the endpoint is open by default (dev-friendly; the
// per-tab id is the CSRF token): a cross-site connect must succeed.
func TestSSE_defaultAllowsCrossSite(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(quietIsland{}))
	assert.Equal(t, http.StatusOK,
		sseStatus(t, srv, map[string]string{"Sec-Fetch-Site": "cross-site"}))
}

// A known cross-origin embedder allowlisted with WithTrustedOrigin must still be
// able to open the stream, so the floor doesn't break a deliberate embed.
func TestSSE_allowsTrustedCrossOrigin(t *testing.T) {
	t.Parallel()
	const embedder = "https://embedder.example"
	srv := serve(t, via.Register(quietIsland{}, via.WithTrustedOrigin(embedder)))
	assert.Equal(t, http.StatusOK,
		sseStatus(t, srv, map[string]string{"Origin": embedder, "Sec-Fetch-Site": "cross-site"}))
}

// Each live stream holds an island goroutine + timers; left uncapped, a client
// can open them without bound and exhaust the server. A connect past the cap
// must be refused (503) rather than admitted, so the resource ceiling holds.
func TestSSE_overTheConnectionCapIsRefused(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(quietIsland{}, via.WithMaxSSEConnections(1)))

	_, release := openStream(t, srv) // takes the only slot; asserts it connected (200)
	defer release()

	assert.Equal(t, http.StatusServiceUnavailable, sseStatus(t, srv, sameOrigin()),
		"a connect past the cap must be refused with 503")
}

// The cap counts CONCURRENT streams, not lifetime connects: when a stream closes
// its slot must free so a later client can connect. A cap that never decremented
// would wedge the app at its limit forever.
func TestSSE_disconnectFreesACapSlot(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(quietIsland{}, via.WithMaxSSEConnections(1)))

	_, release := openStream(t, srv)
	require.Equal(t, http.StatusServiceUnavailable, sseStatus(t, srv, sameOrigin()),
		"precondition: the single slot is taken")

	release() // disconnect frees the slot (the server observes it asynchronously)

	require.Eventually(t, func() bool {
		return sseStatus(t, srv, sameOrigin()) == http.StatusOK
	}, 2*time.Second, 20*time.Millisecond, "a freed slot must admit a new connection")
}

// The cap is per-Register: two independently registered apps in one process must
// not share a counter, or a busy app would throttle an unrelated one.
func TestSSE_capIsPerRegister(t *testing.T) {
	t.Parallel()
	a := serve(t, via.Register(quietIsland{}, via.WithMaxSSEConnections(1)))
	b := serve(t, via.Register(quietIsland{}, via.WithMaxSSEConnections(1)))

	_, release := openStream(t, a) // fills A's only slot
	defer release()

	assert.Equal(t, http.StatusOK, sseStatus(t, b, sameOrigin()),
		"a second Register must have an independent cap")
}

// panicComp's action panics, to prove a buggy handler returns 500 and does not
// crash the server.
type panicComp struct{}

func (p *panicComp) Boom(*via.Ctx) { panic("boom") }
func (p *panicComp) View() h.H {
	return h.Div(h.Button(via.OnClick(p.Boom), h.Str("x")))
}

// POST /_via/a/{n} is a state-changing endpoint; under origin enforcement
// (WithTrustedOrigin set) a cross-site request must be rejected and must not
// mutate server state, or any page on the web can drive the counter (CSRF).
func TestAction_enforcementRejectsCrossSiteOriginAndDoesNotMutate(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(counter{count: &store{}}, via.WithTrustedOrigin("https://embedder.example")))

	status, _ := app.Action(1).SecFetch("cross-site").Fire()
	assert.Equal(t, http.StatusForbidden, status)

	_, body := app.Get("/")
	assert.Contains(t, body, "<h1>0</h1>", "cross-site POST must not have mutated the store")
}

// same-site (a sibling subdomain) is NOT same-origin; treating it as trusted is
// the classic CSRF confusion, so under enforcement a same-site action request
// must be rejected.
func TestAction_enforcementRejectsSameSiteOrigin(t *testing.T) {
	t.Parallel()
	status, _ := vt.Serve(t, via.Register(counter{count: &store{}}, via.WithTrustedOrigin("https://embedder.example"))).Action(1).SecFetch("same-site").Fire()
	assert.Equal(t, http.StatusForbidden, status)
}

// Under enforcement, a request that carries no origin signal at all (no
// Sec-Fetch-Site, no Origin) proves nothing about where it came from; the
// floor must fail closed rather than silently trust it.
func TestAction_enforcementFailsClosedWithoutAnyOriginSignal(t *testing.T) {
	t.Parallel()
	status, _ := vt.Serve(t, via.Register(counter{count: &store{}}, via.WithTrustedOrigin("https://embedder.example"))).Action(1).NoOrigin().Fire()
	assert.Equal(t, http.StatusForbidden, status)
}

// A legitimate same-origin browser fetch (proven via a matching Origin header
// when Sec-Fetch-Site is absent) must be allowed and must mutate state.
func TestAction_allowsSameOriginViaMatchingOriginHeader(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(counter{count: &store{}}))
	status, body := app.Action(1).Origin(app.URL()).Fire()
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "<h1>1</h1>")
}

// An unbounded request body is a memory-exhaustion DoS; the handler must cap it
// and reject an oversize body with 413 rather than buffering it whole.
func TestAction_rejectsOversizeBody(t *testing.T) {
	t.Parallel()
	big := `{"s0":"` + strings.Repeat("a", 2<<20) + `"}`
	status, _ := vt.Serve(t, via.Register(counter{count: &store{}})).Action(1).Body(big).Fire()
	assert.Equal(t, http.StatusRequestEntityTooLarge, status)
}

// A malformed (non-empty, invalid-JSON) body must fail loudly with 400, not
// silently bind an empty signal set and misroute the action.
func TestAction_rejectsMalformedBody(t *testing.T) {
	t.Parallel()
	status, _ := vt.Serve(t, via.Register(counter{count: &store{}})).Action(1).Body("{not valid json").Fire()
	assert.Equal(t, http.StatusBadRequest, status)
}

// An empty body is the common stateless-action case and must be treated as "no
// signals", not as a malformed-body 400.
func TestAction_treatsEmptyBodyAsNoSignals(t *testing.T) {
	t.Parallel()
	status, body := vt.Serve(t, via.Register(counter{count: &store{}})).Action(1).Body("").Fire()
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "<h1>1</h1>")
}

// A panicking action must be contained: the request returns 500 and the server
// keeps serving subsequent requests (no crash, no wedged connection).
func TestAction_panicIsRecoveredAs500AndServerStaysUp(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(panicComp{}))

	status, _ := app.Action(0).Fire()
	assert.Equal(t, http.StatusInternalServerError, status)

	status2, _ := app.Get("/")
	assert.Equal(t, http.StatusOK, status2, "server must keep serving after a panicking action")
}

// Without WithTrustedOrigin the action endpoint is open by default, so
// non-browser/dev clients that cannot send origin headers just work.
func TestAction_defaultAllowsCrossSite(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(counter{count: &store{}}))
	status, body := app.Action(1).SecFetch("cross-site").Fire()
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "<h1>1</h1>")
}

// The default-open floor must also admit a request with NO origin signal at all
// (no Origin, no Sec-Fetch-Site) — the plain curl / non-browser client case
// that motivates the dev-friendly default.
func TestAction_defaultAllowsRequestWithoutAnyOriginSignal(t *testing.T) {
	t.Parallel()
	status, body := vt.Serve(t, via.Register(counter{count: &store{}})).Action(1).NoOrigin().Fire()
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "<h1>1</h1>")
}

// WithTrustedOrigin allowlists a specific cross-origin embedder; that origin
// must be allowed even when the browser labels the request cross-site.
func TestWithTrustedOrigin_allowsNamedCrossOrigin(t *testing.T) {
	t.Parallel()
	const trusted = "https://trusted.example"
	app := vt.Serve(t, via.Register(counter{count: &store{}}, via.WithTrustedOrigin(trusted)))
	status, body := app.Action(1).Origin(trusted).SecFetch("cross-site").Fire()
	assert.Equal(t, http.StatusOK, status)
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
			app := vt.Serve(t, via.Register(counter{count: &store{}}, via.WithTrustedOrigin("https://embedder.example")))
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
			app := vt.Serve(t, via.Register(counter{count: &store{}}, via.WithTrustedOrigin("https://embedder.example")))
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
	app := vt.ServeTLS(t, via.Register(counter{count: &store{}}, via.WithTrustedOrigin("https://embedder.example")))

	down, _ := app.Action(1).Host("app.example").Origin("http://app.example").Fire()
	assert.Equal(t, http.StatusForbidden, down, "http Origin on a TLS request is a scheme downgrade")

	ok, body := app.Action(1).Host("app.example").Origin("https://app.example").Fire()
	assert.Equal(t, http.StatusOK, ok, "https Origin on a TLS request to the same host matches")
	assert.Contains(t, body, "<h1>1</h1>")
}
