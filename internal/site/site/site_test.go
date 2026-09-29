package site_test

import (
	"bytes"
	"cmp"
	"encoding/json"
	"html"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/shell"
	"go-via.dev/site/site"
)

const testVersion = "test-build"

func siteServer(t *testing.T, opts site.Options) *httptest.Server {
	t.Helper()
	opts.Version = testVersion
	app, mux := site.New(opts)
	t.Cleanup(app.Close)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, srv *httptest.Server, path string, hdr http.Header) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	require.NoError(t, err)
	for k, v := range hdr {
		req.Header[k] = v
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, string(body)
}

func TestSite_rendersEveryMountedPage(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	for _, p := range shell.Pages() {
		t.Run(p.Path, func(t *testing.T) {
			t.Parallel()
			resp, body := get(t, srv, p.Path, nil)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Contains(t, body, "<h1>"+html.EscapeString(cmp.Or(p.Heading, p.Title))+"</h1>")
		})
	}
}

func TestSite_answersUnknownPathWithTheErrorPage(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	resp, body := get(t, srv, "/no-such-page", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Contains(t, body, "<h1>Not found</h1>")
}

func TestSite_servesHealthzWithTheVersion(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	resp, body := get(t, srv, "/healthz", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "ok "+testVersion+"\n", body)
}

func TestSite_servesRobotsAndRedirectsFavicon(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	resp, body := get(t, srv, "/robots.txt", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "User-agent: *\nAllow: /\n", body)

	resp, _ = get(t, srv, "/favicon.ico", nil)
	assert.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
	assert.Equal(t, "/static/brand/icon-amber-ink.svg", resp.Header.Get("Location"))
}

func TestMux_healthzAndRobotsCarryHeaders(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	resp, _ := get(t, srv, "/healthz", nil)
	assert.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))

	resp, _ = get(t, srv, "/robots.txt", nil)
	assert.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))
	assert.Equal(t, "public, max-age=3600", resp.Header.Get("Cache-Control"))
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
}

func TestSite_namesItsCanonicalURLOnlyWhenTheOriginIsKnown(t *testing.T) {
	t.Parallel()

	_, body := get(t, siteServer(t, site.Options{Origin: "https://go-via.dev"}), "/actions", nil)
	assert.Contains(t, body, `<link rel="canonical" href="https://go-via.dev/actions">`)

	_, body = get(t, siteServer(t, site.Options{}), "/actions", nil)
	assert.NotContains(t, body, `rel="canonical"`)
}

var voteAction = regexp.MustCompile(`data-via-q-click="([^"]*a=0)" data-on:click="@post\('([^']+)'[^"]*">vote<`)

// postAction fires the vote demo's Cast: action names are hashed per render,
// so the URL is read off the page rather than spelled.
func postAction(t *testing.T, srv *httptest.Server, origin string) *http.Response {
	t.Helper()
	_, body := get(t, srv, "/actions", nil)
	m := voteAction.FindStringSubmatch(body)
	require.NotNil(t, m, "no vote button on /actions")
	req, err := http.NewRequest(http.MethodPost, srv.URL+html.UnescapeString(m[2]+m[1]), strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Origin", origin)
	req.Header.Set("Datastar-Request", "true")
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestSite_refusesACrossOriginActionWhenTheOriginIsSet(t *testing.T) {
	t.Parallel()

	resp := postAction(t, siteServer(t, site.Options{Origin: "https://go-via.dev"}), "https://evil.example")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	resp = postAction(t, siteServer(t, site.Options{}), "https://evil.example")
	assert.Equal(t, http.StatusGone, resp.StatusCode,
		"without a trusted origin the origin is not checked; the 410 comes from dispatch, not an origin refusal")
}

var signInForm = regexp.MustCompile(`<form[^>]*action="([^"]+)"`)

// sessionCookie signs in through the /security form: no page mints a session
// on a GET, so the cookie exists only once a handler has stored something.
func sessionCookie(t *testing.T, srv *httptest.Server) *http.Cookie {
	t.Helper()
	_, body := get(t, srv, "/security", nil)
	m := signInForm.FindStringSubmatch(body)
	require.NotNil(t, m, "no sign-in form on /security")

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	// /security streams, so the form's tab id is filled client-side from
	// $viatab. This client has none; Auth is not live, so the action runs on
	// the plain path anyway.
	require.NoError(t, mw.WriteField("_viatab", ""))
	require.NoError(t, mw.WriteField("name", "tester"))
	require.NoError(t, mw.Close())

	req, err := http.NewRequest(http.MethodPost, srv.URL+m[1], &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Origin", srv.URL)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "via_session" {
			cookie = c
		}
	}
	require.NotNil(t, cookie, "no via_session cookie after signing in")
	return cookie
}

func TestSite_marksTheSessionCookieSecureOnlyWhenTheOriginIsSet(t *testing.T) {
	t.Parallel()

	assert.True(t, sessionCookie(t, siteServer(t, site.Options{Origin: "https://go-via.dev"})).Secure)
	assert.False(t, sessionCookie(t, siteServer(t, site.Options{})).Secure)
}

func TestSite_redirectsPlatformToSecurity(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	resp, _ := get(t, srv, "/platform", nil)
	assert.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
	assert.Equal(t, "/security", resp.Header.Get("Location"))
}

var anyID = regexp.MustCompile(`\sid="([^"]+)"`)
var bareHeading = regexp.MustCompile(`<h[23]>`)

func TestSite_givesEveryHeadingAUniqueAnchor(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	for _, p := range shell.Pages() {
		t.Run(p.Path, func(t *testing.T) {
			t.Parallel()
			_, body := get(t, srv, p.Path, nil)
			assert.NotRegexp(t, bareHeading, body, "every h2 and h3 carries an id")
			seen := map[string]bool{}
			for _, m := range anyID.FindAllStringSubmatch(body, -1) {
				assert.False(t, seen[m[1]], "id %q appears twice", m[1])
				seen[m[1]] = true
			}
		})
	}
}

func TestSite_prefixesLinksAndMountsWithTheBase(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{
		Base:     "/v0.8",
		Versions: []shell.Version{{Label: "v0.9", Base: ""}, {Label: "v0.8", Base: "/v0.8"}},
	})

	resp, body := get(t, srv, "/v0.8/actions", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, `href="/v0.8/signals"`)
	assert.Regexp(t, `href="/v0.8/static/[0-9a-f]{8}/site\.css"`, body)
	assert.Contains(t, body, `@post('/v0.8/actions/_via/a/`)
	assert.Contains(t, body, `<p class="old-version" role="note">You are reading the docs for v0.8. <a href="/actions">Read the latest (v0.9)</a></p>`)
	assert.Contains(t, body, `<meta name="robots" content="noindex">`)
	assert.Contains(t, body, `<a href="/v0.8/actions" aria-current="page">v0.8</a>`, "the version picker marks the one being read")

	resp, _ = get(t, srv, "/v0.8/static/site.css", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	resp, _ = get(t, srv, "/actions", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	resp, _ = get(t, srv, "/v0.8", nil)
	assert.Equal(t, 3, resp.StatusCode/100, "the bare base redirects to its trailing-slash front page")
	assert.Equal(t, "/v0.8/", resp.Header.Get("Location"))

	_, body = get(t, siteServer(t, site.Options{}), "/actions", nil)
	assert.NotContains(t, body, `class="old-version"`)
	assert.NotContains(t, body, `name="robots"`)
}

var (
	searchSlot = regexp.MustCompile(`data-bind="([^"]+)"`)
	searchPost = regexp.MustCompile(`data-via-q-input__debounce\.250ms="([^"]*)" data-on:input__debounce\.250ms="@post\('([^']+)'`)
)

func TestSite_searchReturnsAnchoredHits(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	_, body := get(t, srv, "/deploy", nil)
	slot := searchSlot.FindStringSubmatch(body)
	post := searchPost.FindStringSubmatch(body)
	require.NotNil(t, slot, "no bound search input on /deploy")
	require.NotNil(t, post, "no search action on /deploy")

	req, err := http.NewRequest(http.MethodPost, srv.URL+html.UnescapeString(post[2]+post[1]),
		strings.NewReader(`{"viatab":"","`+slot[1]+`":"shutdown order"}`))
	require.NoError(t, err)
	req.Header.Set("Datastar-Request", "true")
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	out, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(out), `href="/deploy#shutdown-order"`)
}

var counterInc = regexp.MustCompile(`data-on:click="@post\('([^']+)'\)">\+<`)

func postFrom(t *testing.T, srv *httptest.Server, path, body, client string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Datastar-Request", "true")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("X-Forwarded-For", client)
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func TestSite_limitsTheFrontPageCounterPerClient(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	_, body := get(t, srv, "/", nil)
	inc := counterInc.FindStringSubmatch(body)
	slot := searchSlot.FindStringSubmatch(body)
	find := searchPost.FindStringSubmatch(body)
	require.NotNil(t, inc, "no + button on /")
	require.NotNil(t, slot, "no bound search input on /")
	require.NotNil(t, find, "no search action on /")
	incURL := html.UnescapeString(inc[1])

	require.Equal(t, http.StatusOK, postFrom(t, srv, incURL, "{}", "203.0.113.1"))
	limited := false
	for range 3 * 30 {
		if postFrom(t, srv, incURL, "{}", "203.0.113.1") == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	require.True(t, limited, "the counter never answered 429 within three times its budget")

	assert.Equal(t, http.StatusOK, postFrom(t, srv, incURL, "{}", "203.0.113.2"), "another client has its own budget")
	assert.Equal(t, http.StatusOK, postFrom(t, srv, html.UnescapeString(find[2]+find[1]),
		`{"viatab":"","`+slot[1]+`":"shutdown order"}`, "203.0.113.1"), "search on / is not the counter")
}

func TestSite_labelsTheCellsOfWideTables(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	_, body := get(t, srv, "/migrate", nil)
	assert.Contains(t, body, `<td data-label="Caught by">`, "under 50rem a row stacks and hides its header row")
	_, body = get(t, srv, "/start", nil)
	assert.NotContains(t, body, `data-label="What via does"`, "a two-column table needs no labels")
}

func TestSite_foldsTheContentsListAfterTheLead(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})
	_, body := get(t, srv, "/security", nil)
	lead := strings.Index(body, "An action is an HTTP POST")
	toc := strings.Index(body, `class="toc toc-inline"`)
	h2 := strings.Index(body, `<h2 id="origin-checks-and-csrf"`)
	require.True(t, lead >= 0 && toc >= 0 && h2 >= 0, "lead %d, toc %d, h2 %d", lead, toc, h2)
	assert.Less(t, lead, toc)
	assert.Less(t, toc, h2)
}

func TestSite_leavesTheContentsRailOffTheFrontPage(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})
	_, body := get(t, srv, "/", nil)
	assert.NotContains(t, body, "toc-rail")
	_, body = get(t, srv, "/security", nil)
	assert.Contains(t, body, "toc-rail")
}

func TestSite_keepsASearchInTheTopBarOfTheNotFoundPage(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})
	_, body := get(t, srv, "/no-such-page", nil)
	tools := strings.Index(body, `class="top-tools"`)
	require.GreaterOrEqual(t, tools, 0)
	assert.Contains(t, body[tools:strings.Index(body, `class="side"`)], `name="q"`)
}

func TestSite_quotesTheOriginNoticeViaLogsVerbatim(t *testing.T) {
	t.Parallel()
	var logged bytes.Buffer
	via.NewRouter(via.WithLogger(slog.New(slog.NewJSONHandler(&logged, nil))))
	var rec struct{ Msg string }
	require.NoError(t, json.Unmarshal(logged.Bytes(), &rec))
	require.Contains(t, rec.Msg, "WithTrustedOrigin", "precondition: the startup warning is the one line logged")

	srv := siteServer(t, site.Options{})
	_, body := get(t, srv, "/security", nil)
	assert.Contains(t, html.UnescapeString(body), rec.Msg)
}

func TestSite_servesEveryPageAndAnchorTheReadmeLinks(t *testing.T) {
	t.Parallel()
	readme, err := os.ReadFile("../../../README.md")
	require.NoError(t, err)
	links := regexp.MustCompile(`https://go-via\.dev(/[^)#\s]*)(?:#([^)\s]+))?`).FindAllStringSubmatch(string(readme), -1)
	require.NotEmpty(t, links, "the README links no go-via.dev page")
	srv := siteServer(t, site.Options{})

	seen := map[string]bool{}
	for _, l := range links {
		if seen[l[0]] {
			continue
		}
		seen[l[0]] = true
		t.Run(l[0], func(t *testing.T) {
			t.Parallel()
			resp, body := get(t, srv, l[1], nil)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			if l[2] != "" {
				assert.True(t, strings.Contains(body, `id="`+l[2]+`"`), "%s has no #%s anchor", l[1], l[2])
			}
		})
	}
}

func TestSite_pagerLinksTheNeighboursOutsideMain(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	written := shell.Pages()
	for i, p := range written {
		t.Run(p.Path, func(t *testing.T) {
			t.Parallel()
			_, body := get(t, srv, p.Path, nil)
			pager := strings.Index(body, `<nav class="pager"`)
			require.NotEqual(t, -1, pager, "no pager")
			assert.Greater(t, pager, strings.Index(body, "</main>"), "the pager sits outside <main>, where search does not read")
			if i > 0 {
				assert.Contains(t, body, `<a class="pager-prev" href="`+written[i-1].Path+`" rel="prev">`)
			}
			if p.Path == "/" {
				assert.Contains(t, body, `<a class="pager-next" href="/why" rel="next">`, "the front page leads to Why via")
				return
			}
			if i+1 < len(written) {
				assert.Contains(t, body, `<a class="pager-next" href="`+written[i+1].Path+`" rel="next">`)
				assert.Contains(t, body, html.EscapeString(written[i+1].Title))
			} else {
				assert.NotContains(t, body, `class="pager-next"`)
			}
		})
	}
}

func TestSite_listsContentsFromTwoHeadings(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	_, body := get(t, srv, "/signals", nil)
	assert.Contains(t, body, `<nav class="toc toc-rail" aria-label="On this page">`)
	assert.Contains(t, body, `<details class="toc toc-inline">`)
	assert.Contains(t, body, `<a href="#greeting">Greeting</a>`, "a demo card's heading is an h3 entry")
	assert.Contains(t, body, `<a href="#top" class="to-top">`)

	_, body = get(t, srv, "/actions", nil)
	if strings.Count(body, `<h2 id=`)+strings.Count(body, `<h3 id=`) < 2 {
		assert.NotContains(t, body, `class="toc`)
	}
}

func TestSite_linksEachPageToItsSource(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	const src = "https://github.com/go-via/via/blob/main/internal/site/content/"
	_, body := get(t, srv, "/signals", nil)
	assert.Contains(t, body, `href="`+src+`signals.go"`)
	_, body = get(t, srv, "/", nil)
	assert.Contains(t, body, `href="`+src+`landing.go"`)
}

func TestSite_redirectsATrailingSlashToTheCanonicalPath(t *testing.T) {
	t.Parallel()

	resp, _ := get(t, siteServer(t, site.Options{}), "/reference/?x=1", nil)
	assert.Equal(t, http.StatusPermanentRedirect, resp.StatusCode)
	assert.Equal(t, "/reference?x=1", resp.Header.Get("Location"))

	resp, _ = get(t, siteServer(t, site.Options{}), "/no-such-page/", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	resp, _ = get(t, siteServer(t, site.Options{
		Base:     "/v0.8",
		Versions: []shell.Version{{Label: "v0.9", Base: ""}, {Label: "v0.8", Base: "/v0.8"}},
	}), "/v0.8/actions/", nil)
	assert.Equal(t, http.StatusPermanentRedirect, resp.StatusCode)
	assert.Equal(t, "/v0.8/actions", resp.Header.Get("Location"))
}

func TestSite_notFoundSuggestsAPageAndSearches(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	resp, body := get(t, srv, "/refrence", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Contains(t, body, `Did you mean <a href="/reference">`)
	assert.Contains(t, body, `name="q"`)

	_, body = get(t, srv, "/nothing-here?q=shutdown+order", nil)
	assert.Contains(t, body, `href="/deploy#shutdown-order"`)
}

func TestSite_writesSocialMetaOffTheOrigin(t *testing.T) {
	t.Parallel()

	_, body := get(t, siteServer(t, site.Options{Origin: "https://go-via.dev"}), "/signals", nil)
	assert.Contains(t, body, `<meta property="og:image" content="https://go-via.dev/static/brand/punch-dark.png">`)
	assert.Contains(t, body, `<meta property="og:url" content="https://go-via.dev/signals">`)
	assert.Contains(t, body, `<meta name="twitter:card" content="summary_large_image">`)
	assert.Contains(t, body, `<meta name="theme-color" content="#1b1e24">`)
	assert.Contains(t, body, `rel="apple-touch-icon"`)

	_, body = get(t, siteServer(t, site.Options{}), "/signals", nil)
	assert.NotContains(t, body, `og:image`, "og:image must be absolute, so no origin is no image")
	assert.NotContains(t, body, `og:url`)
}

func TestSite_servesASitemapOfEveryPage(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{Origin: "https://go-via.dev"})

	resp, body := get(t, srv, "/sitemap.xml", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/xml")
	assert.Contains(t, body, "<loc>https://go-via.dev/signals</loc>")
	assert.Contains(t, body, "<loc>https://go-via.dev/</loc>")
	for _, p := range shell.Pages() {
		assert.Contains(t, body, "<loc>https://go-via.dev"+p.Path+"</loc>")
	}

	_, body = get(t, srv, "/robots.txt", nil)
	assert.Contains(t, body, "Sitemap: https://go-via.dev/sitemap.xml\n")

	resp, _ = get(t, siteServer(t, site.Options{}), "/sitemap.xml", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "a sitemap needs absolute URLs")
}

func TestSite_tablesHaveAHeaderRow(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	_, body := get(t, srv, "/reference", nil)
	assert.Contains(t, body, `<thead><tr><th scope="col">`)
	assert.Contains(t, body, `<tbody>`)
}

var (
	hrefAttr = regexp.MustCompile(`\shref="([^"]*)"`)
	idAttr   = regexp.MustCompile(`\sid="([^"]*)"`)
)

func TestSite_everyInternalLinkResolvesToAPageAndAnchor(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	paths := []string{"/_ui"}
	for _, p := range shell.Pages() {
		paths = append(paths, p.Path)
	}
	bodies := map[string]string{}
	ids := map[string]map[string]bool{}
	for _, p := range paths {
		_, body := get(t, srv, p, nil)
		bodies[p] = body
		ids[p] = map[string]bool{}
		for _, m := range idAttr.FindAllStringSubmatch(body, -1) {
			ids[p][html.UnescapeString(m[1])] = true
		}
	}

	static := map[string]int{}
	for _, from := range paths {
		base := &url.URL{Path: from}
		for _, m := range hrefAttr.FindAllStringSubmatch(bodies[from], -1) {
			raw := html.UnescapeString(m[1])
			u, err := url.Parse(raw)
			if !assert.NoError(t, err, "%s: href %q", from, raw) {
				continue
			}
			if u.Scheme != "" || u.Host != "" {
				continue
			}
			to := base.ResolveReference(u)
			if strings.HasPrefix(to.Path, "/static/") {
				if _, seen := static[to.Path]; !seen {
					resp, _ := get(t, srv, to.Path, nil)
					static[to.Path] = resp.StatusCode
				}
				assert.Equal(t, http.StatusOK, static[to.Path], "%s: href %q", from, raw)
				continue
			}
			target, ok := ids[to.Path]
			if !assert.True(t, ok, "%s: href %q is not a page", from, raw) {
				continue
			}
			// "#top" scrolls to the top of any document (HTML spec), no id needed.
			if to.Fragment != "" && to.Fragment != "top" {
				assert.True(t, target[to.Fragment], "%s: href %q has no #%s on %s", from, raw, to.Fragment, to.Path)
			}
		}
	}
}

var cspCode = regexp.MustCompile(`<code class="(csp-has|csp-absent)">([^<]*)</code>`)

func TestSecurity_printsTheDirectivesItsResponseCarries(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	resp, body := get(t, srv, "/security", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	policy := resp.Header.Get("Content-Security-Policy")
	require.NotEmpty(t, policy)

	matches := cspCode.FindAllStringSubmatch(body, -1)
	require.NotEmpty(t, matches, "the page rendered no tagged directives")

	var present, absent int
	for _, m := range matches {
		directive := html.UnescapeString(m[2])
		if m[1] == "csp-absent" {
			absent++
			assert.NotContains(t, policy, directive, "the page says %q is absent", directive)
			continue
		}
		present++
		assert.Contains(t, policy, directive)
	}
	assert.NotZero(t, present, "the page listed no directive the header carries")
	assert.NotZero(t, absent, "the page listed no directive the header omits")
}
