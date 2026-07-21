package via_test

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/sess"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// acct is a session-stored value; profilePage loads it in OnInit so its ctx-free
// View can render the logged-in user.
type acct struct{ Name string }

type profilePage struct{ greeting string }

func (p *profilePage) OnInit(ctx *via.Ctx) error {
	if a, ok := sess.Get[acct](ctx); ok {
		p.greeting = "hi " + a.Name
	}
	return nil
}
func (p *profilePage) SignIn(ctx *via.Ctx) { sess.Put(ctx, acct{Name: "alice"}) }
func (p *profilePage) View() h.H {
	return h.Div(h.P(h.Str(p.greeting)), h.Button(via.OnClick(p.SignIn), h.Str("in")))
}

func jarGet(t *testing.T, c *http.Client, url string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func jarPost(t *testing.T, c *http.Client, url string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader("{}"))
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := c.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
}

// redirectPage is a stateless page whose @post action navigates the browser via
// via.Redirect — the case PostForm's native 303 can't cover (a Datastar @post).
type redirectPage struct{}

func (p *redirectPage) Go(ctx *via.Ctx)   { via.Redirect(ctx, "/dest") }
func (p *redirectPage) Evil(ctx *via.Ctx) { via.Redirect(ctx, "javascript:alert(1)") }
func (p *redirectPage) View() h.H {
	return h.Div(h.Button(via.OnClick(p.Go)), h.Button(via.OnClick(p.Evil)))
}

// relRedirectPage redirects to a relative path (no scheme) — also valid.
type relRedirectPage struct{}

func (p *relRedirectPage) Go(ctx *via.Ctx) { via.Redirect(ctx, "threads/7") }
func (p *relRedirectPage) View() h.H       { return h.Div(h.Button(via.OnClick(p.Go))) }

var nonceRe = regexp.MustCompile(`'nonce-([^']+)'`)

func cspNonce(t *testing.T, c *http.Client, url string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := c.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	m := nonceRe.FindStringSubmatch(resp.Header.Get("Content-Security-Policy"))
	require.Len(t, m, 2, "CSP header must carry a script-src nonce")
	return m[1]
}

// Once a session exists, the strict-CSP nonce is scoped to it (stable across the
// session's requests) rather than fresh per render — so a later action response
// can stamp an injected script with the SAME nonce the document's CSP admits.
func TestRouter_sessionScopesCSPNonceAcrossRequests(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	via.Mount(r, "/login", loginForm{})
	via.Mount(r, "/x", redirectPage{})
	srv := serve(t, r)

	// No session yet → per-render nonce (two GETs differ).
	anon, _ := cookiejar.New(nil)
	ca := &http.Client{Jar: anon}
	require.NotEqual(t, cspNonce(t, ca, srv.URL+"/x"), cspNonce(t, ca, srv.URL+"/x"),
		"without a session the nonce stays fresh per render")

	// After login the nonce is session-scoped → stable across requests.
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	formPost(c, t, srv.URL+"/login/_via/f/0", "name=alice")
	assert.Equal(t, cspNonce(t, c, srv.URL+"/x"), cspNonce(t, c, srv.URL+"/x"),
		"within a session the nonce is stable so action redirects can reuse it")
}

// A via.Redirect from a Datastar @post action sends an executable script
// (location.assign) carrying the document's CSP nonce, so the strict CSP admits
// it — the @post analogue of PostForm's 303.
func TestRouter_postActionRedirectShipsNonceMatchedScript(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	via.Mount(r, "/login", loginForm{})
	via.Mount(r, "/x", redirectPage{})
	srv := serve(t, r)

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	formPost(c, t, srv.URL+"/login/_via/f/0", "name=alice")
	nonce := cspNonce(t, c, srv.URL+"/x")

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/x/_via/a/0", strings.NewReader("{}"))
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	assert.Contains(t, resp.Header.Get("Content-Type"), "text/javascript",
		"a @post redirect is delivered as an executable script, not an element patch")
	assert.Contains(t, string(body), `location.assign("/dest")`)
	assert.Contains(t, resp.Header.Get("datastar-script-attributes"), nonce,
		"the injected script must carry the document's CSP nonce or the browser blocks it")
}

// Redirect interpolates into location.assign('…'), so a non-http(s)/relative URL
// (javascript:, data:, //evil) must be rejected — never shipped as a script.
func TestRouter_postActionRedirectRejectsUnsafeURL(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	via.Mount(r, "/login", loginForm{})
	via.Mount(r, "/x", redirectPage{})
	srv := serve(t, r)

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	formPost(c, t, srv.URL+"/login/_via/f/0", "name=alice")

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/x/_via/a/1", strings.NewReader("{}")) // Evil
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	assert.NotContains(t, resp.Header.Get("Content-Type"), "text/javascript",
		"an unsafe redirect URL must not be shipped as a script")
	assert.NotContains(t, string(body), "javascript:alert", "the unsafe URL must never reach the client")
}

// Without a session there is no nonce the document's CSP would admit, so a @post
// redirect is dropped (no script) rather than shipping one the browser blocks —
// the cookieless / pre-session case falls back to the element patch.
func TestRouter_postActionRedirectDroppedWithoutSession(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	via.Mount(r, "/x", redirectPage{})
	srv := serve(t, r)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/x/_via/a/0", strings.NewReader("{}"))
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.NotContains(t, resp.Header.Get("Content-Type"), "text/javascript",
		"no session ⇒ no admissible nonce ⇒ no redirect script")
}

// A same-origin relative path (no scheme) is a valid redirect target and is
// json-escaped into the script intact.
func TestRouter_postActionRedirectAllowsRelativePath(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	via.Mount(r, "/login", loginForm{})
	via.Mount(r, "/x", relRedirectPage{})
	srv := serve(t, r)

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	formPost(c, t, srv.URL+"/login/_via/f/0", "name=alice")
	cspNonce(t, c, srv.URL+"/x") // establish the document nonce

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/x/_via/a/0", strings.NewReader("{}"))
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/javascript")
	assert.Contains(t, string(body), `location.assign("threads/7")`, "a relative path is a valid target")
}

// OnInit runs per request before the (ctx-free) View, so a page can load
// session data into its fields and render it. Without it, a stateless page could
// never show "the logged-in user" — View has no ctx to read the session from.
func TestRouter_onInitLoadsSessionForRender(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	via.Mount(r, "/p", profilePage{})
	srv := serve(t, r)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}

	assert.NotContains(t, jarGet(t, c, srv.URL+"/p"), "hi alice", "no session yet")
	jarPost(t, c, srv.URL+"/p/_via/a/0") // SignIn → sets the session cookie
	assert.Contains(t, jarGet(t, c, srv.URL+"/p"), "hi alice",
		"OnInit must load the session before the ctx-free View renders")
}

// threadPage reads a positional path param (the {} in /thread/{}) in OnInit.
type threadPage struct{ id int }

func (p *threadPage) OnInit(ctx *via.Ctx) error { p.id = via.Param[int](ctx, 0); return nil }
func (p *threadPage) View() h.H                 { return h.Div(h.P(h.Str("thread "), h.Str(p.id))) }

// echoPage proves a path param is readable inside an ACTION (not just OnInit) on
// a param'd mount — the action POST URL carries the {} segment (/e/7/_via/a/0).
type echoPage struct{ echoed int }

func (p *echoPage) Echo(ctx *via.Ctx) { p.echoed = via.Param[int](ctx, 0) }
func (p *echoPage) View() h.H {
	return h.Div(h.Button(via.OnClick(p.Echo)), h.P(h.Str("echoed "), h.Str(p.echoed)))
}

// avatarPage uploads a file via a native multipart form; Save receives the file
// as a via.File and drains it. cap records what the handler saw (the per-request
// page copy is discarded, so a pointer captures it for the assertion).
type capture struct {
	name, ctype, body string
	size              int64
}
type avatarPage struct{ cap *capture }

func (p *avatarPage) Save(ctx *via.Ctx, f via.File) {
	p.cap.name = f.Name()
	p.cap.ctype = f.ContentType()
	p.cap.size = f.Size()
	b, _ := io.ReadAll(f)
	p.cap.body = string(b)
	via.Redirect(ctx, "/done")
}
func (p *avatarPage) View() h.H {
	return via.OnUpload(p.Save,
		h.Input(h.RawAttr("type", "file"), h.RawAttr("name", "avatar")),
		h.Button(h.Str("upload")),
	)
}

func uploadPOST(c *http.Client, t *testing.T, url, filename, content string) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("avatar", filename)
	fw.Write([]byte(content))
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, url, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := c.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// OnUpload renders a native multipart form; its handler receives the uploaded
// file (bytes + filename) — the file analogue of PostForm. Storage is app-land:
// File is just an io.Reader + metadata the app drains.
func TestRouter_onUploadDeliversFileToHandler(t *testing.T) {
	t.Parallel()
	cap := &capture{}
	r := via.NewRouter()
	via.Mount(r, "/p", avatarPage{cap: cap})
	srv := serve(t, r)

	_, page := do(t, srv, http.MethodGet, "/p", "")
	assert.Contains(t, page, `enctype="multipart/form-data"`, "OnUpload must render a multipart form")
	assert.Contains(t, page, `action="/p/_via/upload/0"`, "posting to the positional upload endpoint")

	resp := uploadPOST(&http.Client{CheckRedirect: noFollow}, t, srv.URL+"/p/_via/upload/0", "me.png", "PNGBYTES")
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode, "a Redirect in the handler must 303")
	assert.Equal(t, "me.png", cap.name, "handler must receive the filename")
	assert.Equal(t, "PNGBYTES", cap.body, "handler must receive the file bytes")
	assert.Equal(t, int64(len("PNGBYTES")), cap.size, "File.Size must report the parsed byte count")
	assert.Equal(t, "application/octet-stream", cap.ctype, "File.ContentType must report the part's declared type")
}

// A multipart body with no file part is a client error (the handler needs a
// file); an out-of-range upload index fails closed so a stale client re-bootstraps.
func TestRouter_onUploadRejectsMissingFileAndBadIndex(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	via.Mount(r, "/p", avatarPage{cap: &capture{}})
	srv := serve(t, r)
	c := &http.Client{CheckRedirect: noFollow}

	// out-of-range upload slot → 410
	resp := uploadPOST(c, t, srv.URL+"/p/_via/upload/9", "x.png", "data")
	assert.Equal(t, http.StatusGone, resp.StatusCode)

	// multipart body carrying only a text field (no file) → 400
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("caption", "hi")
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/p/_via/upload/0", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp2, err := c.Do(req)
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp2.StatusCode)
}

// The upload body is capped (memory/disk-exhaustion defense): an oversize upload
// is rejected, not buffered/spilled whole.
func TestRouter_onUploadCapsBody(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	via.Mount(r, "/p", avatarPage{cap: &capture{}})
	srv := serve(t, r)

	big := strings.Repeat("x", 9<<20) // > maxUploadBytes (8 MiB)
	resp := uploadPOST(&http.Client{CheckRedirect: noFollow}, t, srv.URL+"/p/_via/upload/0", "big.bin", big)
	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
}

// An upload is state-changing, so under origin enforcement (WithTrustedOrigin
// set) a cross-site POST fails closed (CSRF), like the action and form
// endpoints.
func TestRouter_onUploadRejectsCrossSiteOrigin(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithTrustedOrigin("https://embedder.example"))
	via.Mount(r, "/p", avatarPage{cap: &capture{}})
	srv := serve(t, r)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("avatar", "x.png")
	fw.Write([]byte("data"))
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/p/_via/upload/0", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// secret is a guarded page: RequireSession redirects to /login when no acct is
// in the session.
type secret struct{}

func (s *secret) View() h.H { return h.Div(h.Str("secret area")) }

var noFollow = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func formPost(c *http.Client, t *testing.T, url, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := c.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// A positional path param binds the {} segment so the page can read it (in
// OnInit / actions) without an identifier string — /thread/42 → Param[int](0)=42.
func TestRouter_pathParamBindsPositionally(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	via.Mount(r, "/thread/{}", threadPage{})
	srv := serve(t, r)

	_, body := do(t, srv, http.MethodGet, "/thread/42", "")
	assert.Contains(t, body, "thread 42", "Param[int](ctx,0) must read the {} segment")
}

// The path param is captured on the action sub-route too, so an action (whose
// POST URL carries the {} segment) reads it — not just OnInit.
func TestRouter_pathParamReadableInAction(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	via.Mount(r, "/e/{}", echoPage{})
	srv := serve(t, r)

	_, page := do(t, srv, http.MethodGet, "/e/7", "")
	assert.Contains(t, page, `@post('/e/7/_via/a/0')`, "action URL must carry the concrete {} segment")
	_, body := do(t, srv, http.MethodPost, "/e/7/_via/a/0", "{}")
	assert.Contains(t, body, "echoed 7", "the action must read Param[int](ctx,0) from its own POST path")
}

// A segment that cannot decode into Param's type is a bad request against a
// real route shape — /thread/abc for Param[int] answers 404, never a silent
// zero-value render ("thread 0" would be a lie). Fails if Param goes back to
// swallowing the decode error.
func TestRouter_pathParamBadSegmentIs404(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	via.Mount(r, "/thread/{}", threadPage{})
	srv := serve(t, r)

	resp, body := do(t, srv, http.MethodGet, "/thread/abc", "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.NotContains(t, body, "thread 0", "a bad segment must never render the zero value")
}

// The same 404 contract holds when the bad segment reaches Param inside an
// ACTION (the POST URL carries the segment).
func TestRouter_pathParamBadSegmentInActionIs404(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	via.Mount(r, "/e/{}", echoPage{})
	srv := serve(t, r)

	resp, _ := do(t, srv, http.MethodPost, "/e/abc/_via/a/0", "{}")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// A guard protects the action sub-route too (not just the page GET): an
// unauthenticated action POST is redirected before any handler runs.
func TestRouter_guardProtectsActionPost(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	via.Mount(r, "/secret", secret{}, via.RequireSession[acct]("/login"))
	srv := serve(t, r)

	resp := formPost(&http.Client{CheckRedirect: noFollow}, t, srv.URL+"/secret/_via/a/0", "{}")
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode, "guard must gate the action route, not only the page")
	assert.Equal(t, "/login", resp.Header.Get("Location"))
}

// A guarded page redirects (303) to the login path when the required session
// value is absent.
func TestRouter_requireSessionRedirectsWhenAbsent(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	via.Mount(r, "/secret", secret{}, via.RequireSession[acct]("/login"))
	srv := serve(t, r)

	c := &http.Client{CheckRedirect: noFollow}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/secret", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "/login", resp.Header.Get("Location"))
}

// With the required session present, the guard passes and the page renders.
func TestRouter_requireSessionAllowsWhenPresent(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	via.Mount(r, "/login", loginForm{})
	via.Mount(r, "/secret", secret{}, via.RequireSession[acct]("/login"))
	srv := serve(t, r)

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: noFollow}
	formPost(c, t, srv.URL+"/login/_via/f/0", "name=alice") // sets acct in the session
	assert.Contains(t, jarGet(t, c, srv.URL+"/secret"), "secret area",
		"guard must pass once the session has the required value")
}

// loginForm is a native server-rendered form (no Datastar): Submit reads a form
// field, logs the user in, and redirects — the canonical auth flow.
type loginForm struct{}

func (l *loginForm) Submit(ctx *via.Ctx) {
	if name := ctx.Request().FormValue("name"); name != "" {
		sess.Put(ctx, acct{Name: name})
		via.Redirect(ctx, "/welcome")
	}
}
func (l *loginForm) View() h.H {
	return via.PostForm(l.Submit,
		h.Input(h.RawAttr("name", "name")),
		h.Button(h.Str("go")),
	)
}

func postFormURL(t *testing.T, url, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// A native form posts to a positional form endpoint; its handler runs (reading
// the form fields off the request), and a via.Redirect turns into a 303 — so a
// sign-in navigates the browser, which Datastar (no execute-script) cannot do.
func TestRouter_postFormRunsHandlerAndRedirects(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	via.Mount(r, "/login", loginForm{})
	srv := serve(t, r)

	_, page := do(t, srv, http.MethodGet, "/login", "")
	assert.Contains(t, page, `<form method="post" action="/login/_via/f/0">`,
		"PostForm must render a native form posting to the form endpoint")

	resp := postFormURL(t, srv.URL+"/login/_via/f/0", "name=alice")
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode, "via.Redirect must 303")
	assert.Equal(t, "/welcome", resp.Header.Get("Location"))
	assert.Contains(t, resp.Header.Get("Set-Cookie"), "via_session",
		"the sign-in session cookie must ride the 303 so the redirect lands authenticated")
}

// A form POST is state-changing, so under origin enforcement (WithTrustedOrigin
// set) it must fail closed to a cross-site origin (CSRF), exactly like the
// action endpoint.
func TestRouter_postFormRejectsCrossSiteOrigin(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithTrustedOrigin("https://embedder.example"))
	via.Mount(r, "/login", loginForm{})
	srv := serve(t, r)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/login/_via/f/0", strings.NewReader("name=alice"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// An out-of-range form index fails closed (410), so a stale client re-bootstraps.
func TestRouter_postFormOutOfRangeIsGone(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	via.Mount(r, "/login", loginForm{})
	srv := serve(t, r)

	resp := postFormURL(t, srv.URL+"/login/_via/f/9", "name=alice")
	assert.Equal(t, http.StatusGone, resp.StatusCode)
}

// The form body is capped (memory-exhaustion parity with the JSON action path):
// an oversize body is rejected, not buffered whole.
func TestRouter_postFormCapsBody(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	via.Mount(r, "/login", loginForm{})
	srv := serve(t, r)

	resp := postFormURL(t, srv.URL+"/login/_via/f/0", "name="+strings.Repeat("x", 2<<20))
	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
}

// A form handler that does not redirect re-renders the page (so it can show
// validation errors etc.).
func TestRouter_postFormWithoutRedirectReRenders(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	via.Mount(r, "/login", loginForm{})
	srv := serve(t, r)

	resp := postFormURL(t, srv.URL+"/login/_via/f/0", "name=") // empty → no redirect
	require.Equal(t, http.StatusOK, resp.StatusCode)
	b, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(b), `<form method="post"`, "no-redirect form post must re-render the page")
}

// A router serves several pages at their own paths; each page's actions are
// namespaced under its mount path, so two pages can both declare action 1
// without colliding, and an action on one page never touches the other.
func TestRouter_mountsPagesWithPathNamespacedIndependentActions(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	via.Mount(r, "/a", counter{count: &store{}})
	via.Mount(r, "/b", counter{count: &store{}})
	srv := serve(t, r)

	// Each page renders at its path, with its actions namespaced under it.
	_, a := do(t, srv, http.MethodGet, "/a", "")
	assert.Contains(t, a, `<h1>0</h1>`)
	assert.Contains(t, a, `@post('/a/_via/a/1')`, "page /a's Inc must post under /a")
	_, b := do(t, srv, http.MethodGet, "/b", "")
	assert.Contains(t, b, `@post('/b/_via/a/1')`, "page /b's Inc must post under /b")

	// Inc on /a; /b must be untouched (independent state + routing).
	do(t, srv, http.MethodPost, "/a/_via/a/1", "{}")
	_, a2 := do(t, srv, http.MethodGet, "/a", "")
	assert.Contains(t, a2, `<h1>1</h1>`, "/a's counter must reflect its action")
	_, b2 := do(t, srv, http.MethodGet, "/b", "")
	assert.Contains(t, b2, `<h1>0</h1>`, "/b must be unaffected by an action on /a")
}

// Mounting at "/" must namespace to the root (no prefix): the page posts to
// /_via/a/{n}, exactly like a single-page Register.
func TestRouter_mountAtRootHasNoPrefix(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	via.Mount(r, "/", counter{count: &store{}})
	srv := serve(t, r)

	_, body := do(t, srv, http.MethodGet, "/", "")
	assert.Contains(t, body, `@post('/_via/a/1')`, "root mount must post to /_via/a/{n} with no prefix")
	resp, after := do(t, srv, http.MethodPost, "/_via/a/1", "{}")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, after, `<h1>1</h1>`)
}

// A mounted action still ships the page-hardening response headers and behaves
// like the single-page action (element-patch on change).
func TestRouter_mountedActionElementPatches(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	via.Mount(r, "/a", counter{count: &store{}})
	srv := serve(t, r)

	resp, body := do(t, srv, http.MethodPost, "/a/_via/a/1", "{}")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, `<h1>1</h1>`, "mounted action must element-patch the new value")
}

// failInitPage's OnInit fails: with via.ErrNotFound for /missing (a
// world-changed miss → 404) and a plain error otherwise (a server fault → 500).
type failInitPage struct{ kind string }

func (p *failInitPage) OnInit(ctx *via.Ctx) error {
	if p.kind == "missing" {
		return via.ErrNotFound
	}
	return errors.New("db down")
}
func (p *failInitPage) View() h.H { return h.P(h.Str("never")) }

// OnInit returning via.ErrNotFound must answer 404 and never render the View —
// the page's data is gone, and pretending otherwise would paint a lie. Fails if
// the sentinel stops mapping to 404 or the render proceeds past a failed init.
func TestRouter_onInitErrNotFoundIs404(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	via.Mount(r, "/x", failInitPage{kind: "missing"})
	resp, body := do(t, serve(t, r), http.MethodGet, "/x", "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.NotContains(t, body, "never", "a failed OnInit must not render the View")
}

// Any other OnInit error is the app's fault → 500, View never renders.
func TestRouter_onInitErrorIs500(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	via.Mount(r, "/x", failInitPage{kind: "boom"})
	resp, body := do(t, serve(t, r), http.MethodGet, "/x", "")
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.NotContains(t, body, "never")
}

// The same contract holds on the action path: a failed OnInit blocks the
// action from running at all.
func TestRouter_onInitErrorBlocksAction(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	via.Mount(r, "/x", failInitPage{kind: "missing"})
	resp, _ := do(t, serve(t, r), http.MethodPost, "/x/_via/a/0", "{}")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// Register is Mount at "/" internally — ONE dispatch pipeline. The observable
// consequence: a single-page Register(root) serves the router-only transports
// too (a native PostForm posts to /_via/f/0 and 303s). Fails if Register grows
// its own separate mux again.
func TestRegister_isMountAtRootOneDispatchPipeline(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(loginForm{}))

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/_via/f/0", strings.NewReader("name=alice"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode, "Register must serve the form transport like any mount")
	assert.Equal(t, "/welcome", resp.Header.Get("Location"))
}

// The unified pipeline carries the live machinery too: a live island mounted on
// a Router (not just Register) bootstraps the SSE stream from its page — the
// body carries @post('<base>/_via/sse') and the reconnect manager. Fails if
// Mount loses the live bootstrap detection.
func TestMount_livePageBootstrapsStreamUnderTheRouter(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	via.Mount(r, "/live", quietIsland{})
	_, body := do(t, serve(t, r), http.MethodGet, "/live", "")
	assert.Contains(t, body, `@post('/live/_via/sse')`, "a mounted live page must bootstrap its own SSE endpoint")
	assert.Contains(t, body, "window.__viaRC", "the reconnect manager rides the mounted live page")
}
