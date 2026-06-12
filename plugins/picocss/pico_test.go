package picocss_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/plugins/picocss"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type emptyPage struct{}

func (e *emptyPage) View(ctx *via.CtxR) h.H { return h.Div() }

func serveApp(t *testing.T, opts ...picocss.PicoOption) *httptest.Server {
	t.Helper()
	app := via.New(via.WithPlugins(picocss.Plugin(opts...)))
	server := vt.Serve(t, app)
	via.Mount[emptyPage](app, "/")
	return server
}

func renderPage(t *testing.T, opts ...picocss.PicoOption) string {
	t.Helper()
	server := serveApp(t, opts...)
	resp, err := server.Client().Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

var hashedThemeCSS = regexp.MustCompile(`/via/assets/picocss/[0-9a-f]+/pico\.[a-z.]+\.min\.css`)

type refusingTransport struct{ t *testing.T }

func (rt refusingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rt.t.Errorf("plugin registration performed network I/O: %s %s", r.Method, r.URL)
	return nil, http.ErrUseLastResponse
}

func TestPicoPlugin_registersWithoutNetwork(t *testing.T) { //nolint:paralleltest // mutates global http.DefaultTransport
	// Mutates http.DefaultTransport, so this test must not be parallel.
	orig := http.DefaultTransport
	http.DefaultTransport = refusingTransport{t: t}
	defer func() { http.DefaultTransport = orig }()

	assert.NotPanics(t, func() {
		via.New(via.WithPlugins(picocss.Plugin(
			picocss.WithThemes(picocss.AllPicoThemes),
			picocss.WithColorClasses(),
		)))
	}, "Register must do zero network I/O — boot must succeed offline")
}

func TestPicocss_initialSignalsIncludeThemeAndDarkMode(t *testing.T) {
	t.Parallel()
	body := renderPage(t)
	assert.Contains(t, body, "_picoTheme")
	assert.Contains(t, body, "_picoDarkMode")
}

func TestPicocss_attachesStylesheetLink(t *testing.T) {
	t.Parallel()
	body := renderPage(t)
	assert.Contains(t, body, `id="_picoThemeLink"`,
		"plugin must inject a <link id=\"_picoThemeLink\"> into the document head")
}

func TestPage_rendersNoThirdPartyScriptWithoutIntegrity(t *testing.T) {
	t.Parallel()
	body := renderPage(t)

	assert.NotContains(t, body, `src="http`,
		"the default plugin must not reference any third-party script origin")
	assert.NotContains(t, body, `src="//`,
		"protocol-relative third-party origins are third-party too")
	assert.NotContains(t, body, `href="http`,
		"theme CSS must come from the embedded assets, not a CDN")
}

func TestPicocss_servesThemeAtContentHashedImmutablePath(t *testing.T) {
	t.Parallel()
	server := serveApp(t, picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeBlue}))

	resp, err := server.Client().Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	path := hashedThemeCSS.FindString(string(body))
	require.NotEmpty(t, path, "rendered page must reference the content-hashed theme CSS")

	asset, err := server.Client().Get(server.URL + path)
	require.NoError(t, err)
	defer asset.Body.Close()
	require.Equal(t, http.StatusOK, asset.StatusCode)
	assert.Equal(t, "text/css", asset.Header.Get("Content-Type"))
	assert.Equal(t, "public, max-age=31536000, immutable",
		asset.Header.Get("Cache-Control"),
		"hashed URLs change with content, so the response may cache forever")
	css, _ := io.ReadAll(asset.Body)
	assert.Contains(t, string(css), "Pico",
		"the served body must be the real vendored Pico CSS")
}

func TestPicocss_hashedAssetReturns404ForStaleHash(t *testing.T) {
	t.Parallel()
	server := serveApp(t, picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeBlue}))

	resp, err := server.Client().Get(
		server.URL + "/via/assets/picocss/0000000000000000/pico.blue.min.css")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode,
		"a hash that no longer matches the embedded content must 404, not serve a mismatched body")
}

func TestPicocss_servesThemeCSS(t *testing.T) {
	t.Parallel()
	server := serveApp(t, picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeBlue}))
	resp, err := server.Client().Get(server.URL + "/_plugins/picocss/theme/blue")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "text/css", resp.Header.Get("Content-Type"))
}

func picoBlueServer(t *testing.T) *httptest.Server {
	t.Helper()
	return serveApp(t, picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeBlue}))
}

func TestPicocss_servesUncompressedCSSWhenGzipNotAccepted(t *testing.T) {
	t.Parallel()
	server := picoBlueServer(t)

	// Go's transport auto-adds Accept-Encoding: gzip; "identity" opts out
	// so the uncompressed branch runs.
	req, _ := http.NewRequest("GET", server.URL+"/_plugins/picocss/theme/blue", nil)
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := server.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, 200, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("Content-Encoding"),
		"without gzip in Accept-Encoding the CSS must be served uncompressed")
	body, _ := io.ReadAll(resp.Body)
	assert.NotEmpty(t, body, "uncompressed branch must still return the CSS body")
}

// The gzip and identity bodies are distinct representations of one URL, so they
// must carry distinct ETags and a Vary: Accept-Encoding header — otherwise a
// shared/intermediary cache can hand a gzipped body to a client that didn't ask
// for it (corrupted CSS), and a cross-encoding If-None-Match could 304 the wrong
// representation. (RFC 7232 §2.3.3 / RFC 9110.)
func TestPicocss_assetCachingIsRepresentationSpecific(t *testing.T) {
	t.Parallel()
	server := picoBlueServer(t)
	url := server.URL + "/_plugins/picocss/theme/blue"

	gzReq, _ := http.NewRequest("GET", url, nil)
	gzReq.Header.Set("Accept-Encoding", "gzip")
	gz, err := server.Client().Do(gzReq)
	require.NoError(t, err)
	defer gz.Body.Close()

	idReq, _ := http.NewRequest("GET", url, nil)
	idReq.Header.Set("Accept-Encoding", "identity")
	id, err := server.Client().Do(idReq)
	require.NoError(t, err)
	defer id.Body.Close()

	assert.Equal(t, "Accept-Encoding", gz.Header.Get("Vary"),
		"gzip response must Vary on Accept-Encoding so caches key per encoding")
	assert.Equal(t, "Accept-Encoding", id.Header.Get("Vary"),
		"identity response must Vary on Accept-Encoding too")
	assert.NotEqual(t, id.Header.Get("ETag"), gz.Header.Get("ETag"),
		"gzip and identity are distinct representations and must not share an ETag")
}

func TestPicocss_returns404ForUnknownTheme(t *testing.T) {
	t.Parallel()
	server := picoBlueServer(t)

	resp, err := server.Client().Get(server.URL + "/_plugins/picocss/theme/no-such-theme")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode,
		"a theme that was never configured must 404, not serve empty CSS")
}

func TestPicocss_revalidatesThemeCSSWithETag(t *testing.T) {
	t.Parallel()
	server := picoBlueServer(t)
	url := server.URL + "/_plugins/picocss/theme/blue"

	first, err := server.Client().Get(url)
	require.NoError(t, err)
	first.Body.Close()
	etag := first.Header.Get("ETag")
	require.NotEmpty(t, etag, "theme CSS must carry an ETag for revalidation")

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("If-None-Match", etag)
	second, err := server.Client().Do(req)
	require.NoError(t, err)
	defer second.Body.Close()
	assert.Equal(t, http.StatusNotModified, second.StatusCode,
		"a matching If-None-Match must yield 304, not re-send the body")
}

func TestPicocss_WithDefaultTheme_seedsInitialThemeSignal(t *testing.T) {
	t.Parallel()
	body := renderPage(t,
		picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeBlue, picocss.PicoThemePurple}),
		picocss.WithDefaultTheme(picocss.PicoThemePurple),
	)
	assert.Contains(t, body, "purple",
		"WithDefaultTheme must seed _picoTheme with the chosen theme name")
}

func TestPicocss_WithClassless_swapsAssetPath(t *testing.T) {
	t.Parallel()
	server := serveApp(t,
		picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeBlue}),
		picocss.WithClassless(),
	)
	resp, err := server.Client().Get(server.URL + "/_plugins/picocss/theme/classless/blue")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode,
		"WithClassless must arrange the plugin to serve the classless asset")
}

func TestPicocss_WithColorClasses_servesUtilityCSS(t *testing.T) {
	t.Parallel()
	server := serveApp(t, picocss.WithColorClasses())
	resp, err := server.Client().Get(server.URL + "/_plugins/picocss/color-classes")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode,
		"WithColorClasses must register the color-classes route")
	assert.Equal(t, "text/css", resp.Header.Get("Content-Type"))
}

func colorClassesServer(t *testing.T) *httptest.Server {
	t.Helper()
	return serveApp(t, picocss.WithColorClasses())
}

func TestPicocss_colorClassesServedUncompressedWhenGzipNotAccepted(t *testing.T) {
	t.Parallel()
	server := colorClassesServer(t)

	req, _ := http.NewRequest("GET", server.URL+"/_plugins/picocss/color-classes", nil)
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := server.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, 200, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("Content-Encoding"),
		"without gzip in Accept-Encoding the utility CSS must be served uncompressed")
	body, _ := io.ReadAll(resp.Body)
	assert.NotEmpty(t, body, "uncompressed branch must still return the CSS body")
}

func TestPicocss_colorClassesRevalidatesWithETag(t *testing.T) {
	t.Parallel()
	server := colorClassesServer(t)
	url := server.URL + "/_plugins/picocss/color-classes"

	first, err := server.Client().Get(url)
	require.NoError(t, err)
	first.Body.Close()
	etag := first.Header.Get("ETag")
	require.NotEmpty(t, etag, "color-classes CSS must carry an ETag for revalidation")

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("If-None-Match", etag)
	second, err := server.Client().Do(req)
	require.NoError(t, err)
	defer second.Body.Close()
	assert.Equal(t, http.StatusNotModified, second.StatusCode,
		"a matching If-None-Match must yield 304 for the color-classes asset")
}

func TestPicocss_WithDarkMode_andWithLightMode_setInitialDarkModeSignal(t *testing.T) {
	t.Parallel()
	dark := renderPage(t, picocss.WithDarkMode())
	assert.Contains(t, dark, `_picoDarkMode`,
		"WithDarkMode must set the initial _picoDarkMode signal value")
	assert.Contains(t, dark, "dark")

	light := renderPage(t, picocss.WithLightMode())
	assert.Contains(t, light, `_picoDarkMode`)
	assert.Contains(t, light, "light")
}

func TestPicocss_WithDarkMode_conflictsWithWithLightMode(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() {
		picocss.Plugin(picocss.WithDarkMode(), picocss.WithLightMode())
	}, "forcing both dark and light mode is a programming error and must panic at registration")
}

func TestPicocss_WithThemes_panicsWhenSetTwice(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() {
		picocss.Plugin(
			picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeBlue}),
			picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeRed}),
		)
	}, "a second WithThemes silently overriding the first hides a programming error")
}

func TestPicocss_WithThemes_panicsOnInvalidList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		themes []picocss.PicoTheme
	}{
		{"empty list", nil},
		{"unknown theme", []picocss.PicoTheme{"mauve"}},
		{"duplicate entry", []picocss.PicoTheme{picocss.PicoThemeBlue, picocss.PicoThemeBlue}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Panics(t, func() {
				picocss.Plugin(picocss.WithThemes(tt.themes))
			}, "an invalid theme list has no embedded asset to serve and must panic at registration")
		})
	}
}

func TestPicocss_WithDefaultTheme_panicsWhenSetTwice(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() {
		picocss.Plugin(
			picocss.WithDefaultTheme(picocss.PicoThemeBlue),
			picocss.WithDefaultTheme(picocss.PicoThemeRed),
		)
	}, "two default themes conflict and must panic at registration")
}

func TestPicocss_WithDefaultTheme_panicsWhenOutsideConfiguredThemes(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() {
		picocss.Plugin(
			picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeBlue}),
			picocss.WithDefaultTheme(picocss.PicoThemePurple),
		)
	}, "a default theme outside WithThemes would render an unloadable stylesheet")
}

func TestPicocss_WithDefaultTheme_panicsOnUnknownTheme(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() {
		picocss.Plugin(picocss.WithDefaultTheme("mauve"))
	}, "an unknown theme name has no embedded asset and must panic at registration")
}

func TestPicocss_WithDefaultTheme_aloneEnablesThatTheme(t *testing.T) {
	t.Parallel()
	body := renderPage(t, picocss.WithDefaultTheme(picocss.PicoThemePurple))
	assert.Contains(t, body, "purple",
		"WithDefaultTheme without WithThemes must make the chosen theme available")
}

func TestPicocss_ThemeRef_andDarkModeRef_returnDatastarExpressions(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "$_picoTheme", picocss.ThemeRef(),
		"ThemeRef must surface the $-prefixed signal name for inline Datastar expressions")
	assert.Equal(t, "$_picoDarkMode", picocss.DarkModeRef())
}
