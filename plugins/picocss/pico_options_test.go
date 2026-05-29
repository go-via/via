package picocss_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/plugins/picocss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func renderPageBody(t *testing.T, opts ...picocss.PicoOption) string {
	t.Helper()
	if testing.Short() {
		t.Skip("plugin test reaches the picocss CDN; skipped under -short")
	}
	var server *httptest.Server
	app := via.New(via.WithPlugins(picocss.Plugin(opts...)), via.WithTestServer(&server))
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
	var server *httptest.Server
	via.New(
		via.WithPlugins(picocss.Plugin(
			picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeBlue}),
			picocss.WithClassless(),
		)),
		via.WithTestServer(&server),
	)
	defer server.Close()
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
	var server *httptest.Server
	via.New(
		via.WithPlugins(picocss.Plugin(picocss.WithColorClasses())),
		via.WithTestServer(&server),
	)
	defer server.Close()
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
	var server *httptest.Server
	_ = via.New(
		via.WithPlugins(picocss.Plugin(picocss.WithColorClasses())),
		via.WithTestServer(&server),
	)
	t.Cleanup(func() { server.Close() })
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
