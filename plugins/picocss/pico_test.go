package picocss_test

import (
	"io"
	"net/http"
	"net/http/httptest"
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

func renderPage(t *testing.T, opts ...picocss.PicoOption) string {
	t.Helper()
	if testing.Short() {
		t.Skip("plugin test reaches the picocss CDN; skipped under -short")
	}
	app := via.New(via.WithPlugins(picocss.Plugin(opts...)))
	server := vt.Serve(t, app)
	via.Mount[emptyPage](app, "/")
	t.Cleanup(server.Close)
	resp, err := server.Client().Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
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

func TestPicocss_servesThemeCSS(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("plugin test reaches the picocss CDN; skipped under -short")
	}
	app := via.New(
		via.WithPlugins(picocss.Plugin(picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeBlue}))),
	)
	server := vt.Serve(t, app)
	_ = app
	resp, err := server.Client().Get(server.URL + "/_plugins/picocss/theme/blue")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "text/css", resp.Header.Get("Content-Type"))
}

func picoBlueServer(t *testing.T) *httptest.Server {
	t.Helper()
	if testing.Short() {
		t.Skip("plugin test reaches the picocss CDN; skipped under -short")
	}
	app := via.New(
		via.WithPlugins(picocss.Plugin(picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeBlue}))),
	)
	server := vt.Serve(t, app)
	return server
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
		"a theme that was never fetched must 404, not serve empty CSS")
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

func renderPageBody(t *testing.T, opts ...picocss.PicoOption) string {
	t.Helper()
	if testing.Short() {
		t.Skip("plugin test reaches the picocss CDN; skipped under -short")
	}
	app := via.New(via.WithPlugins(picocss.Plugin(opts...)))
	server := vt.Serve(t, app)
	via.Mount[emptyPage](app, "/")
	t.Cleanup(server.Close)
	resp, err := server.Client().Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

func TestPicocss_WithDefaultTheme_seedsInitialThemeSignal(t *testing.T) {
	t.Parallel()
	body := renderPageBody(t,
		picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeBlue, picocss.PicoThemePurple}),
		picocss.WithDefaultTheme(picocss.PicoThemePurple),
	)
	assert.Contains(t, body, "purple",
		"WithDefaultTheme must seed _picoTheme with the chosen theme name")
}

func TestPicocss_WithClassless_swapsAssetPath(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("plugin test reaches the picocss CDN; skipped under -short")
	}
	app := via.New(
		via.WithPlugins(picocss.Plugin(
			picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeBlue}),
			picocss.WithClassless(),
		)),
	)
	server := vt.Serve(t, app)
	resp, err := server.Client().Get(server.URL + "/_plugins/picocss/theme/classless/blue")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode,
		"WithClassless must arrange the plugin to serve the classless asset")
}

func TestPicocss_WithColorClasses_servesUtilityCSS(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("plugin test reaches the picocss CDN; skipped under -short")
	}
	app := via.New(
		via.WithPlugins(picocss.Plugin(picocss.WithColorClasses())),
	)
	server := vt.Serve(t, app)
	resp, err := server.Client().Get(server.URL + "/_plugins/picocss/color-classes")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode,
		"WithColorClasses must register the color-classes route")
	assert.Equal(t, "text/css", resp.Header.Get("Content-Type"))
}

func colorClassesServer(t *testing.T) *httptest.Server {
	t.Helper()
	if testing.Short() {
		t.Skip("plugin test reaches the picocss CDN; skipped under -short")
	}
	app := via.New(
		via.WithPlugins(picocss.Plugin(picocss.WithColorClasses())),
	)
	server := vt.Serve(t, app)
	return server
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
	dark := renderPageBody(t, picocss.WithDarkMode())
	assert.Contains(t, dark, `_picoDarkMode`,
		"WithDarkMode must set the initial _picoDarkMode signal value")
	assert.Contains(t, dark, "dark")

	light := renderPageBody(t, picocss.WithLightMode())
	assert.Contains(t, light, `_picoDarkMode`)
	assert.Contains(t, light, "light")
}

func TestPicocss_ThemeRef_andDarkModeRef_returnDatastarExpressions(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "$_picoTheme", picocss.ThemeRef(),
		"ThemeRef must surface the $-prefixed signal name for inline Datastar expressions")
	assert.Equal(t, "$_picoDarkMode", picocss.DarkModeRef())
}
