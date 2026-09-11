package via_test

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// acct is a session-stored value; profilePage loads it in OnInit so its ctx-free
// View can render the logged-in user.
type acct struct{ Name string }

type profilePage struct{ greeting string }

func (p *profilePage) OnInit(ctx *via.Ctx) error {
	if a, ok := ctx.Session().Get[acct](); ok {
		p.greeting = "hi " + a.Name
	}
	return nil
}
func (p *profilePage) SignIn(ctx *via.Ctx) { ctx.Session().Put(acct{Name: "alice"}) }
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
	req.Header.Set("Datastar-Request", "true")
	resp, err := c.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
}

// redirectPage is a stateless page whose @post action calls via.Redirect — a
// case that can no longer navigate the browser (only PostForm and OnInit can).
type redirectPage struct{}

func (p *redirectPage) Go(ctx *via.Ctx)   { ctx.Redirect("/dest") }
func (p *redirectPage) Evil(ctx *via.Ctx) { ctx.Redirect("javascript:alert(1)") }
func (p *redirectPage) View() h.H {
	return h.Div(h.Button(via.OnClick(p.Go)), h.Button(via.OnClick(p.Evil)))
}

// cspOf fetches a page and returns the Content-Security-Policy it served.
func cspOf(t *testing.T, c *http.Client, url string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := c.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	csp := resp.Header.Get("Content-Security-Policy")
	require.Contains(t, csp, "'sha256-", "CSP must admit via's inline scripts by hash")
	return csp
}

// A @post Redirect's script must be admitted by a document that may have been
// served by another pod. Hashing gets there without any shared secret: the
// policy depends on no cookie, no session, and no signing key, so pods with
// DIFFERENT keys serve byte-identical policies. The old boot nonce only worked
// when every pod shared VIA_SESSION_KEY — mismatched keys broke the redirect.
func TestRouter_cspIsStatelessAndKeyIndependent(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	r.Mount("/x", redirectPage{})
	srv := serve(t, r)

	// No cookies, no session — the policy is still stable across requests.
	c := &http.Client{}
	csp1 := cspOf(t, c, srv.URL+"/x")
	assert.Equal(t, csp1, cspOf(t, c, srv.URL+"/x"),
		"the policy must be stable across requests without any session")

	// A second app booted from a DIFFERENT key serves the same policy.
	r2 := via.NewRouter(via.WithSessionKey([]byte("a-different-key-also-32-bytes-ok")))
	r2.Mount("/x", redirectPage{})
	srv2 := serve(t, r2)
	assert.Equal(t, csp1, cspOf(t, c, srv2.URL+"/x"),
		"a hash-based policy needs no shared key: pods with different keys agree")
}

// A via.Redirect from a Datastar @post action cannot navigate the page — only
// PostForm and OnInit can. It must not ship a script (or any other trace of
// the target); the action just answers its normal element-patch/204 contract,
// for a safe target as much as an unsafe one.
func TestRouter_postActionRedirectDoesNotShipAScript(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	r.Mount("/x", redirectPage{})
	srv := serve(t, r)

	c := &http.Client{}
	_, page := do(t, srv, http.MethodGet, "/x", "")

	for i, target := range []string{"/dest", "javascript:alert(1)"} { // Go, Evil
		req, _ := http.NewRequest(http.MethodPost, srv.URL+actionURL(t, page, 0, i), strings.NewReader("{}"))
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Datastar-Request", "true")
		resp, err := c.Do(req)
		require.NoError(t, err)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		assert.NotContains(t, resp.Header.Get("Content-Type"), "text/javascript",
			"a @post Redirect must not be delivered as an executable script")
		assert.Empty(t, resp.Header.Get("datastar-script-attributes"),
			"no script attributes header for a target %q that cannot navigate", target)
		assert.NotContains(t, string(body), target, "the redirect target must never reach the client")
	}
}

// OnInit runs per request before the (ctx-free) View, so a page can load
// session data into its fields and render it. Without it, a stateless page could
// never show "the logged-in user" — View has no ctx to read the session from.
func TestRouter_onInitLoadsSessionForRender(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	r.Mount("/p", profilePage{})
	srv := serve(t, r)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}

	page := jarGet(t, c, srv.URL+"/p")
	assert.NotContains(t, page, "hi alice", "no session yet")
	jarPost(t, c, srv.URL+actionURL(t, page, 0, 0)) // SignIn → sets the session cookie
	assert.Contains(t, jarGet(t, c, srv.URL+"/p"), "hi alice",
		"OnInit must load the session before the ctx-free View renders")
}

// threadPage reads a named path param (the {id} in /thread/{id}) in OnInit.
type threadPage struct{ id int }

func (p *threadPage) OnInit(ctx *via.Ctx) error { p.id = ctx.Param[int]("id"); return nil }
func (p *threadPage) View() h.H                 { return h.Div(h.P(h.Str("thread "), h.Str(p.id))) }

// echoPage proves a path param is readable inside an ACTION (not just OnInit) on
// a param'd mount — the action POST URL carries the {id} segment (/e/7/_via/a/0/0).
type echoPage struct{ echoed int }

func (p *echoPage) Echo(ctx *via.Ctx) { p.echoed = ctx.Param[int]("id") }
func (p *echoPage) View() h.H {
	return h.Div(h.Button(via.OnClick(p.Echo)), h.P(h.Str("echoed "), h.Str(p.echoed)))
}

// avatarPage uploads a file via PostForm (always multipart); Save reads it with
// stdlib. cap records what the handler saw (the per-request page copy is
// discarded, so a pointer captures it for the assertion).
type capture struct {
	name, ctype, body string
	size              int64
}
type avatarPage struct{ cap *capture }

func (p *avatarPage) Save(ctx *via.Ctx) {
	f, hdr, err := ctx.Request().FormFile("avatar")
	if err != nil {
		return
	}
	defer f.Close()
	p.cap.name = hdr.Filename
	p.cap.ctype = hdr.Header.Get("Content-Type")
	p.cap.size = hdr.Size
	b, _ := io.ReadAll(f)
	p.cap.body = string(b)
	if hdr.Filename == "evil.png" {
		ctx.Redirect("javascript:alert(1)")
		return
	}
	ctx.Redirect("/done")
}
func (p *avatarPage) View() h.H {
	return via.PostForm(p.Save,
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

// PostForm is always multipart, so a file <input> reaches the handler through
// stdlib's ctx.Request().FormFile — no separate upload verb or type.
func TestPostForm_deliversMultipartFileToHandler(t *testing.T) {
	t.Parallel()
	cap := &capture{}
	r := via.NewRouter()
	r.Mount("/p", avatarPage{cap: cap})
	srv := serve(t, r)

	_, page := do(t, srv, http.MethodGet, "/p", "")
	assert.Contains(t, page, `enctype="multipart/form-data"`, "PostForm must always render a multipart form")
	assert.Contains(t, page, `action="/p/_via/a/0/0?v=`, "posting to the positional form endpoint")

	resp := uploadPOST(&http.Client{CheckRedirect: noFollow}, t, srv.URL+actionURL(t, page, 0, 0), "me.png", "PNGBYTES")
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode, "a Redirect in the handler must 303")
	assert.Equal(t, "me.png", cap.name, "handler must receive the filename")
	assert.Equal(t, "PNGBYTES", cap.body, "handler must receive the file bytes")
	assert.Equal(t, int64(len("PNGBYTES")), cap.size, "the header must report the parsed byte count")
	assert.Equal(t, "application/octet-stream", cap.ctype, "the header must report the part's declared type")
}

// The body cap for PostForm rises to maxUploadBytes (files are the payload);
// an oversize multipart body is rejected, not buffered/spilled whole.
func TestPostForm_rejectsOversizeUpload413(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	r.Mount("/p", avatarPage{cap: &capture{}})
	srv := serve(t, r)

	big := strings.Repeat("x", 9<<20) // > maxUploadBytes (8 MiB)
	resp := uploadPOST(&http.Client{CheckRedirect: noFollow}, t, srv.URL+"/p/_via/a/0/0", "big.bin", big)
	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
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

// A named path param binds the {name} segment so the page can read it (in
// OnInit / actions) by that name, exactly like http.ServeMux —
// /thread/42 → Param[int]("id")=42.
func TestRouter_paramByNameMatchesServeMux(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	r.Mount("/thread/{id}", threadPage{})
	srv := serve(t, r)

	_, body := do(t, srv, http.MethodGet, "/thread/42", "")
	assert.Contains(t, body, "thread 42", `ctx.Param[int]("id") must read the {id} segment`)
}

// The path param is captured on the action sub-route too, so an action (whose
// POST URL carries the {id} segment) reads it — not just OnInit.
func TestRouter_pathParamReadableInAction(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	r.Mount("/e/{id}", echoPage{})
	srv := serve(t, r)

	_, page := do(t, srv, http.MethodGet, "/e/7", "")
	assert.Contains(t, page, `@post('/e/7/_via/a/0/0?v=`, "action URL must carry the concrete {id} segment")
	_, body := do(t, srv, http.MethodPost, actionURL(t, page, 0, 0), "{}")
	assert.Contains(t, body, "echoed 7", `the action must read ctx.Param[int]("id") from its own POST path`)
}

// A segment that cannot decode into Param's type is a bad request against a
// real route shape — /thread/abc for Param[int] answers 404, never a silent
// zero-value render ("thread 0" would be a lie). Fails if Param goes back to
// swallowing the decode error.
func TestRouter_pathParamBadSegmentIs404(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	r.Mount("/thread/{id}", threadPage{})
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
	r.Mount("/e/{id}", echoPage{})
	srv := serve(t, r)

	_, page := do(t, srv, http.MethodGet, "/e/abc", "")
	resp, _ := do(t, srv, http.MethodPost, actionURL(t, page, 0, 0), "{}")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// wrongNamePage asks Param for a name its own mount pattern never declares —
// a wiring mistake, not a request-shaped error.
type wrongNamePage struct{}

func (p *wrongNamePage) OnInit(ctx *via.Ctx) error { ctx.Param[int]("bogus"); return nil }
func (p *wrongNamePage) View() h.H                 { return h.Div() }

// Asking Param for a name the mount pattern doesn't declare panics (recovered
// to a 500) rather than returning a silent zero value that would masquerade
// as real data.
func TestRouter_paramUnknownNamePanics(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	r.Mount("/thread/{id}", wrongNamePage{})
	srv := serve(t, r)

	resp, _ := do(t, srv, http.MethodGet, "/thread/42", "")
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode,
		"Param for an undeclared name is a wiring mistake, not a 404")
}

// A guard protects the action sub-route too (not just the page GET): an
// unauthenticated action POST is redirected before any handler runs.
func TestRouter_guardProtectsActionPost(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	r.Mount("/secret", secret{}, via.RequireSession[acct]("/login"))
	srv := serve(t, r)

	resp := formPost(&http.Client{CheckRedirect: noFollow}, t, srv.URL+"/secret/_via/a/0/0", "{}")
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode, "guard must gate the action route too, not just the page")
	assert.Equal(t, "/login", resp.Header.Get("Location"))
}

// unsafeGuard always fails, redirecting to a scheme SafeURL rejects — the
// same shape as a guard authored with a mistyped or hostile target.
func unsafeGuard(*via.Ctx) (string, bool) { return "javascript:alert(1)", false }

// A guard redirect goes through the same hcore.SafeURL check as an in-app
// Redirect: an unsafe target must never reach http.Redirect.
func TestRouter_guardRedirectRejectsUnsafeTarget(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	r.Mount("/secret", secret{}, unsafeGuard)
	srv := serve(t, r)

	c := &http.Client{CheckRedirect: noFollow}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/secret", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("Location"), "an unsafe guard target must never reach http.Redirect")
}

// A guarded page redirects (303) to the login path when the required session
// value is absent.
func TestRouter_requireSessionRedirectsWhenAbsent(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	r.Mount("/secret", secret{}, via.RequireSession[acct]("/login"))
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
	r.Mount("/login", loginForm{})
	r.Mount("/secret", secret{}, via.RequireSession[acct]("/login"))
	srv := serve(t, r)

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: noFollow}
	loginPage := jarGet(t, c, srv.URL+"/login")
	postForm(c, t, srv.URL+actionURL(t, loginPage, 0, 0), "name", "alice") // sets acct in the session
	assert.Contains(t, jarGet(t, c, srv.URL+"/secret"), "secret area",
		"guard must pass once the session has the required value")
}

// loginForm is a native server-rendered form (no Datastar): Submit reads a form
// field, logs the user in, and redirects — the canonical auth flow.
type loginForm struct{}

func (l *loginForm) Submit(ctx *via.Ctx) {
	if name := ctx.Request().FormValue("name"); name != "" {
		ctx.Session().Put(acct{Name: name})
		if name == "evil" {
			ctx.Redirect("javascript:alert(1)")
			return
		}
		ctx.Redirect("/welcome")
	}
}
func (l *loginForm) View() h.H {
	return via.PostForm(l.Submit,
		h.Input(h.RawAttr("name", "name")),
		h.Button(h.Str("go")),
	)
}

// postForm submits a native <form> POST — PostForm is always multipart, so
// every native-form test builds one, even a plain text field.
func postForm(c *http.Client, t *testing.T, url, field, value string) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField(field, value)
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, url, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "same-origin")
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
	r.Mount("/login", loginForm{})
	srv := serve(t, r)

	_, page := do(t, srv, http.MethodGet, "/login", "")
	assert.Contains(t, page, `<form method="post" enctype="multipart/form-data" action="/login/_via/a/0/0?v=`,
		"PostForm must render a native multipart form posting to the form endpoint")

	resp := postForm(&http.Client{CheckRedirect: noFollow}, t, srv.URL+actionURL(t, page, 0, 0), "name", "alice")
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
	r.Mount("/login", loginForm{})
	srv := serve(t, r)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("name", "alice")
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/login/_via/a/0/0", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
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
	r.Mount("/login", loginForm{})
	srv := serve(t, r)
	_, page := do(t, srv, http.MethodGet, "/login", "")
	url := swapActionIndex(t, actionURL(t, page, 0, 0), "9")

	resp := postForm(&http.Client{CheckRedirect: noFollow}, t, srv.URL+url, "name", "alice")
	assert.Equal(t, http.StatusGone, resp.StatusCode)
}

// The form body is capped (memory-exhaustion parity with the JSON action path):
// an oversize body is rejected, not buffered whole.
func TestRouter_postFormCapsBody(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	r.Mount("/login", loginForm{})
	srv := serve(t, r)

	resp := postForm(&http.Client{CheckRedirect: noFollow}, t, srv.URL+"/login/_via/a/0/0", "name", strings.Repeat("x", 9<<20))
	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
}

// A form handler that does not redirect re-renders the page (so it can show
// validation errors etc.).
func TestRouter_postFormWithoutRedirectReRenders(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	r.Mount("/login", loginForm{})
	srv := serve(t, r)

	_, page := do(t, srv, http.MethodGet, "/login", "")
	resp := postForm(&http.Client{CheckRedirect: noFollow}, t, srv.URL+actionURL(t, page, 0, 0), "name", "") // empty → no redirect
	require.Equal(t, http.StatusOK, resp.StatusCode)
	b, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(b), `<form method="post"`, "no-redirect form post must re-render the page")
}

// A form handler's ctx.Redirect target reaches http.Redirect verbatim: an
// hostile scheme must be rejected here too, not just on the @post script path.
func TestRouter_postFormRejectsUnsafeRedirectScheme(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	r.Mount("/login", loginForm{})
	srv := serve(t, r)

	_, page := do(t, srv, http.MethodGet, "/login", "")
	resp := postForm(&http.Client{CheckRedirect: noFollow}, t, srv.URL+actionURL(t, page, 0, 0), "name", "evil")
	assert.NotEqual(t, http.StatusSeeOther, resp.StatusCode,
		"an unsafe redirect target must not be shipped as a Location header")
	assert.Empty(t, resp.Header.Get("Location"), "no Location header for a rejected redirect")
}

// A router serves several pages at their own paths; each page's actions are
// namespaced under its mount path, so two pages can both declare action 1
// without colliding, and an action on one page never touches the other.
func TestRouter_mountsPagesWithPathNamespacedIndependentActions(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	r.Mount("/a", counter{count: &store{}})
	r.Mount("/b", counter{count: &store{}})
	srv := serve(t, r)

	// Each page renders at its path, with its actions namespaced under it.
	_, a := do(t, srv, http.MethodGet, "/a", "")
	assert.Contains(t, a, `<h1>0</h1>`)
	assert.Contains(t, a, `@post('/a/_via/a/0/1?v=`, "page /a's Inc must post under /a")
	_, b := do(t, srv, http.MethodGet, "/b", "")
	assert.Contains(t, b, `@post('/b/_via/a/0/1?v=`, "page /b's Inc must post under /b")

	// Inc on /a; /b must be untouched (independent state + routing).
	do(t, srv, http.MethodPost, actionURL(t, a, 0, 1), "{}")
	_, a2 := do(t, srv, http.MethodGet, "/a", "")
	assert.Contains(t, a2, `<h1>1</h1>`, "/a's counter must reflect its action")
	_, b2 := do(t, srv, http.MethodGet, "/b", "")
	assert.Contains(t, b2, `<h1>0</h1>`, "/b must be unaffected by an action on /a")
}

// Mounting at "/" must namespace to the root (no prefix): the page posts to
// /_via/a/0/{n}, exactly like a single-page Register.
func TestRouter_mountAtRootHasNoPrefix(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	r.Mount("/", counter{count: &store{}})
	srv := serve(t, r)

	_, body := do(t, srv, http.MethodGet, "/", "")
	assert.Contains(t, body, `@post('/_via/a/0/1?v=`, "root mount must post to /_via/a/{n} with no prefix")
	resp, after := do(t, srv, http.MethodPost, actionURL(t, body, 0, 1), "{}")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, after, `<h1>1</h1>`)
}

// A mounted action still ships the page-hardening response headers and behaves
// like the single-page action (element-patch on change).
func TestRouter_mountedActionElementPatches(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	r.Mount("/a", counter{count: &store{}})
	srv := serve(t, r)

	_, page := do(t, srv, http.MethodGet, "/a", "")
	resp, body := do(t, srv, http.MethodPost, actionURL(t, page, 0, 1), "{}")
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
	r.Mount("/x", failInitPage{kind: "missing"})
	resp, body := do(t, serve(t, r), http.MethodGet, "/x", "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.NotContains(t, body, "never", "a failed OnInit must not render the View")
}

// Any other OnInit error is the app's fault → 500, View never renders.
func TestRouter_onInitErrorIs500(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	r.Mount("/x", failInitPage{kind: "boom"})
	resp, body := do(t, serve(t, r), http.MethodGet, "/x", "")
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.NotContains(t, body, "never")
}

// The same contract holds on the action path: a failed OnInit blocks the
// action from running at all.
func TestRouter_onInitErrorBlocksAction(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	r.Mount("/x", failInitPage{kind: "missing"})
	resp, _ := do(t, serve(t, r), http.MethodPost, "/x/_via/a/0/0", "{}")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// Register is Mount at "/" internally — ONE dispatch pipeline. The observable
// consequence: a single-page Register(root) serves the router-only transports
// too (a native PostForm posts to /_via/a/0/0 and 303s). Fails if Register grows
// its own separate mux again.
func TestRegister_isMountAtRootOneDispatchPipeline(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(loginForm{}))

	_, page := do(t, srv, http.MethodGet, "/", "")
	resp := postForm(&http.Client{CheckRedirect: noFollow}, t, srv.URL+actionURL(t, page, 0, 0), "name", "alice")
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
	r.Mount("/live", quietIsland{})
	_, body := do(t, serve(t, r), http.MethodGet, "/live", "")
	assert.Contains(t, body, `@post('/live/_via/sse')`, "a mounted live page must bootstrap its own SSE endpoint")
	assert.Contains(t, body, "window.__viaRC", "the reconnect manager rides the mounted live page")
}
