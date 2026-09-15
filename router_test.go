package via_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
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
	return h.Div(h.P(h.Str(p.greeting)), h.Button(via.On("click", p.SignIn), h.Str("in")))
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

// redirectPage is a plain page whose @post action calls via.Redirect: one safe
// target and one that must be refused.
type redirectPage struct{}

func (p *redirectPage) Go(ctx *via.Ctx)   { ctx.Redirect("/dest") }
func (p *redirectPage) Evil(ctx *via.Ctx) { ctx.Redirect("javascript:alert(1)") }
func (p *redirectPage) View() h.H {
	return h.Div(h.Button(via.On("click", p.Go)), h.Button(via.On("click", p.Evil)))
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

// A via.Redirect from a Datastar @post action navigates the tab through the
// hash-admitted script Datastar's text/javascript branch runs — but ONLY for a
// target hcore.SafeURL clears. An unsafe one is dropped exactly as before: no
// script, no header, no trace of the target anywhere in the response.
func TestRouter_postActionRedirectNavigatesOnlySafeTargets(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	r.Mount("/x", redirectPage{})
	srv := serve(t, r)

	c := &http.Client{}
	_, page := do(t, srv, http.MethodGet, "/x", "")

	for i, target := range []string{"/dest", "javascript:alert(1)"} { // Go, Evil
		req, _ := http.NewRequest(http.MethodPost, srv.URL+actionURL(t, page, "r", i), strings.NewReader("{}"))
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Datastar-Request", "true")
		resp, err := c.Do(req)
		require.NoError(t, err)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if i == 0 {
			assert.Contains(t, resp.Header.Get("Content-Type"), "text/javascript")
			assert.JSONEq(t, `{"data-via-to":"/dest"}`, resp.Header.Get("datastar-script-attributes"))
			assert.NotContains(t, string(body), target,
				"the target rides in a header attribute, never in the script bytes the CSP hashes")
			continue
		}
		assert.NotContains(t, resp.Header.Get("Content-Type"), "text/javascript",
			"an unsafe Redirect target must not be delivered as an executable script")
		assert.Empty(t, resp.Header.Get("datastar-script-attributes"),
			"no script attributes header for a target %q that may not navigate", target)
		assert.NotContains(t, string(body), target, "an unsafe redirect target must never reach the client")
	}
}

// OnInit runs per request before the (ctx-free) View, so a page can load
// session data into its fields and render it. Without it, a plain page could
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
	jarPost(t, c, srv.URL+actionURL(t, page, "r", 0)) // SignIn → sets the session cookie
	assert.Contains(t, jarGet(t, c, srv.URL+"/p"), "hi alice",
		"OnInit must load the session before the ctx-free View renders")
}

// threadPage reads a named path param (the {id} in /thread/{id}) in OnInit.
type threadPage struct{ id int }

func (p *threadPage) OnInit(ctx *via.Ctx) error { p.id = ctx.Param[int]("id"); return nil }
func (p *threadPage) View() h.H                 { return h.Div(h.P(h.Str("thread "), h.Str(p.id))) }

// echoPage proves a path param is readable inside an ACTION (not just OnInit) on
// a param'd mount — the action POST URL carries the {id} segment (/e/7/_via/a/r/0).
type echoPage struct{ echoed int }

func (p *echoPage) Echo(ctx *via.Ctx) { p.echoed = ctx.Param[int]("id") }
func (p *echoPage) View() h.H {
	return h.Div(h.Button(via.On("click", p.Echo)), h.P(h.Str("echoed "), h.Str(p.echoed)))
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
	assert.Regexp(t, `action="/p/_via/a/r/[A-Za-z0-9_-]+"`, page, "posting to the form's own action endpoint")

	resp := uploadPOST(&http.Client{CheckRedirect: noFollow}, t, srv.URL+actionURL(t, page, "r", 0), "me.png", "PNGBYTES")
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode, "a Redirect in the handler must 303")
	assert.Equal(t, "me.png", cap.name, "handler must receive the filename")
	assert.Equal(t, "PNGBYTES", cap.body, "handler must receive the file bytes")
	assert.Equal(t, int64(len("PNGBYTES")), cap.size, "the header must report the parsed byte count")
	assert.Equal(t, "application/octet-stream", cap.ctype, "the header must report the part's declared type")
}

// multipartTempFiles lists os.TempDir() entries matching the "multipart-*"
// pattern mime/multipart's Reader uses for a part it spills to disk.
func multipartTempFiles(t *testing.T) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(os.TempDir(), "multipart-*"))
	require.NoError(t, err)
	return matches
}

// deferred req.MultipartForm.RemoveAll() is what deletes a part's spilled
// temp file — deleting that defer entirely fails no other test, since none of
// them upload a file big enough to spill past maxActionBody in the first
// place. This one deliberately does.
func TestPostForm_removesSpilledMultipartTempFilesAfterHandling(t *testing.T) {
	// Not t.Parallel(): os.TempDir() is process-wide, and mime/multipart's
	// spill files all share the "multipart-*" prefix regardless of which
	// test created them. Redirect TMPDIR to a private dir instead of
	// sharing the real one with whatever else is mid-upload.
	t.Setenv("TMPDIR", t.TempDir())
	r := via.NewRouter()
	r.Mount("/p", avatarPage{cap: &capture{}})
	srv := serve(t, r)
	_, page := do(t, srv, http.MethodGet, "/p", "")

	before := multipartTempFiles(t)

	big := strings.Repeat("x", 2<<20) // > maxActionBody (1 MiB): the part spills to a temp file
	resp := uploadPOST(&http.Client{CheckRedirect: noFollow}, t, srv.URL+actionURL(t, page, "r", 0), "big.bin", big)
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)

	// The removal races the client observing the response (the server's own
	// post-handler teardown runs after the response is already flushed), so
	// poll briefly rather than asserting on the very next instant.
	assert.Eventually(t, func() bool {
		return len(multipartTempFiles(t)) == len(before)
	}, time.Second, 5*time.Millisecond,
		"no multipart temp file spilled during this upload may survive the handler returning")
}

// The body cap for PostForm rises to maxUploadBytes (files are the payload);
// an oversize multipart body is rejected, not buffered/spilled whole.
func TestPostForm_rejectsOversizeUpload413(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	r.Mount("/p", avatarPage{cap: &capture{}})
	srv := serve(t, r)

	big := strings.Repeat("x", 9<<20) // > maxUploadBytes (8 MiB)
	resp := uploadPOST(&http.Client{CheckRedirect: noFollow}, t, srv.URL+"/p/_via/a/r/0", "big.bin", big)
	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
}

// secret is a session-gated page: its own OnInit redirects to /login when no
// acct is in the session, replacing the removed guard mechanism.
type secret struct{}

func (s *secret) OnInit(ctx *via.Ctx) error {
	if _, ok := ctx.Session().Get[acct](); !ok {
		ctx.Redirect("/login")
	}
	return nil
}
func (s *secret) View() h.H { return h.Div(h.Str("secret area")) }

var noFollow = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

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
	assert.Regexp(t, `@post\('/e/7/_via/a/r/[A-Za-z0-9_-]+'`, page, "action URL must carry the concrete {id} segment")
	_, body := do(t, srv, http.MethodPost, actionURL(t, page, "r", 0), "{}")
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
	resp, _ := do(t, srv, http.MethodPost, actionURL(t, page, "r", 0), "{}")
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

// An OnInit Redirect protects the action sub-route too (not just the page
// GET): an unauthenticated action POST is redirected before any handler runs.
func TestRouter_onInitRedirectProtectsActionPost(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	r.Mount("/secret", secret{})
	srv := serve(t, r)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/secret/_via/a/r/0", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Datastar-Request", "true")
	resp, err := (&http.Client{CheckRedirect: noFollow}).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusSeeOther, resp.StatusCode, "OnInit's Redirect must gate the action route too, not just the page")
	assert.Equal(t, "/login", resp.Header.Get("Location"))
}

// unsafeRedirectPage's OnInit always redirects to a scheme SafeURL rejects —
// the same shape as an OnInit authored with a mistyped or hostile target.
type unsafeRedirectPage struct{}

func (p *unsafeRedirectPage) OnInit(ctx *via.Ctx) error {
	ctx.Redirect("javascript:alert(1)")
	return nil
}
func (p *unsafeRedirectPage) View() h.H { return h.Div() }

// An OnInit redirect goes through the same hcore.SafeURL check as any other
// Redirect: an unsafe target must never reach http.Redirect.
func TestRouter_onInitRedirectRejectsUnsafeTarget(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	r.Mount("/secret", unsafeRedirectPage{})
	srv := serve(t, r)

	c := &http.Client{CheckRedirect: noFollow}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/secret", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("Location"), "an unsafe OnInit redirect target must never reach http.Redirect")
}

// A session-gated page redirects (303) to the login path when the required
// session value is absent.
func TestRouter_onInitRedirectsWhenSessionAbsent(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	r.Mount("/secret", secret{})
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

// With the required session present, OnInit does not redirect and the page
// renders.
func TestRouter_onInitAllowsWhenSessionPresent(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	r.Mount("/login", loginForm{})
	r.Mount("/secret", secret{})
	srv := serve(t, r)

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: noFollow}
	loginPage := jarGet(t, c, srv.URL+"/login")
	postForm(c, t, srv.URL+actionURL(t, loginPage, "r", 0), "name", "alice") // sets acct in the session
	assert.Contains(t, jarGet(t, c, srv.URL+"/secret"), "secret area",
		"OnInit must not redirect once the session has the required value")
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
	assert.Regexp(t, `<form method="post" enctype="multipart/form-data" action="/login/_via/a/r/[A-Za-z0-9_-]+"`, page,
		"PostForm must render a native multipart form posting to the form endpoint")

	resp := postForm(&http.Client{CheckRedirect: noFollow}, t, srv.URL+actionURL(t, page, "r", 0), "name", "alice")
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
	r := via.NewRouter(via.WithTrustedOrigin("https://childder.example"))
	r.Mount("/login", loginForm{})
	srv := serve(t, r)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("name", "alice")
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/login/_via/a/r/0", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// An unbound form action id fails closed (410), so a stale client re-bootstraps.
func TestRouter_postFormUnknownActionIsGone(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	r.Mount("/login", loginForm{})
	srv := serve(t, r)
	_, page := do(t, srv, http.MethodGet, "/login", "")
	url := swapActionID(t, actionURL(t, page, "r", 0), "zzzzzzzz")

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

	resp := postForm(&http.Client{CheckRedirect: noFollow}, t, srv.URL+"/login/_via/a/r/0", "name", strings.Repeat("x", 9<<20))
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
	resp := postForm(&http.Client{CheckRedirect: noFollow}, t, srv.URL+actionURL(t, page, "r", 0), "name", "") // empty → no redirect
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
	resp := postForm(&http.Client{CheckRedirect: noFollow}, t, srv.URL+actionURL(t, page, "r", 0), "name", "evil")
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

	_, a := do(t, srv, http.MethodGet, "/a", "")
	assert.Contains(t, a, `<h1>0</h1>`)
	assert.Contains(t, a, `@post('`+actionURL(t, a, "r", 1)+`'`, "page /a's Inc must post under /a")
	_, b := do(t, srv, http.MethodGet, "/b", "")
	assert.Contains(t, b, `@post('`+actionURL(t, b, "r", 1)+`'`, "page /b's Inc must post under /b")

	do(t, srv, http.MethodPost, actionURL(t, a, "r", 1), "{}")
	_, a2 := do(t, srv, http.MethodGet, "/a", "")
	assert.Contains(t, a2, `<h1>1</h1>`, "/a's counter must reflect its action")
	_, b2 := do(t, srv, http.MethodGet, "/b", "")
	assert.Contains(t, b2, `<h1>0</h1>`, "/b must be unaffected by an action on /a")
}

// Mounting at "/" must namespace to the root (no prefix): the page posts to
// /_via/a/r/{act}, exactly like a single-page Handler.
func TestRouter_mountAtRootHasNoPrefix(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	r.Mount("/", counter{count: &store{}})
	srv := serve(t, r)

	_, body := do(t, srv, http.MethodGet, "/", "")
	assert.Regexp(t, `@post\('/_via/a/r/[A-Za-z0-9_-]+'`, body, "root mount must post to /_via/a/{act} with no prefix")
	resp, after := do(t, srv, http.MethodPost, actionURL(t, body, "r", 1), "{}")
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
	resp, body := do(t, srv, http.MethodPost, actionURL(t, page, "r", 1), "{}")
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
	resp, _ := do(t, serve(t, r), http.MethodPost, "/x/_via/a/r/0", "{}")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// Handler is Mount at "/" internally — ONE dispatch pipeline. The observable
// consequence: a single-page Handler(root) serves the router-only transports
// too (a native PostForm posts to /_via/a/r/0 and 303s). Fails if Handler grows
// its own separate mux again.
func TestHandler_isMountAtRootOneDispatchPipeline(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(loginForm{}))

	_, page := do(t, srv, http.MethodGet, "/", "")
	resp := postForm(&http.Client{CheckRedirect: noFollow}, t, srv.URL+actionURL(t, page, "r", 0), "name", "alice")
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode, "Handler must serve the form transport like any mount")
	assert.Equal(t, "/welcome", resp.Header.Get("Location"))
}

// The unified pipeline carries the live machinery too: a live child mounted on
// a Router (not just Handler) bootstraps the SSE stream from its page — the
// body carries @post('<base>/_via/sse') and the reconnect manager. Fails if
// Mount loses the live bootstrap detection.
func TestMount_livePageBootstrapsStreamUnderTheRouter(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	r.Mount("/live", quietChild{})
	_, body := do(t, serve(t, r), http.MethodGet, "/live", "")
	assert.Contains(t, body, `@post('/live/_via/sse')`, "a mounted streaming page must bootstrap its own SSE endpoint")
	assert.Contains(t, body, "window.__viaRC", "the reconnect manager rides the mounted streaming page")
}

// jobBar is a live child under a parametrised mount: it reads the mount's
// {id} in its own OnInit and ticks, so the page only works if the browser can
// actually open the stream the page advertised.
type jobBar struct {
	id  int
	pct via.State[int]
}

func (g *jobBar) OnInit(ctx *via.Ctx) error {
	g.id = ctx.Param[int]("id")
	ctx.Tick(15*time.Millisecond, g.poll)
	return nil
}
func (g *jobBar) poll(*via.Ctx) { g.pct.Set(g.pct.Get() + g.id) }
func (g *jobBar) View() h.H     { return h.Div(h.Str("pct "), g.pct.Display()) }

type jobPage struct{ Bar jobBar }

func (p *jobPage) View() h.H { return h.Main(via.Child(p.Bar)) }

// A page under a parametrised mount must advertise the CONCRETE SSE path. The
// pattern base would be POSTed literally by the browser (/job/%7Bid%7D/_via/sse),
// miss the route, and leave every live child under such a mount dead — while
// the same page's action URLs already carried the concrete segment, so the two
// halves of the library disagreed.
func TestMount_advertisesTheConcreteSSEURLUnderAParametrisedMount(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	r.Mount("/job/{id}", jobPage{})
	srv := liveServer(t, r)

	_, page := do(t, srv, http.MethodGet, "/job/7", "")
	m := regexp.MustCompile(`data-init="@post\('([^']+)'\)"`).FindStringSubmatch(page)
	require.NotNil(t, m, "a streaming page must bootstrap its stream:\n%s", page)
	assert.Equal(t, "/job/7/_via/sse", m[1], "the advertised stream URL must be the concrete request path")

	lines, cancel := openStreamAt(t, srv, m[1])
	defer cancel()
	awaitTabID(t, lines)
	awaitLine(t, lines, "pct ")
}

// slugPage is a LIVE page under a param'd mount: it holds a Tick (so the page
// gets the data-init="@post('…/_via/sse')" bootstrap), an action binding (so
// every data-on:@post URL carries the segment too), and a PostForm (so the
// form action attribute does), and it echoes the raw segment through
// Param[string] so the escaping can be proven not to corrupt the value.
type slugPage struct{ slug string }

func (p *slugPage) OnInit(ctx *via.Ctx) error {
	p.slug = ctx.Param[string]("slug")
	ctx.Tick(time.Hour, p.tick)
	return nil
}
func (p *slugPage) tick(ctx *via.Ctx) {}
func (p *slugPage) Bump(ctx *via.Ctx) {}
func (p *slugPage) View() h.H {
	return h.Div(
		h.P(h.Str("slug=["), h.Str(p.slug), h.Str("]")),
		h.Button(via.On("click", p.Bump), h.Str("b")),
		via.PostForm(p.Bump, h.Button(h.Str("go"))),
	)
}

func slugServer(t *testing.T) *httptest.Server {
	t.Helper()
	r := via.NewRouter()
	r.Mount("/t/{slug}", slugPage{})
	return serve(t, r)
}

// A mount param is concatenated into a Datastar expression (@post('…')) inside
// an HTML attribute, and Datastar EVALUATES that expression as JavaScript under
// a CSP carrying 'unsafe-eval'. A quote in the segment would close the string
// literal and run whatever follows. The assertion is on the attribute CONTENT,
// not the absence of a payload string: the escaped form must be exactly the
// percent-encoded segment inside one unbroken quoted literal.
func TestRouter_paramCannotBreakOutOfADatastarExpression(t *testing.T) {
	t.Parallel()
	srv := slugServer(t)

	_, page := do(t, srv, http.MethodGet, "/t/x%27%2Balert(1)%2B%27y", "")

	assert.Contains(t, page, `data-init="@post('/t/x%27+alert%281%29+%27y/_via/sse')"`,
		"the SSE bootstrap must carry the segment percent-encoded, inside one quoted literal")
	assert.Regexp(t, `data-on:click="@post\('/t/x%27\+alert%281%29\+%27y/_via/a/r/[A-Za-z0-9_-]+'\)"`, page,
		"every action URL must carry the segment percent-encoded")
	assert.Regexp(t, `<form method="post" enctype="multipart/form-data" action="/t/x%27\+alert%281%29\+%27y/_via/a/r/[A-Za-z0-9_-]+">`, page,
		"the PostForm action must carry it too")
	assert.NotContains(t, page, "@post('/t/x'", "the segment must never terminate the JS string literal")
}

// The double quote closes the ATTRIBUTE rather than the JS literal, so
// path-escaping alone is not enough: the value must be HTML-escaped where it is
// written. (PathEscape leaves '"' alone.)
func TestRouter_paramCannotBreakOutOfTheAttribute(t *testing.T) {
	t.Parallel()
	srv := slugServer(t)

	_, page := do(t, srv, http.MethodGet, `/t/a%22%20onload=alert(1)%20x=%22b`, "")

	assert.Contains(t, page, `data-init="@post('/t/a%22%20onload=alert%281%29%20x=%22b/_via/sse')"`)
	assert.NotContains(t, page, `" onload=`, "an unescaped quote would graft a live attribute into <body>")
}

// '&' must not start an entity inside the attribute, and '</script>' must not
// be able to close an element — both are written escaped.
func TestRouter_paramAmpersandAndTagAreEscaped(t *testing.T) {
	t.Parallel()
	srv := slugServer(t)

	_, page := do(t, srv, http.MethodGet, `/t/a%26amp%3Bb`, "")
	assert.Contains(t, page, `data-init="@post('/t/a&amp;amp%3Bb/_via/sse')"`,
		"'&' must be HTML-escaped in the attribute; PathEscape leaves it alone")

	_, page2 := do(t, srv, http.MethodGet, `/t/%3C%2Fscript%3E`, "")
	assert.Contains(t, page2, `data-init="@post('/t/%3C%2Fscript%3E/_via/sse')"`)
	assert.NotContains(t, page2, "@post('/t/</script>", "no raw tag may reach an attribute from a segment")
}

// Escaping must not corrupt the VALUE: a non-ASCII segment still round-trips
// to Param[string] intact, and the URL it mints is a valid path the mux routes.
func TestRouter_nonASCIIParamRoundTrips(t *testing.T) {
	t.Parallel()
	srv := slugServer(t)

	_, page := do(t, srv, http.MethodGet, "/t/caf%C3%A9-%F0%9F%8E%89", "")

	assert.Contains(t, page, "slug=[café-🎉]", "Param[string] must see the decoded segment")
	assert.Contains(t, page, `data-init="@post('/t/caf%C3%A9-%F0%9F%8E%89/_via/sse')"`,
		"the minted URL must be the percent-encoded form the mux decodes back")
}

type badInitSig struct{ N via.Signal[int] }

func (b *badInitSig) OnInit(*via.Ctx) {}
func (b *badInitSig) View() h.H       { return h.Div(b.N.Display()) }

type misnamedReload struct{ N via.Signal[int] }

func (m *misnamedReload) Reload(*via.Ctx) error { return nil }
func (m *misnamedReload) View() h.H             { return h.Div(m.N.Display()) }

type reloadHelper struct{ N via.Signal[int] }

func (r *reloadHelper) Reload(*via.Ctx) error       { return nil }
func (r *reloadHelper) OnReload(ctx *via.Ctx) error { return r.Reload(ctx) }
func (r *reloadHelper) View() h.H                   { return h.Div(r.N.Display()) }

type refreshAction struct{ N via.Signal[int] }

func (b *refreshAction) Refresh(*via.Ctx) {}
func (b *refreshAction) View() h.H        { return h.Div(b.N.Display()) }

type badMetaSig struct{ N via.Signal[int] }

func (b *badMetaSig) PageMeta(string) via.Meta { return via.Meta{} }
func (b *badMetaSig) View() h.H                { return h.Div(b.N.Display()) }

type misnamedMeta struct{ N via.Signal[int] }

func (m *misnamedMeta) Metadata() via.Meta { return via.Meta{Title: "x"} }
func (m *misnamedMeta) View() h.H          { return h.Div(m.N.Display()) }

type metaHelper struct{ N via.Signal[int] }

func (r *metaHelper) Metadata() via.Meta { return via.Meta{Title: "x"} }
func (r *metaHelper) PageMeta() via.Meta { return r.Metadata() }
func (r *metaHelper) View() h.H          { return h.Div(r.N.Display()) }

type legacyTitle struct{ N via.Signal[int] }

func (l *legacyTitle) Title() string { return "x" }
func (l *legacyTitle) View() h.H     { return h.Div(l.N.Display()) }

func TestMount_panicsOnAPageMetaCarryingTheWrongSignature(t *testing.T) {
	t.Parallel()
	assert.PanicsWithValue(t,
		"via: via_test.badMetaSig.PageMeta has signature func(string) via.Meta, not func() via.Meta — "+
			"so via_test.badMetaSig does NOT implement via.PageMetaer and the hook will never run",
		func() { via.NewRouter().Mount("/", badMetaSig{}) })
}

func TestMount_warnsOnAMethodShapedLikeAMisnamedPageMeta(t *testing.T) {
	logged := captureLog(t, func() { via.NewRouter().Mount("/", misnamedMeta{}) })
	assert.Contains(t, logged, "misnamedMeta.Metadata looks like a mis-named PageMeta")
	assert.Contains(t, logged, "var _ via.PageMetaer = (*misnamedMeta)(nil)")
}

func TestMount_staysQuietWhenThePageMetaLookalikeIsAHelperItCalls(t *testing.T) {
	logged := captureLog(t, func() { via.NewRouter().Mount("/", metaHelper{}) })
	assert.NotContains(t, logged, "mis-named")
}

// Title was the hook PageMeta replaced. A leftover one still compiles and still
// looks like it names the page, so the drop must be loud.
func TestMount_warnsOnALeftoverTitleMethod(t *testing.T) {
	logged := captureLog(t, func() { via.NewRouter().Mount("/", legacyTitle{}) })
	assert.Contains(t, logged, "legacyTitle.Title is no longer a via hook")
}

func TestMount_panicsOnAHookNameCarryingTheWrongSignature(t *testing.T) {
	t.Parallel()
	assert.PanicsWithValue(t,
		"via: via_test.badInitSig.OnInit has signature func(*via.Ctx), not func(*via.Ctx) error — "+
			"so via_test.badInitSig does NOT implement via.Initer and the hook will never run",
		func() { via.NewRouter().Mount("/", badInitSig{}) })
}

func TestMount_warnsOnAMethodShapedLikeAMisnamedHook(t *testing.T) {
	logged := captureLog(t, func() { via.NewRouter().Mount("/", misnamedReload{}) })
	assert.Contains(t, logged, "misnamedReload.Reload looks like a mis-named OnReload")
	assert.Contains(t, logged, "var _ via.Reloader = (*misnamedReload)(nil)")
}

func TestMount_staysQuietWhenTheLookalikeIsJustAHelperTheRealHookCalls(t *testing.T) {
	logged := captureLog(t, func() { via.NewRouter().Mount("/", reloadHelper{}) })
	assert.NotContains(t, logged, "mis-named")
}

func TestMount_staysQuietForAnOrdinaryActionThatSharesAHookLookalikeName(t *testing.T) {
	logged := captureLog(t, func() { via.NewRouter().Mount("/", refreshAction{}) })
	assert.NotContains(t, logged, "mis-named")
}

func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)
	fn()
	return buf.String()
}

// connReqEchoer reads the connect request in OnInit and a no-op tick forces a
// push so the read value is observable on the stream.
type connReqEchoer struct{ host via.State[string] }

func (e *connReqEchoer) OnInit(ctx *via.Ctx) error {
	e.host.Set(ctx.Request().Host)
	ctx.Tick(20*time.Millisecond, e.push)
	return nil
}

func (e *connReqEchoer) push(ctx *via.Ctx) {}

func (e *connReqEchoer) View() h.H {
	return h.Div(h.P(h.Str("host: "), e.host.Display()))
}

// OnInit must see the SSE connect request, so a child can authorize or
// inspect the connection at open time. "example.com" is httptest's fixed
// in-memory network host, not a real loopback address.
func TestOnInit_seesTheConnectRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := liveServer(t, via.Handler(connReqEchoer{}))

		lines, cancel := openStream(t, srv)
		defer cancel()
		awaitLine(t, lines, "host: example.com")
	})
}

// An OnInit Redirect must gate the SSE connect itself, beyond the page GET
// and the action route — before A3 the stream handler ran OnInit at all, so
// a session check left a streaming page's push channel open to anyone who knew
// the URL even though the page and its actions were protected.
func TestLive_streamRunsOnInitRedirect(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	r.Mount("/secret", secret{})
	srv := serve(t, r)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/secret/_via/sse", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := (&http.Client{CheckRedirect: noFollow}).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusSeeOther, resp.StatusCode,
		"an OnInit Redirect must gate the SSE connect too, not just the page and action routes")
	assert.Equal(t, "/login", resp.Header.Get("Location"))
}

// onInitLive loads a field in OnInit, before the connect render ever runs —
// a live child's ticks fire on the connection's own goroutine, so its first
// pushed frame is the proof OnInit's field reached the persistent instance.
type onInitLive struct{ label string }

func (p *onInitLive) OnInit(ctx *via.Ctx) error {
	p.label = "loaded"
	ctx.Tick(time.Millisecond, func(*via.Ctx) {})
	return nil
}

func (p *onInitLive) View() h.H { return h.Div(h.Str(p.label)) }

// OnInit must run before the connect render binds the live unit, so a field
// it loads is already set by the time the first push renders — before A3 the
// stream handler never ran OnInit at all.
func TestLive_onInitRunsBeforeConnectRender(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := liveServer(t, via.Handler(onInitLive{}))

		frame := readFirstFrame(t, srv)
		require.NotEmpty(t, frame)
		assert.Contains(t, strings.Join(frame, "\n"), "loaded")
	})
}

// livePushChild is a live child under a parametrised mount; Bump
// changes its visible count so its action's push is a real patch.
type livePushChild struct {
	n    int
	seen via.State[int]
}

func (k *livePushChild) Bump(ctx *via.Ctx) { k.n++ }

func (k *livePushChild) View() h.H {
	return h.Div(h.Str(k.n), k.seen.Display(), h.Button(via.On("click", k.Bump)))
}

type livePushParent struct{ I livePushChild }

func (p *livePushParent) View() h.H { return h.Div(via.Child(p.I)) }

// A live push under a parametrised mount must carry the concrete path
// segment in its action URLs, not the literal "{id}" pattern wildcard —
// before A3 mount.connect closed over the pattern base instead of computing
// the concrete base per connection, so every push rendered a dead "{id}" URL.
func TestLive_pushUnderParamMountRendersConcreteBase(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := via.NewRouter()
		r.Mount("/thread/{id}", livePushParent{})
		srv := liveServer(t, r)

		lines, cancel := openStreamAt(t, srv, "/thread/7/_via/sse")
		defer cancel()
		tab := awaitTabID(t, lines)

		_, page := do(t, srv, http.MethodGet, "/thread/7", "")
		resp, _ := post(t, srv, actionURL(t, page, "0", 0), withTab(tab, "{}"), map[string]string{
			"Sec-Fetch-Site": "same-origin",
		})
		assert.Equal(t, http.StatusNoContent, resp.StatusCode, "the live action answers 204; the push carries the patch")

		awaitLine(t, lines, "/thread/7/_via/a/0/")
	})
}

// paramChild is embedded under a parametrised mount; Bump changes its
// visible count so the action's response is a real patch, not a 204.
type paramChild struct{ n int }

func (k *paramChild) Bump(ctx *via.Ctx) { k.n++ }

func (k *paramChild) View() h.H { return h.Div(h.Str(k.n), h.Button(via.On("click", k.Bump))) }

type paramParent struct{ I paramChild }

func (p *paramParent) View() h.H { return h.Div(via.Child(p.I)) }

func TestDispatch_pushUnderParamMountRendersConcreteBase(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	r.Mount("/thread/{id}", paramParent{})
	srv := serve(t, r)

	_, page := do(t, srv, http.MethodGet, "/thread/7", "")
	assert.Regexp(t, `@post\('/thread/7/_via/a/0/[A-Za-z0-9_-]+'`, page)

	resp, body := do(t, srv, http.MethodPost, actionURL(t, page, "0", 0), "{}")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotContains(t, body, "{id}", "the re-rendered action URL must not carry the pattern wildcard")
	assert.Contains(t, body, `/thread/7/_via/a/0/`, "the re-rendered action URL must carry the concrete segment")
}

// staleLoader is the week-one shape: OnInit reads the store into a field, the
// action mutates the store, and nothing re-reads it — so the response render
// frames the PRE-action number.
type staleLoader struct {
	s     *store
	shown int
}

func (p *staleLoader) OnInit(ctx *via.Ctx) error { p.shown = p.s.Value(); return nil }

func (p *staleLoader) Bump(ctx *via.Ctx) { p.s.Add(1) }

func (p *staleLoader) View() h.H {
	return h.Div(h.P(h.ID("n"), h.Str(p.shown)), h.Button(via.On("click", p.Bump)))
}

// reloadingLoader is staleLoader with the one method that fixes it.
type reloadingLoader struct {
	s     *store
	shown int
}

func (p *reloadingLoader) OnInit(ctx *via.Ctx) error { return p.OnReload(ctx) }

func (p *reloadingLoader) OnReload(ctx *via.Ctx) error { p.shown = p.s.Value(); return nil }

func (p *reloadingLoader) Bump(ctx *via.Ctx) { p.s.Add(1) }

func (p *reloadingLoader) View() h.H {
	return h.Div(h.P(h.ID("n"), h.Str(p.shown)), h.Button(via.On("click", p.Bump)))
}

func TestReload_rereadsMutatedDataForThePlainActionRender(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(reloadingLoader{s: &store{}}))
	_, page := app.Get("/")
	require.Contains(t, page, `<p id="n">0</p>`)

	code, body := app.Action(0).Fire()
	require.Equal(t, http.StatusOK, code, "a OnReload that changes the render must not answer 204")
	assert.Contains(t, body, `<p id="n">1</p>`,
		"the action's response must show what the handler wrote, not what OnInit loaded before it")
}

// Without OnReload the defect is intact — that is the point of the hook being
// opt-in — but it must no longer be SILENT.
func TestReload_absenceIsLoggedWhenTheActionChangesNothing(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)

	app := vt.Serve(t, via.Handler(staleLoader{s: &store{}}))
	app.Get("/")
	code, _ := app.Action(0).Fire()

	require.Equal(t, http.StatusNoContent, code)
	assert.Contains(t, logs.String(), "changed nothing the render shows")
	assert.Contains(t, logs.String(), "OnReload(*via.Ctx) error",
		"the 204 must name the hook that fixes it")

	// A legitimately idempotent click is a dead click EVERY time. One line per
	// click buries the log instead of reading it.
	logs.Reset()
	for range 5 {
		app.Action(0).Fire()
	}
	assert.NotContains(t, logs.String(), "changed nothing the render shows",
		"the dead-click warning must be deduped per action, not repeated per click")
}

// liveReloader's data lives in the store, not in State — the live push has to
// re-read it or the patched frame carries the pre-action value.
type liveReloader struct {
	s     *store
	shown int
	beat  via.State[int]
}

func (p *liveReloader) OnInit(ctx *via.Ctx) error { return p.OnReload(ctx) }

func (p *liveReloader) OnReload(ctx *via.Ctx) error { p.shown = p.s.Value(); return nil }

func (p *liveReloader) Bump(ctx *via.Ctx) { p.s.Add(1) }

func (p *liveReloader) View() h.H {
	return h.Div(p.beat.Display(), h.P(h.Str("n="+fmt.Sprint(p.shown))), h.Button(via.On("click", p.Bump)))
}

func TestReload_rereadsMutatedDataBeforeTheLivePush(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(liveReloader{s: &store{}}))
	conn := app.Connect()

	code, _ := app.Action(0).Over(conn).Fire()
	require.Less(t, code, 300)
	assert.Contains(t, conn.Await("n=1"), "n=1",
		"the pushed frame must carry post-action data, not the connect-time snapshot")
}

// reloadNotFound models the row an action just deleted: OnReload says the page's
// data is gone, and that answer must reach the client instead of a stale render.
type reloadNotFound struct{ gone bool }

func (p *reloadNotFound) OnReload(ctx *via.Ctx) error {
	if p.gone {
		return via.ErrNotFound
	}
	return nil
}

func (p *reloadNotFound) Drop(ctx *via.Ctx) { p.gone = true }

func (p *reloadNotFound) View() h.H { return h.Div(h.Button(via.On("click", p.Drop))) }

func TestReload_errNotFoundAfterAnActionAnswers404(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(reloadNotFound{}))
	app.Get("/")
	code, body := app.Action(0).Fire()
	assert.Equal(t, http.StatusNotFound, code)
	assert.Contains(t, body, "not found")
}

// reloadRedirector queues its Redirect from OnReload rather than the handler.
type reloadRedirector struct{}

func (p *reloadRedirector) OnReload(ctx *via.Ctx) error { ctx.Redirect("/elsewhere"); return nil }

func (p *reloadRedirector) Go(ctx *via.Ctx) {}

func (p *reloadRedirector) View() h.H { return h.Div(h.Button(via.On("click", p.Go))) }

func TestReload_redirectFromReloadNavigatesTheTab(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(reloadRedirector{}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	resp, _ := post(t, srv, actionURL(t, page, "r", 0), "{}", sameOrigin())

	assert.Contains(t, resp.Header.Get("Content-Type"), "text/javascript")
	assert.JSONEq(t, `{"data-via-to":"/elsewhere"}`, resp.Header.Get("datastar-script-attributes"))
}

// tickingReload registers a Tick from OnReload on a page served PLAIN. Honouring
// it would turn the unit live on a page with no stream (I5) and trip the
// render-invariant panic; OnReload must register nothing.
type tickingReload struct{ n int }

func (p *tickingReload) OnReload(ctx *via.Ctx) error {
	ctx.Tick(time.Second, func(*via.Ctx) {})
	return nil
}

func (p *tickingReload) Bump(ctx *via.Ctx) { p.n++ }

func (p *tickingReload) View() h.H {
	return h.Div(h.P(h.Str(p.n)), h.Button(via.On("click", p.Bump)))
}

func TestReload_tickInsideReloadDoesNotMakeAPlainUnitLive(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)

	app := vt.Serve(t, via.Handler(tickingReload{}))
	_, page := app.Get("/")
	require.NotContains(t, page, "data-init", "the page must be served plain")

	code, body := app.Action(0).Fire()
	assert.Equal(t, http.StatusOK, code, "a Tick in OnReload must be ignored, not fail the action")
	assert.Contains(t, body, "<p>1</p>")
	assert.NotContains(t, logs.String(), "Tick called after OnInit returned",
		"OnReload is not a late OnInit — registering from it is expected and silently ignored")
}

// reloadedChild proves the reload targets the ACTED unit: a child's action
// must re-read the child, not the root.
type reloadedChild struct {
	s     *store
	shown int
}

func (p *reloadedChild) OnReload(ctx *via.Ctx) error { p.shown = p.s.Value(); return nil }

func (p *reloadedChild) OnInit(ctx *via.Ctx) error { return p.OnReload(ctx) }

func (p *reloadedChild) Bump(ctx *via.Ctx) { p.s.Add(1) }

func (p *reloadedChild) View() h.H {
	return h.Div(h.P(h.ID("n"), h.Str(p.shown)), h.Button(via.On("click", p.Bump)))
}

type reloadedChildParent struct{ C reloadedChild }

func (p *reloadedChildParent) View() h.H { return h.Div(via.Child(p.C)) }

func TestReload_runsOnTheActedChildNotTheRoot(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(reloadedChildParent{C: reloadedChild{s: &store{}}}))
	_, page := app.Get("/")
	require.Contains(t, page, `<p id="n">0</p>`)

	code, body := app.Action(0).Raw(actionURL(t, page, "0", 0)).Fire()
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `<p id="n">1</p>`)
}

// closablePage is live by way of a Tick, so Close has a stream goroutine, a
// ticker and a disposer to shut down.
type closablePage struct {
	n     via.State[int]
	gone  chan struct{}
	beats chan struct{}
}

func (p *closablePage) OnInit(ctx *via.Ctx) error {
	ctx.Tick(10*time.Millisecond, p.beat)
	ctx.OnDispose(p.dispose)
	return nil
}
func (p *closablePage) beat(ctx *via.Ctx) {
	p.n.Set(p.n.Get() + 1)
	select {
	case p.beats <- struct{}{}:
	default:
	}
}
func (p *closablePage) dispose()      { close(p.gone) }
func (p *closablePage) Bump(*via.Ctx) { p.n.Set(p.n.Get() + 1) }
func (p *closablePage) View() h.H {
	return h.Div(h.Button(via.On("click", p.Bump), h.Str("bump")), h.Span(p.n.Display()))
}

func closableRouter(t *testing.T) (*via.Router, *closablePage) {
	t.Helper()
	p := &closablePage{gone: make(chan struct{}), beats: make(chan struct{}, 1)}
	r := via.NewRouter()
	// By value, like every Mount: the channels are what the test observes and a
	// copy shares them.
	r.Mount("/", *p)
	return r, p
}

func TestRouterClose_endsAnOpenStreamWithoutTruncatingIt(t *testing.T) {
	t.Parallel()
	r, _ := closableRouter(t)
	app := vt.Serve(t, r)
	conn := app.Connect()
	conn.Await("datastar-patch-elements")

	r.Close()

	assert.NoError(t, conn.AwaitClose(), "a closed router must end the response cleanly, not truncate it")
}

// via.Handler is how every single-page app starts, and a via app owns
// goroutines — so the entry point must hand back the thing that drains them.
// Typed as *via.Router, not http.Handler: no type assertion at the call site.
func TestHandler_returnsTheRouterSoCloseIsReachable(t *testing.T) {
	t.Parallel()
	p := &closablePage{gone: make(chan struct{}), beats: make(chan struct{}, 1)}
	var r *via.Router = via.Handler(*p)
	app := vt.Serve(t, r)
	conn := app.Connect()
	conn.Await("datastar-patch-elements")

	r.Close()

	assert.NoError(t, conn.AwaitClose(), "Close from the Handler entry point must end the stream cleanly")
	select {
	case <-p.gone:
	default:
		assert.Fail(t, "Close returned before the unit's OnDispose ran")
	}
}

func TestRouterClose_waitsForDisposersToRun(t *testing.T) {
	t.Parallel()
	r, p := closableRouter(t)
	app := vt.Serve(t, r)
	app.Connect().Await("datastar-patch-elements")

	r.Close()

	select {
	case <-p.gone:
	default:
		assert.Fail(t, "Close returned before the unit's OnDispose ran")
	}
}

func TestRouterClose_refusesANewStream(t *testing.T) {
	t.Parallel()
	r, _ := closableRouter(t)
	app := vt.Serve(t, r)
	r.Close()

	req, err := http.NewRequest(http.MethodPost, app.URL()+"/_via/sse", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

func TestRouterClose_answersALiveActionOnAClosedTabWith410(t *testing.T) {
	t.Parallel()
	r, _ := closableRouter(t)
	app := vt.Serve(t, r)
	conn := app.Connect()
	conn.Await("datastar-patch-elements")
	r.Close()

	code, _ := app.Action(0).Over(conn).Fire()
	assert.Equal(t, http.StatusGone, code, "an action on a shut-down tab must be refused, not dropped")
}

func TestRouterClose_isSafeToCallTwice(t *testing.T) {
	t.Parallel()
	r, _ := closableRouter(t)
	app := vt.Serve(t, r)
	app.Connect().Await("datastar-patch-elements")

	r.Close()
	r.Close()
}

func TestRouterClose_stopsTheTickGoroutine(t *testing.T) {
	t.Parallel()
	r, p := closableRouter(t)
	app := vt.Serve(t, r)
	conn := app.Connect()
	<-p.beats
	r.Close()
	require.NoError(t, conn.AwaitClose())

	// Drain whatever the last live tick already queued, then prove the timer
	// is gone rather than merely between beats.
	select {
	case <-p.beats:
	default:
	}
	select {
	case <-p.beats:
		assert.Fail(t, "a Tick fired after Close returned")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestRouter_zeroValueServesAndMountsWithoutNewRouter(t *testing.T) {
	t.Parallel()
	r := new(via.Router)
	r.Mount("/", greetPage{})
	app := vt.Serve(t, r)

	code, body := app.Get("/")
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "hi ")
}
